package exporter

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// Config holds exporter configuration.
type Config struct {
	OutputDir         string
	Format            string // csv
	CSVDelimiter      string
	CSVQuoteChar      string
	CSVNullRep        string
	CSVHeader         bool
	CSVLineTerminator string
	PageSize          int
	MaxWorkers        int
	DBType            string // oracle/postgres/mysql
	Logger            *zap.Logger
}

// Exporter reads data from a database and writes to files.
type Exporter struct {
	db     *sql.DB
	cfg    Config
	logger *zap.Logger
}

// New creates a new Exporter.
func New(db *sql.DB, cfg Config) *Exporter {
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	if cfg.PageSize == 0 {
		cfg.PageSize = 5000
	}
	if cfg.CSVDelimiter == "" {
		cfg.CSVDelimiter = ","
	}
	if cfg.CSVQuoteChar == "" {
		cfg.CSVQuoteChar = "\""
	}
	if cfg.CSVNullRep == "" {
		cfg.CSVNullRep = "\\N"
	}
	if cfg.CSVLineTerminator == "" {
		cfg.CSVLineTerminator = "\n"
	}
	return &Exporter{db: db, cfg: cfg, logger: cfg.Logger}
}

// TableResult holds the result of exporting one table.
type TableResult struct {
	Schema     string
	Table      string
	Rows       int64
	Batches    int
	Duration   time.Duration
	OutputFile string
	Error      error
}

// ExportTables exports multiple tables, optionally in parallel.
func (e *Exporter) ExportTables(ctx context.Context, tables []*md.TableDef, primaryKeys map[string][]string) ([]TableResult, error) {
	if err := os.MkdirAll(e.cfg.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	workers := e.cfg.MaxWorkers
	if workers <= 0 {
		workers = 1
	}
	if workers > len(tables) {
		workers = len(tables)
	}

	// Table queue
	tableCh := make(chan *md.TableDef, len(tables))
	for _, t := range tables {
		tableCh <- t
	}
	close(tableCh)

	var (
		results []TableResult
		mu      sync.Mutex
		wg      sync.WaitGroup
		errCh   = make(chan error, workers)
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

				result := e.exportOneTable(ctx, tbl, primaryKeys)
				mu.Lock()
				results = append(results, result)
				mu.Unlock()

				if result.Error != nil {
					errCh <- result.Error
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errCh)

	// Check for errors
	for err := range errCh {
		if err != nil {
			return results, err
		}
	}

	return results, nil
}

func (e *Exporter) exportOneTable(ctx context.Context, tbl *md.TableDef, primaryKeys map[string][]string) TableResult {
	start := time.Now()
	key := fmt.Sprintf("%s.%s", tbl.TableSchema, tbl.TableName)
	result := TableResult{Schema: tbl.TableSchema, Table: tbl.TableName}

	e.logger.Info("Export started",
		zap.String("table", key),
		zap.Int("page_size", e.cfg.PageSize),
	)

	// Get column info from DB
	columns, err := e.getColumns(ctx, tbl)
	if err != nil {
		result.Error = fmt.Errorf("get columns: %w", err)
		return result
	}

	pkCols := primaryKeys[key]
	// Build output file
	filename := fmt.Sprintf("%s.%s.csv", strings.ToLower(tbl.TableSchema), strings.ToLower(tbl.TableName))
	filepath := filepath.Join(e.cfg.OutputDir, filename)
	f, err := os.Create(filepath)
	if err != nil {
		result.Error = fmt.Errorf("create file: %w", err)
		return result
	}
	defer f.Close()

	// Write CSV header
	if e.cfg.CSVHeader {
		header := make([]string, len(columns))
		for i, col := range columns {
			header[i] = col.Name
		}
		f.WriteString(e.csvLine(header))
	}

	// Batch read using cursor-based pagination
	var (
		totalRows int64
		batches   int
		lastVals  []any
	)

	for {
		select {
		case <-ctx.Done():
			result.Error = ctx.Err()
			return result
		default:
		}

		rows, newLast, err := e.fetchBatch(ctx, tbl, columns, pkCols, lastVals)
		if err != nil {
			result.Error = fmt.Errorf("fetch batch %d: %w", batches, err)
			return result
		}
		if len(rows) == 0 {
			break
		}

		for _, row := range rows {
			line := e.rowToCSV(row, columns)
			f.WriteString(line)
		}

		totalRows += int64(len(rows))
		batches++
		lastVals = newLast

		if len(rows) < e.cfg.PageSize {
			break
		}
	}

	result.Rows = totalRows
	result.Batches = batches
	result.Duration = time.Since(start)
	result.OutputFile = filepath

	e.logger.Info("Export completed",
		zap.String("table", key),
		zap.Int64("rows", totalRows),
		zap.Int("batches", batches),
		zap.Duration("elapsed", result.Duration),
	)

	return result
}

// ColumnInfo is lightweight column metadata from the database.
type ColumnInfo struct {
	Name     string
	TypeName string
	Nullable bool
}

func (e *Exporter) getColumns(ctx context.Context, tbl *md.TableDef) ([]ColumnInfo, error) {
	query := fmt.Sprintf("SELECT * FROM %s.%s WHERE 1=0",
		e.quoteIdent(tbl.TableSchema), e.quoteIdent(tbl.TableName))
	rows, err := e.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	colTypes, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}

	columns := make([]ColumnInfo, len(colTypes))
	for i, ct := range colTypes {
		nullable := true
		if n, ok := ct.Nullable(); ok {
			nullable = n
		}
		columns[i] = ColumnInfo{
			Name:     ct.Name(),
			TypeName: ct.DatabaseTypeName(),
			Nullable: nullable,
		}
	}
	return columns, nil
}

func (e *Exporter) isMySQL() bool {
	t := strings.ToLower(e.cfg.DBType)
	return t == "mysql" || t == "goldendb" || strings.HasSuffix(t, "-mysql")
}

func (e *Exporter) isOracle() bool {
	t := strings.ToLower(e.cfg.DBType)
	return t == "oracle" || strings.HasSuffix(t, "-oracle")
}

func (e *Exporter) isPostgres() bool {
	t := strings.ToLower(e.cfg.DBType)
	return t == "postgres" || t == "postgresql" || t == "opengaussdb" || t == "panweidb"
}

func (e *Exporter) limitClause() string {
	if e.isOracle() {
		return fmt.Sprintf("FETCH NEXT %d ROWS ONLY", e.cfg.PageSize)
	}
	return fmt.Sprintf("LIMIT %d", e.cfg.PageSize)
}

func (e *Exporter) placeholder(idx int) string {
	if e.isOracle() {
		return fmt.Sprintf(":%d", idx+1)
	}
	if e.isPostgres() {
		return fmt.Sprintf("$%d", idx+1)
	}
	return "?"
}

func (e *Exporter) fetchBatch(ctx context.Context, tbl *md.TableDef, columns []ColumnInfo, pkCols []string, lastVals []any) ([][]any, []any, error) {
	colNames := make([]string, len(columns))
	colByName := make(map[string]string, len(columns))
	for i, c := range columns {
		colNames[i] = e.quoteIdent(c.Name)
		colByName[strings.ToLower(c.Name)] = c.Name
	}

	// Resolve PK column names against actual column casing from DB
	resolvePK := func(pk string) string {
		if actual, ok := colByName[strings.ToLower(pk)]; ok {
			return actual
		}
		return pk
	}

	// Build ORDER BY clause using correctly-cased column names
	quotedPKs := make([]string, len(pkCols))
	for i, pk := range pkCols {
		quotedPKs[i] = e.quoteIdent(resolvePK(pk))
	}

	var query string
	limit := e.limitClause()
	if len(pkCols) > 0 && len(lastVals) > 0 {
		// Cursor-based
		conds := make([]string, len(pkCols))
		for i, pk := range pkCols {
			conds[i] = fmt.Sprintf("%s > %s", e.quoteIdent(resolvePK(pk)), e.placeholder(i))
		}
		query = fmt.Sprintf("SELECT %s FROM %s.%s WHERE %s ORDER BY %s %s",
			strings.Join(colNames, ", "),
			e.quoteIdent(tbl.TableSchema), e.quoteIdent(tbl.TableName),
			strings.Join(conds, " AND "),
			strings.Join(quotedPKs, ", "),
			limit,
		)
	} else if len(pkCols) > 0 {
		// First page with cursor
		query = fmt.Sprintf("SELECT %s FROM %s.%s ORDER BY %s %s",
			strings.Join(colNames, ", "),
			e.quoteIdent(tbl.TableSchema), e.quoteIdent(tbl.TableName),
			strings.Join(quotedPKs, ", "),
			limit,
		)
	} else {
		// No primary key, use limit only
		query = fmt.Sprintf("SELECT %s FROM %s.%s %s",
			strings.Join(colNames, ", "),
			e.quoteIdent(tbl.TableSchema), e.quoteIdent(tbl.TableName),
			limit,
		)
	}

	rows, err := e.db.QueryContext(ctx, query, lastVals...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var results [][]any
	for rows.Next() {
		vals := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		results = append(results, vals)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	// Get last values from the last row for cursor continuation
	var newLast []any
	if len(results) > 0 && len(pkCols) > 0 {
		lastRow := results[len(results)-1]
		newLast = make([]any, len(pkCols))
		// Find PK column positions in the column list
		for i, pkName := range pkCols {
			for j, col := range columns {
				if strings.EqualFold(col.Name, pkName) {
					newLast[i] = lastRow[j]
					break
				}
			}
		}
	}

	return results, newLast, nil
}

func (e *Exporter) rowToCSV(row []any, columns []ColumnInfo) string {
	vals := make([]string, len(row))
	for i, v := range row {
		vals[i] = e.formatCSVValue(v, columns[i])
	}
	return e.csvLine(vals)
}

func (e *Exporter) formatCSVValue(v any, col ColumnInfo) string {
	if v == nil {
		return e.cfg.CSVNullRep
	}

	var s string
	switch t := v.(type) {
	case []byte:
		if isBinaryType(col.TypeName) {
			s = hex.EncodeToString(t)
		} else {
			s = string(t)
		}
	case time.Time:
		s = t.Format("20060102150405")
	case string:
		s = t
	default:
		s = fmt.Sprintf("%v", v)
	}

	// RFC 4180: quote if contains delimiter, quote char, or newline
	needsQuote := strings.Contains(s, e.cfg.CSVDelimiter) ||
		strings.Contains(s, e.cfg.CSVQuoteChar) ||
		strings.Contains(s, "\n") ||
		strings.Contains(s, "\r")

	if needsQuote {
		q := e.cfg.CSVQuoteChar
		s = q + strings.ReplaceAll(s, q, q+q) + q
	}

	return s
}

func (e *Exporter) csvLine(vals []string) string {
	return strings.Join(vals, e.cfg.CSVDelimiter) + e.cfg.CSVLineTerminator
}

func isBinaryType(typeName string) bool {
	switch strings.ToUpper(strings.TrimSpace(typeName)) {
	case "BLOB", "BYTEA", "RAW", "BINARY", "VARBINARY":
		return true
	default:
		return false
	}
}

// QuoteStyle determines identifier quoting for different databases.
type QuoteStyle int

const (
	QuoteBacktick QuoteStyle = iota // MySQL: `name`
	QuoteDouble                     // Oracle/PG: "name"
)

// quoteIdent quotes an identifier for SQL.
func (e *Exporter) quoteIdent(name string) string {
	if e.isMySQL() {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
