package importer

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// Config holds importer configuration.
type Config struct {
	SourceDir      string
	CSVDelimiter   string
	CSVNullMarker  string
	TruncateBefore bool
	CommitInterval int
	ErrorPolicy    string // skip_row/stop/log_only
	MaxErrors      int
	MaxWorkers     int
	DateTimeFormat string // e.g. "yyyyMMddHHmmss"
	TrimStrings    bool
	TargetDBType   string // "postgres", "mysql", "oracle" — affects quoting and placeholders
	SourceEncoding string // ""=UTF-8, "GBK", "LATIN1" — CSV file encoding
	TargetEncoding string // ""=UTF-8, "GBK", "LATIN1" — target database encoding
	Logger         *zap.Logger
	QuoteAllIdentifiers bool
}

// Importer reads CSV files and inserts data into a target database.
type Importer struct {
	db     *sql.DB
	cfg    Config
	logger *zap.Logger
	dec    *encoding.Decoder // source → UTF-8 (nil if no conversion needed)
}

// New creates a new Importer.
func New(db *sql.DB, cfg Config) *Importer {
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	if cfg.CommitInterval == 0 {
		cfg.CommitInterval = 1000
	}
	if cfg.CSVDelimiter == "" {
		cfg.CSVDelimiter = ","
	}
	if cfg.CSVNullMarker == "" {
		cfg.CSVNullMarker = "\\N"
	}

	// Initialize encoding decoder if source encoding is specified and differs from UTF-8
	imp := &Importer{db: db, cfg: cfg, logger: cfg.Logger}
	if enc := getEncoding(cfg.SourceEncoding); enc != nil {
		imp.dec = enc.NewDecoder()
	}
	return imp
}

// getEncoding returns the encoding for a named charset, or nil for UTF-8/no-op.
func getEncoding(name string) encoding.Encoding {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "GBK", "GB2312", "GB18030":
		return simplifiedchinese.GBK
	case "LATIN1", "ISO-8859-1":
		return charmap.ISO8859_1
	case "LATIN9", "ISO-8859-15":
		return charmap.ISO8859_15
	case "WINDOWS-1252":
		return charmap.Windows1252
	case "SHIFT_JIS", "SJIS":
		return simplifiedchinese.GBK // approximate; shift-jis not common
	default:
		return nil
	}
}

// isMySQL returns true if the target database is MySQL or a MySQL-compatible dialect.
func (imp *Importer) isMySQL() bool {
	t := strings.ToLower(imp.cfg.TargetDBType)
	return t == "mysql" || t == "goldendb" || strings.HasSuffix(t, "-mysql")
}

// isOracle returns true if the target database is Oracle or an Oracle-compatible dialect.
func (imp *Importer) isOracle() bool {
	t := strings.ToLower(imp.cfg.TargetDBType)
	return t == "oracle" || strings.HasSuffix(t, "-oracle")
}

// ImportResult holds the result of importing one table.
type ImportResult struct {
	Schema   string
	Table    string
	Expected int64
	Actual   int64
	Skipped  int64
	Errors   int64
	Duration time.Duration
	Err      error
}

// ImportTables imports CSV data for multiple tables.
func (imp *Importer) ImportTables(ctx context.Context, tables []*md.TableDef, schemaMapping map[string]string) ([]ImportResult, error) {
	workers := imp.cfg.MaxWorkers
	if workers <= 0 {
		workers = 1
	}
	if workers > len(tables) {
		workers = len(tables)
	}

	tableCh := make(chan *md.TableDef, len(tables))
	for _, t := range tables {
		tableCh <- t
	}
	close(tableCh)

	var (
		results []ImportResult
		mu      sync.Mutex
		wg      sync.WaitGroup
	)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for tbl := range tableCh {
				select {
				case <-ctx.Done():
					return
				default:
				}

				targetSchema := tbl.TableSchema
				if m, ok := schemaMapping[targetSchema]; ok {
					targetSchema = m
				}

				result := imp.importOneTable(ctx, tbl, targetSchema)
				mu.Lock()
				results = append(results, result)
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	return results, nil
}

func (imp *Importer) importOneTable(ctx context.Context, tbl *md.TableDef, targetSchema string) ImportResult {
	start := time.Now()
	key := fmt.Sprintf("%s.%s", tbl.TableSchema, tbl.TableName)
	result := ImportResult{Schema: targetSchema, Table: tbl.TableName}

	imp.logger.Info("Import started",
		zap.String("table", key),
		zap.String("target", fmt.Sprintf("%s.%s", targetSchema, tbl.TableName)),
	)

	// Set Oracle session date/timestamp formats for automatic string conversion
	if imp.isOracle() {
		if _, err := imp.db.ExecContext(ctx, "ALTER SESSION SET NLS_DATE_FORMAT = 'YYYY-MM-DD HH24:MI:SS'"); err != nil {
			imp.logger.Warn("Failed to set NLS_DATE_FORMAT", zap.Error(err))
		}
		if _, err := imp.db.ExecContext(ctx, "ALTER SESSION SET NLS_TIMESTAMP_FORMAT = 'YYYY-MM-DD HH24:MI:SS'"); err != nil {
			imp.logger.Warn("Failed to set NLS_TIMESTAMP_FORMAT", zap.Error(err))
		}
		if _, err := imp.db.ExecContext(ctx, "ALTER SESSION SET NLS_TIMESTAMP_TZ_FORMAT = 'YYYY-MM-DD HH24:MI:SS TZH:TZM'"); err != nil {
			imp.logger.Warn("Failed to set NLS_TIMESTAMP_TZ_FORMAT", zap.Error(err))
		}
	}

	// Read CSV file
	filename := fmt.Sprintf("%s.%s.csv", strings.ToLower(tbl.TableSchema), strings.ToLower(tbl.TableName))
	filePath := filepath.Join(imp.cfg.SourceDir, filename)
	f, openErr := os.Open(filePath)
	if openErr != nil {
		result.Err = fmt.Errorf("open CSV: %w", openErr)
		return result
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	// Read header
	header, err := reader.Read()
	if err != nil {
		result.Err = fmt.Errorf("read header: %w", err)
		return result
	}

	// Read all rows
	var allRows [][]string
	for {
		record, err := reader.Read()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			result.Err = fmt.Errorf("read CSV: %w", err)
			return result
		}
		allRows = append(allRows, record)
	}

	result.Expected = int64(len(allRows))
	if result.Expected == 0 {
		imp.logger.Info("No data to import", zap.String("table", key))
		return result
	}

	// Build INSERT statement
	quotedCols := make([]string, len(header))
	for i, h := range header {
		quotedCols[i] = imp.quoteIdent(h)
	}
	// Generate placeholders: ? for MySQL, :1/:2 for Oracle, $1/$2 for PG
	placeholders := make([]string, len(header))
	if imp.isMySQL() {
		for i := range placeholders {
			placeholders[i] = "?"
		}
	} else if imp.isOracle() {
		for i := range placeholders {
			placeholders[i] = fmt.Sprintf(":%d", i+1)
		}
	} else {
		for i := range placeholders {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
		}
	}

	insertSQL := fmt.Sprintf("INSERT INTO %s.%s (%s) VALUES (%s)",
		imp.quoteIdent(targetSchema), imp.quoteIdent(tbl.TableName),
		strings.Join(quotedCols, ", "),
		strings.Join(placeholders, ", "),
	)

	// Truncate if configured
	if imp.cfg.TruncateBefore {
		truncSQL := fmt.Sprintf("TRUNCATE TABLE %s.%s",
			imp.quoteIdent(targetSchema), imp.quoteIdent(tbl.TableName))
		if _, err := imp.db.ExecContext(ctx, truncSQL); err != nil {
			imp.logger.Warn("TRUNCATE failed (table may not exist yet)", zap.Error(err))
		}
	}

	// Insert in batches with transaction control
	var (
		skipped  int64
		errCount int64
		inserted int64
		tx       *sql.Tx
	)

	beginTx := func() error {
		var e error
		tx, e = imp.db.BeginTx(ctx, nil)
		return e
	}
	commitTx := func() error {
		if tx == nil {
			return nil
		}
		return tx.Commit()
	}

	if err := beginTx(); err != nil {
		result.Err = fmt.Errorf("begin transaction: %w", err)
		return result
	}

	for i, row := range allRows {
		// Convert CSV values to SQL values with data transforms
		vals := make([]any, len(row))
		for j, v := range row {
			if v == imp.cfg.CSVNullMarker {
				vals[j] = nil
			} else {
				val := imp.transformValue(v)
				if j < len(header) {
					if imp.needsNumericBoolean(tbl, header[j]) {
						val = numericBooleanValue(val)
					}
					if imp.isBinaryColumn(tbl, header[j]) {
						decoded, err := hex.DecodeString(v)
						if err != nil {
							result.Err = fmt.Errorf("row %d column %s: decode hex: %w", i, header[j], err)
							return result
						}
						val = decoded
					}
				}
				vals[j] = val
			}
		}

		var execErr error
		if tx != nil {
			_, execErr = tx.ExecContext(ctx, insertSQL, vals...)
		} else {
			_, execErr = imp.db.ExecContext(ctx, insertSQL, vals...)
		}

		if execErr != nil {
			errCount++
			switch imp.cfg.ErrorPolicy {
			case "stop":
				commitTx()
				result.Err = fmt.Errorf("row %d: %w", i, execErr)
				result.Actual = inserted
				result.Skipped = skipped
				result.Errors = errCount
				return result
			case "skip_row":
				skipped++
				imp.logger.Warn("Skipping row",
					zap.Int("row", i),
					zap.Error(execErr),
				)
				if imp.cfg.MaxErrors > 0 && errCount >= int64(imp.cfg.MaxErrors) {
					commitTx()
					result.Err = fmt.Errorf("max errors (%d) reached", imp.cfg.MaxErrors)
					result.Actual = inserted
					result.Skipped = skipped
					result.Errors = errCount
					return result
				}
				// Rollback to savepoint or just continue with next row
				continue
			case "log_only":
				imp.logger.Warn("Row error (continuing)",
					zap.Int("row", i),
					zap.Error(execErr),
				)
			}
		} else {
			inserted++
		}

		// Commit interval
		if inserted > 0 && inserted%int64(imp.cfg.CommitInterval) == 0 {
			if err := commitTx(); err != nil {
				result.Err = fmt.Errorf("commit at row %d: %w", inserted, err)
				return result
			}
			imp.logger.Debug("Committed",
				zap.Int64("rows", inserted),
				zap.String("table", key),
			)
			if err := beginTx(); err != nil {
				result.Err = fmt.Errorf("begin tx after commit: %w", err)
				return result
			}
		}
	}

	// Final commit
	if err := commitTx(); err != nil {
		result.Err = fmt.Errorf("final commit: %w", err)
		return result
	}

	result.Actual = inserted
	result.Skipped = skipped
	result.Errors = errCount
	result.Duration = time.Since(start)

	imp.logger.Info("Import completed",
		zap.String("table", key),
		zap.Int64("expected", result.Expected),
		zap.Int64("actual", result.Actual),
		zap.Int64("skipped", result.Skipped),
		zap.Duration("elapsed", result.Duration),
	)

	return result
}

// transformValue applies data transformations to a CSV value before INSERT.
func (imp *Importer) transformValue(v string) any {
	s := v

	// Decode from source encoding to UTF-8 if configured
	if imp.dec != nil {
		decoded, err := imp.dec.String(s)
		if err != nil {
			// Fallback to original on decode error
			imp.logger.Warn("Encoding decode failed, using original", zap.Error(err))
		} else {
			s = decoded
		}
	}

	// Trim strings
	if imp.cfg.TrimStrings {
		s = strings.TrimSpace(s)
	}

	// Detect and convert compact datetime formats
	// "yyyyMMddHHmmss" (14 digits) → "YYYY-MM-DD HH24:MI:SS"
	if imp.cfg.DateTimeFormat == "yyyyMMddHHmmss" && len(s) == 14 && isAllDigits(s) {
		// 19801217000000 → 1980-12-17 00:00:00
		formatted := fmt.Sprintf("%s-%s-%s %s:%s:%s",
			s[0:4], s[4:6], s[6:8],
			s[8:10], s[10:12], s[12:14])
		return formatted
	}

	// "yyyyMMdd" (8 digits) → "YYYY-MM-DD"
	if imp.cfg.DateTimeFormat == "yyyyMMdd" && len(s) == 8 && isAllDigits(s) {
		return fmt.Sprintf("%s-%s-%s", s[0:4], s[4:6], s[6:8])
	}

	return s
}

func isAllDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (imp *Importer) needsNumericBoolean(tbl *md.TableDef, columnName string) bool {
	if !imp.isMySQL() && !imp.isOracle() {
		return false
	}
	for _, col := range tbl.GetColumns() {
		if strings.EqualFold(col.ColumnName, columnName) {
			return strings.EqualFold(col.DataType, "boolean")
		}
	}
	return false
}

func numericBooleanValue(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "t", "yes", "y":
		return "1"
	case "false", "f", "no", "n":
		return "0"
	default:
		return v
	}
}

func (imp *Importer) isBinaryColumn(tbl *md.TableDef, columnName string) bool {
	for _, col := range tbl.GetColumns() {
		if strings.EqualFold(col.ColumnName, columnName) {
			switch strings.ToUpper(strings.TrimSpace(col.DataType)) {
			case "BLOB", "BYTEA", "RAW", "BINARY", "VARBINARY":
				return true
			}
		}
	}
	return false
}

func (imp *Importer) quoteIdent(name string) string {
	if imp.cfg.QuoteAllIdentifiers {
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	}
	if imp.isMySQL() {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` // PostgreSQL/Oracle-style
}
