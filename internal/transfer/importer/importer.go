package importer

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
	"go.uber.org/zap"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// Config holds importer configuration.
type Config struct {
	SourceDir                    string
	CSVDelimiter                 string
	CSVNullMarker                string
	NullIf                       []string
	NullIdentifiers              []string
	NullIdentifiersCaseSensitive bool
	NullIdentifierRegex          string
	OracleEmptyStringIsNull      bool
	NumericZeroNotNull           bool
	TruncateBefore               bool
	DisableConstraints           bool
	DisableTriggers              bool
	DropIndexes                  bool
	IndexDDL                     func(tbl *md.TableDef) (drop []string, recreate []string)
	CommitInterval               int
	ErrorPolicy                  string // skip_row/stop/log_only
	MaxErrors                    int
	// UseCopy enables the PostgreSQL COPY fast path for PG-family targets.
	UseCopy                  bool
	MaxWorkers               int
	RespectForeignKeys       bool
	DateTimeFormat           string // e.g. "yyyyMMddHHmmss"
	DateTimeFormatFallback   []string
	DateTimeTruncateToTarget bool
	TrimStrings              bool
	TargetDBType             string // "postgres", "mysql", "oracle" — affects quoting and placeholders
	// PlaceholderFamily overrides dialect-derived bind placeholders:
	// "qmark" (?), "colon" (:N) or "dollar" ($N). Used for OceanBase Oracle
	// tenants reached over the MySQL wire protocol.
	PlaceholderFamily  string
	SourceEncoding     string // ""=UTF-8, "GBK", "LATIN1" — CSV file encoding
	Logger             *zap.Logger
	NoQuoteIdentifiers bool
}

// Importer reads CSV files and inserts data into a target database.
type Importer struct {
	db     *sql.DB
	cfg    Config
	logger *zap.Logger
	dec    *encoding.Decoder // source → UTF-8 (nil if no conversion needed)
	nullRe *regexp.Regexp
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
	if cfg.NullIdentifierRegex != "" {
		if re, err := regexp.Compile(cfg.NullIdentifierRegex); err == nil {
			imp.nullRe = re
		} else {
			cfg.Logger.Warn("invalid null_identifiers regex; ignoring", zap.String("regex", cfg.NullIdentifierRegex), zap.Error(err))
		}
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
	// PanWeiDB uses PG wire protocol, not MySQL
	if t == "panweidb-mysql" {
		return false
	}
	return t == "mysql" || t == "goldendb" || strings.HasSuffix(t, "-mysql")
}

// buildPlaceholders renders bind placeholders for n columns, honoring the
// optional PlaceholderFamily override.
func (imp *Importer) buildPlaceholders(n int) []string {
	p := make([]string, n)
	family := imp.cfg.PlaceholderFamily
	switch {
	case family == "qmark":
		for i := range p {
			p[i] = "?"
		}
	case family == "colon" || (family == "" && imp.isOracle()):
		for i := range p {
			p[i] = fmt.Sprintf(":%d", i+1)
		}
	case family == "dollar":
		for i := range p {
			p[i] = fmt.Sprintf("$%d", i+1)
		}
	case imp.isMySQL():
		for i := range p {
			p[i] = "?"
		}
	default:
		for i := range p {
			p[i] = fmt.Sprintf("$%d", i+1)
		}
	}
	return p
}

// isOracle returns true if the target database is Oracle or an Oracle-compatible dialect.
func (imp *Importer) isOracle() bool {
	t := strings.ToLower(imp.cfg.TargetDBType)
	// PanWeiDB uses PG wire protocol ($N placeholders), not Oracle's :N
	if t == "panweidb-oracle" {
		return false
	}
	return t == "oracle" || strings.HasSuffix(t, "-oracle")
}

// maxBindParams is the bind-parameter ceiling of the MySQL and PostgreSQL wire
// protocols; multi-row batches must stay below it.
const maxBindParams = 65535

// useMultiRowInsert reports whether multi-row INSERT ... VALUES statements are
// usable for the target: the MySQL/PG wire protocols support them, including
// OceanBase Oracle tenants reached over the MySQL wire ("?" placeholders).
// Native Oracle (":N" binds) falls back to a reused prepared statement.
func (imp *Importer) useMultiRowInsert() bool {
	return imp.cfg.PlaceholderFamily == "qmark" || !imp.isOracle()
}

// useCopyFastPath reports whether the PostgreSQL COPY path is enabled and
// applicable to the target.
func (imp *Importer) useCopyFastPath() bool {
	return imp.cfg.UseCopy && !imp.isMySQL() && !imp.isOracle() && imp.cfg.PlaceholderFamily == ""
}

// importViaCopy loads all rows with a single COPY statement. It returns an
// error (leaving the table unchanged) when COPY fails, so the caller can fall
// back to the row-level engine.
func (imp *Importer) importViaCopy(ctx context.Context, conn dbConn, schema, table string, header []string, valsRows [][]any, rowIndexes []int, result *ImportResult) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin copy tx: %w", err)
	}
	stmt, err := tx.Prepare(pq.CopyInSchema(schema, table, header...))
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("prepare copy: %w", err)
	}
	for i, vals := range valsRows {
		row := make([]any, len(vals))
		for j, v := range vals {
			// COPY text mode requires bytea as \x-hex, not raw bytes.
			if b, ok := v.([]byte); ok {
				row[j] = `\x` + hex.EncodeToString(b)
				continue
			}
			row[j] = v
		}
		if _, err := stmt.ExecContext(ctx, row...); err != nil {
			stmt.Close()
			tx.Rollback()
			return fmt.Errorf("copy row %d: %w", rowIndexes[i], err)
		}
	}
	if _, err := stmt.ExecContext(ctx); err != nil {
		stmt.Close()
		tx.Rollback()
		return fmt.Errorf("copy flush: %w", err)
	}
	if err := stmt.Close(); err != nil {
		tx.Rollback()
		return fmt.Errorf("close copy stmt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit copy: %w", err)
	}
	result.Actual = int64(len(valsRows))
	return nil
}

// statementBatchRows sizes one multi-row INSERT statement: bounded by the
// commit interval and by the wire-protocol parameter limit.
func (imp *Importer) statementBatchRows(numCols int, multiRow bool) int {
	if !multiRow {
		return 1
	}
	n := imp.cfg.CommitInterval
	if n <= 0 {
		n = 1000
	}
	if numCols > 0 {
		if byArgs := maxBindParams / numCols; byArgs >= 1 && byArgs < n {
			n = byArgs
		}
	}
	if n < 1 {
		n = 1
	}
	return n
}

// buildMultiRowInsert renders INSERT INTO ... VALUES (...),(...),... for the
// given number of rows. Numbered placeholder families ($N, :N) continue their
// sequence across rows; the ? family simply repeats.
func (imp *Importer) buildMultiRowInsert(schema, table string, quotedCols []string, numRows, numCols int) string {
	numbered := imp.numberedPlaceholders()
	valueGroups := make([]string, numRows)
	n := 0
	for i := range valueGroups {
		ph := make([]string, numCols)
		for j := range ph {
			n++
			switch {
			case numbered == "dollar":
				ph[j] = fmt.Sprintf("$%d", n)
			case numbered == "colon":
				ph[j] = fmt.Sprintf(":%d", n)
			default:
				ph[j] = "?"
			}
		}
		valueGroups[i] = "(" + strings.Join(ph, ", ") + ")"
	}
	return fmt.Sprintf("INSERT INTO %s.%s (%s) VALUES %s",
		imp.quoteIdent(schema), imp.quoteIdent(table),
		strings.Join(quotedCols, ", "),
		strings.Join(valueGroups, ", "),
	)
}

// numberedPlaceholders reports the numbered placeholder style of the target:
// "dollar" for PG-family, "colon" for Oracle-family, "" for "?".
func (imp *Importer) numberedPlaceholders() string {
	switch imp.cfg.PlaceholderFamily {
	case "dollar":
		return "dollar"
	case "colon":
		return "colon"
	case "qmark":
		return ""
	}
	if imp.isOracle() {
		return "colon"
	}
	if imp.isMySQL() {
		return ""
	}
	return "dollar"
}

// salvageChunk re-inserts a failed batch one row at a time inside savepoints so
// only the genuinely bad rows are skipped. It returns the number of inserted
// rows and whether the caller must stop.
func (imp *Importer) salvageChunk(ctx context.Context, tx *sql.Tx, insertSQL string, chunk [][]any, rowIndexes []int, handleError func(int, error) bool) (int64, bool) {
	var ok int64
	for j, vals := range chunk {
		sp := fmt.Sprintf("owl_row_%d", j)
		tx.ExecContext(ctx, "SAVEPOINT "+sp)
		if _, err := tx.ExecContext(ctx, insertSQL, vals...); err != nil {
			tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+sp)
			if handleError(rowIndexes[j], err) {
				return ok, true
			}
			continue
		}
		tx.ExecContext(ctx, "RELEASE SAVEPOINT "+sp)
		ok++
	}
	return ok, false
}

// isPlaceholderLimitError detects wire-protocol parameter-limit failures so the
// batch can be bisected instead of falling back row by row.
func isPlaceholderLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "too many placeholders") ||
		strings.Contains(msg, "parameters must be between") ||
		strings.Contains(msg, "exceeds the maximum number") ||
		strings.Contains(msg, "65535")
}

// maxErrorsReached reports whether the error ceiling has been hit.
func (imp *Importer) maxErrorsReached(result *ImportResult) bool {
	return imp.cfg.MaxErrors > 0 && result.Errors >= int64(imp.cfg.MaxErrors)
}

// convertRow maps one CSV record to typed SQL values, applying null semantics
// and data transforms.
func (imp *Importer) convertRow(tbl *md.TableDef, header []string, record []string) ([]any, error) {
	vals := make([]any, len(record))
	for j, v := range record {
		isNull := imp.isNullValue(v)
		if !isNull && imp.cfg.NumericZeroNotNull && j < len(header) && imp.isNumericColumn(tbl, header[j]) && isZeroNumeric(v) {
			isNull = true
		}
		if isNull {
			vals[j] = nil
			continue
		}
		val := imp.transformValue(v)
		if j < len(header) {
			if imp.cfg.DateTimeTruncateToTarget {
				if s, ok := val.(string); ok {
					val = truncateDatetimeToTarget(s, tbl.GetColumn(header[j]))
				}
			}
			if imp.needsNumericBoolean(tbl, header[j]) {
				val = numericBooleanValue(val)
			}
			if imp.isBinaryColumn(tbl, header[j]) {
				decoded, err := hex.DecodeString(v)
				if err != nil {
					return nil, fmt.Errorf("column %s: decode hex: %w", header[j], err)
				}
				val = decoded
			}
		}
		vals[j] = val
	}
	return vals, nil
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

func sortByForeignKeys(tables []*md.TableDef) []*md.TableDef {
	key := func(schema, table string) string {
		return strings.ToLower(schema) + "." + strings.ToLower(table)
	}
	index := make(map[string]int, len(tables))
	for i, t := range tables {
		index[key(t.TableSchema, t.TableName)] = i
	}

	n := len(tables)
	inDegree := make([]int, n)
	dependents := make([][]int, n)
	for i, t := range tables {
		for _, fk := range t.ForeignKeys {
			if j, ok := index[key(fk.RefSchema, fk.RefTable)]; ok && j != i {
				inDegree[i]++
				dependents[j] = append(dependents[j], i)
			}
		}
	}

	queue := make([]int, 0, n)
	for i := 0; i < n; i++ {
		if inDegree[i] == 0 {
			queue = append(queue, i)
		}
	}

	sorted := make([]*md.TableDef, 0, n)
	done := make([]bool, n)
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		sorted = append(sorted, tables[i])
		done[i] = true
		for _, child := range dependents[i] {
			inDegree[child]--
			if inDegree[child] == 0 {
				queue = append(queue, child)
			}
		}
	}

	for i := 0; i < n; i++ {
		if !done[i] {
			sorted = append(sorted, tables[i])
		}
	}
	return sorted
}

// ImportTables imports CSV data for multiple tables.
func (imp *Importer) ImportTables(ctx context.Context, tables []*md.TableDef, schemaMapping map[string]string) ([]ImportResult, error) {
	if imp.cfg.RespectForeignKeys {
		tables = sortByForeignKeys(tables)
	}
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

// dbConn abstracts operations available on both *sql.DB and *sql.Conn.
type dbConn interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

func (imp *Importer) guardStatements(ctx context.Context, conn dbConn, schema, table string) (disable []string, enable []string) {
	full := imp.quoteIdent(schema) + "." + imp.quoteIdent(table)
	switch {
	case imp.isMySQL():
		if imp.cfg.DisableConstraints {
			disable = append(disable, "SET FOREIGN_KEY_CHECKS=0")
			enable = append(enable, "SET FOREIGN_KEY_CHECKS=1")
		}
		if imp.cfg.DisableTriggers {
			imp.logger.Warn("disable_triggers is not supported on MySQL; skipping", zap.String("table", full))
		}
	case imp.isOracle():
		if imp.cfg.DisableTriggers {
			disable = append(disable, fmt.Sprintf("ALTER TABLE %s DISABLE ALL TRIGGERS", full))
			enable = append(enable, fmt.Sprintf("ALTER TABLE %s ENABLE ALL TRIGGERS", full))
		}
		if imp.cfg.DisableConstraints {
			for _, name := range imp.oracleFKConstraints(ctx, conn, schema, table) {
				quoted := imp.quoteIdent(name)
				disable = append(disable, fmt.Sprintf("ALTER TABLE %s DISABLE CONSTRAINT %s", full, quoted))
				enable = append(enable, fmt.Sprintf("ALTER TABLE %s ENABLE CONSTRAINT %s", full, quoted))
			}
		}
	default:
		if imp.cfg.DisableConstraints || imp.cfg.DisableTriggers {
			disable = append(disable, fmt.Sprintf("ALTER TABLE %s DISABLE TRIGGER ALL", full))
			enable = append(enable, fmt.Sprintf("ALTER TABLE %s ENABLE TRIGGER ALL", full))
		}
	}
	return disable, enable
}

func (imp *Importer) oracleFKConstraints(ctx context.Context, conn dbConn, schema, table string) []string {
	rows, err := conn.QueryContext(ctx,
		"SELECT constraint_name FROM all_constraints WHERE owner = UPPER(:1) AND table_name = UPPER(:2) AND constraint_type = 'R'",
		schema, table)
	if err != nil {
		imp.logger.Warn("query foreign key constraints failed", zap.Error(err))
		return nil
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			imp.logger.Warn("scan foreign key constraint row", zap.Error(err))
			continue
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		imp.logger.Warn("iterate foreign key constraints", zap.Error(err))
	}
	return names
}

func (imp *Importer) importOneTable(ctx context.Context, tbl *md.TableDef, targetSchema string) ImportResult {
	start := time.Now()
	key := fmt.Sprintf("%s.%s", tbl.TableSchema, tbl.TableName)
	result := ImportResult{Schema: targetSchema, Table: tbl.TableName}

	imp.logger.Info("Import started",
		zap.String("table", key),
		zap.String("target", fmt.Sprintf("%s.%s", targetSchema, tbl.TableName)),
	)

	var conn dbConn = imp.db
	if imp.isOracle() || (imp.isMySQL() && imp.cfg.DisableConstraints) {
		sqlConn, err := imp.db.Conn(ctx)
		if err != nil {
			result.Err = fmt.Errorf("acquire dedicated conn: %w", err)
			return result
		}
		defer sqlConn.Close()
		conn = sqlConn
	}

	if imp.isOracle() {
		if _, err := conn.ExecContext(ctx, "ALTER SESSION SET NLS_DATE_FORMAT = 'YYYY-MM-DD HH24:MI:SS'"); err != nil {
			imp.logger.Warn("Failed to set NLS_DATE_FORMAT", zap.Error(err))
		}
		if _, err := conn.ExecContext(ctx, "ALTER SESSION SET NLS_TIMESTAMP_FORMAT = 'YYYY-MM-DD HH24:MI:SS'"); err != nil {
			imp.logger.Warn("Failed to set NLS_TIMESTAMP_FORMAT", zap.Error(err))
		}
		if _, err := conn.ExecContext(ctx, "ALTER SESSION SET NLS_TIMESTAMP_TZ_FORMAT = 'YYYY-MM-DD HH24:MI:SS TZH:TZM'"); err != nil {
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
	placeholders := imp.buildPlaceholders(len(header))

	insertSQL := fmt.Sprintf("INSERT INTO %s.%s (%s) VALUES (%s)",
		imp.quoteIdent(targetSchema), imp.quoteIdent(tbl.TableName),
		strings.Join(quotedCols, ", "),
		strings.Join(placeholders, ", "),
	)

	// Truncate if configured
	if imp.cfg.TruncateBefore {
		truncSQL := fmt.Sprintf("TRUNCATE TABLE %s.%s",
			imp.quoteIdent(targetSchema), imp.quoteIdent(tbl.TableName))
		if _, err := conn.ExecContext(ctx, truncSQL); err != nil {
			imp.logger.Warn("TRUNCATE failed (table may not exist yet)", zap.Error(err))
		}
	}

	if imp.cfg.DisableConstraints || imp.cfg.DisableTriggers {
		disableStmts, enableStmts := imp.guardStatements(ctx, conn, targetSchema, tbl.TableName)
		for _, stmt := range disableStmts {
			if _, err := conn.ExecContext(ctx, stmt); err != nil {
				imp.logger.Warn("disable guard failed", zap.String("sql", stmt), zap.Error(err))
			}
		}
		enableCtx := context.WithoutCancel(ctx)
		defer func() {
			for _, stmt := range enableStmts {
				if _, err := conn.ExecContext(enableCtx, stmt); err != nil {
					imp.logger.Warn("enable guard failed", zap.String("sql", stmt), zap.Error(err))
				}
			}
		}()
	}

	if imp.cfg.DropIndexes && imp.cfg.IndexDDL != nil {
		dropStmts, recreateStmts := imp.cfg.IndexDDL(tbl)
		for _, stmt := range dropStmts {
			if _, err := conn.ExecContext(ctx, stmt); err != nil {
				imp.logger.Warn("drop index failed", zap.String("sql", stmt), zap.Error(err))
			}
		}
		recreateCtx := context.WithoutCancel(ctx)
		defer func() {
			for _, stmt := range recreateStmts {
				if _, err := conn.ExecContext(recreateCtx, stmt); err != nil {
					imp.logger.Warn("recreate index failed", zap.String("sql", stmt), zap.Error(err))
				}
			}
		}()
	}

	// Convert all CSV values up front so the insert loop only deals with
	// batching and execution.
	valsRows := make([][]any, 0, len(allRows))
	rowIndexes := make([]int, 0, len(allRows))
	for i, row := range allRows {
		vals, err := imp.convertRow(tbl, header, row)
		if err != nil {
			result.Errors++
			if imp.cfg.ErrorPolicy == "stop" {
				result.Err = fmt.Errorf("row %d: %w", i, err)
				return result
			}
			result.Skipped++
			imp.logger.Warn("Skipping row (value conversion)", zap.Int("row", i), zap.Error(err))
			if imp.maxErrorsReached(&result) {
				result.Err = fmt.Errorf("max errors (%d) reached", imp.cfg.MaxErrors)
				return result
			}
			continue
		}
		valsRows = append(valsRows, vals)
		rowIndexes = append(rowIndexes, i)
	}

	// PostgreSQL COPY fast path (optional). COPY is all-or-nothing, so on any
	// failure we fall back to the batched INSERT engine which supports the
	// row-level error policies.
	if imp.useCopyFastPath() && len(valsRows) > 0 {
		if err := imp.importViaCopy(ctx, conn, targetSchema, tbl.TableName, header, valsRows, rowIndexes, &result); err != nil {
			imp.logger.Warn("COPY fast path failed; falling back to batched INSERT",
				zap.String("table", key), zap.Error(err))
		} else {
			result.Duration = time.Since(start)
			imp.logger.Info("Import completed (COPY)",
				zap.String("table", key),
				zap.Int64("expected", result.Expected),
				zap.Int64("actual", result.Actual),
				zap.Duration("elapsed", result.Duration),
			)
			return result
		}
	}

	// Insert in batches with transaction control
	var (
		skipped     = &result.Skipped
		errCount    = &result.Errors
		inserted    int64
		pendingInTx int64
		tx          *sql.Tx
	)

	beginTx := func() error {
		var e error
		tx, e = conn.BeginTx(ctx, nil)
		pendingInTx = 0
		return e
	}
	commitTx := func() error {
		if tx == nil {
			return nil
		}
		e := tx.Commit()
		tx = nil
		return e
	}
	rollbackTx := func() {
		if tx != nil {
			tx.Rollback()
			tx = nil
			inserted -= pendingInTx
			pendingInTx = 0
		}
	}

	if err := beginTx(); err != nil {
		result.Err = fmt.Errorf("begin transaction: %w", err)
		return result
	}

	handleRowError := func(rowIndex int, execErr error) (stop bool) {
		*errCount++
		switch imp.cfg.ErrorPolicy {
		case "stop":
			rollbackTx()
			result.Err = fmt.Errorf("row %d: %w", rowIndex, execErr)
			return true
		case "skip_row":
			*skipped++
			imp.logger.Warn("Skipping row", zap.Int("row", rowIndex), zap.Error(execErr))
			return imp.maxErrorsReached(&result)
		default: // log_only
			imp.logger.Warn("Row error (continuing)", zap.Int("row", rowIndex), zap.Error(execErr))
			return false
		}
	}

	useMultiRow := imp.useMultiRowInsert()
	chunkSize := imp.statementBatchRows(len(header), useMultiRow)
	if chunkSize <= 0 {
		chunkSize = 1
	}

	var stmt *sql.Stmt
	if !useMultiRow {
		var perr error
		stmt, perr = tx.PrepareContext(ctx, insertSQL)
		if perr != nil {
			rollbackTx()
			result.Err = fmt.Errorf("prepare insert: %w", perr)
			return result
		}
	}

	maxErrorsStop := false
	for pos := 0; pos < len(valsRows); {
		select {
		case <-ctx.Done():
			if stmt != nil {
				stmt.Close()
			}
			rollbackTx()
			result.Err = ctx.Err()
			return result
		default:
		}

		if useMultiRow {
			end := pos + chunkSize
			if end > len(valsRows) {
				end = len(valsRows)
			}
			chunk := valsRows[pos:end]
			batchSQL := imp.buildMultiRowInsert(targetSchema, tbl.TableName, quotedCols, len(chunk), len(header))
			args := make([]any, 0, len(chunk)*len(header))
			for _, vals := range chunk {
				args = append(args, vals...)
			}

			// The savepoint keeps earlier rows of this transaction intact when
			// the batch fails (required on PostgreSQL, where any statement
			// error aborts the whole transaction).
			tx.ExecContext(ctx, "SAVEPOINT owl_batch")
			_, execErr := tx.ExecContext(ctx, batchSQL, args...)
			if execErr == nil {
				tx.ExecContext(ctx, "RELEASE SAVEPOINT owl_batch")
				inserted += int64(len(chunk))
				pendingInTx += int64(len(chunk))
				pos = end
			} else {
				tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT owl_batch")
				if isPlaceholderLimitError(execErr) && len(chunk) > 1 {
					chunkSize = len(chunk) / 2
					continue
				}
				if imp.cfg.ErrorPolicy == "stop" {
					*errCount++
					rollbackTx()
					result.Err = fmt.Errorf("row %d: %w", rowIndexes[pos], execErr)
					result.Actual = inserted
					return result
				}
				// Salvage the chunk row by row so only the genuinely bad rows
				// are skipped.
				ok, stop := imp.salvageChunk(ctx, tx, insertSQL, chunk, rowIndexes[pos:end], handleRowError)
				inserted += ok
				pendingInTx += ok
				pos = end
				if stop {
					rollbackTx()
					if result.Err == nil && imp.cfg.MaxErrors > 0 && *errCount >= int64(imp.cfg.MaxErrors) {
						result.Err = fmt.Errorf("max errors (%d) reached", imp.cfg.MaxErrors)
					}
					result.Actual = inserted
					return result
				}
			}
		} else {
			vals := valsRows[pos]
			_, execErr := stmt.ExecContext(ctx, vals...)
			if execErr != nil {
				if handleRowError(rowIndexes[pos], execErr) {
					maxErrorsStop = true
					if result.Err == nil && imp.cfg.MaxErrors > 0 && *errCount >= int64(imp.cfg.MaxErrors) {
						result.Err = fmt.Errorf("max errors (%d) reached", imp.cfg.MaxErrors)
					}
					break
				}
			} else {
				inserted++
				pendingInTx++
			}
			pos++
		}

		if pendingInTx >= int64(imp.cfg.CommitInterval) {
			if stmt != nil {
				stmt.Close()
				stmt = nil
			}
			if err := commitTx(); err != nil {
				result.Err = fmt.Errorf("commit at row %d: %w", inserted, err)
				result.Actual = inserted
				return result
			}
			imp.logger.Debug("Committed", zap.Int64("rows", inserted), zap.String("table", key))
			if err := beginTx(); err != nil {
				result.Err = fmt.Errorf("begin tx after commit: %w", err)
				result.Actual = inserted
				return result
			}
			if !useMultiRow {
				var perr error
				stmt, perr = tx.PrepareContext(ctx, insertSQL)
				if perr != nil {
					rollbackTx()
					result.Err = fmt.Errorf("re-prepare insert: %w", perr)
					result.Actual = inserted
					return result
				}
			}
		}
	}
	if maxErrorsStop {
		if stmt != nil {
			stmt.Close()
		}
		result.Actual = inserted
		result.Duration = time.Since(start)
		return result
	}

	if stmt != nil {
		stmt.Close()
	}
	// Final commit
	if err := commitTx(); err != nil {
		result.Err = fmt.Errorf("final commit: %w", err)
		return result
	}

	result.Actual = inserted
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

func (imp *Importer) isNullValue(v string) bool {
	if v == imp.cfg.CSVNullMarker {
		return true
	}
	for _, n := range imp.cfg.NullIf {
		if v == n {
			return true
		}
	}
	for _, n := range imp.cfg.NullIdentifiers {
		if imp.cfg.NullIdentifiersCaseSensitive {
			if v == n {
				return true
			}
		} else if strings.EqualFold(v, n) {
			return true
		}
	}
	if imp.nullRe != nil && imp.nullRe.MatchString(v) {
		return true
	}
	if imp.cfg.OracleEmptyStringIsNull && imp.isOracle() && v == "" {
		return true
	}
	return false
}

func (imp *Importer) isNumericColumn(tbl *md.TableDef, columnName string) bool {
	for _, col := range tbl.GetColumns() {
		if strings.EqualFold(col.ColumnName, columnName) {
			switch strings.ToUpper(strings.TrimSpace(col.DataType)) {
			case "NUMBER", "NUMERIC", "DECIMAL", "DEC", "INT", "INTEGER", "BIGINT", "SMALLINT", "TINYINT", "MEDIUMINT", "FLOAT", "DOUBLE", "REAL":
				return true
			}
		}
	}
	return false
}

func isZeroNumeric(v string) bool {
	t := strings.TrimSpace(v)
	return t == "0" || t == "0.0"
}

func truncateDatetimeToTarget(v string, col *md.ColumnDef) string {
	if col == nil {
		return v
	}
	dt := strings.ToUpper(strings.TrimSpace(col.DataType))
	if dt == "DATE" && len(v) >= 19 && v[10] == ' ' {
		return v[:10]
	}
	if strings.HasPrefix(dt, "TIMESTAMP") && col.DataScale == 0 && len(v) >= 19 {
		if dot := strings.IndexByte(v, '.'); dot >= 19 {
			return v[:dot]
		}
	}
	return v
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

	if imp.cfg.DateTimeFormat != "" {
		if converted, ok := convertCompactDatetime(imp.cfg.DateTimeFormat, s); ok {
			return converted
		}
	}
	for _, fallback := range imp.cfg.DateTimeFormatFallback {
		if converted, ok := convertCompactDatetime(fallback, s); ok {
			return converted
		}
	}

	return s
}

func convertCompactDatetime(format, s string) (string, bool) {
	switch format {
	case "yyyyMMddHHmmss":
		if len(s) == 14 && isAllDigits(s) {
			return fmt.Sprintf("%s-%s-%s %s:%s:%s", s[0:4], s[4:6], s[6:8], s[8:10], s[10:12], s[12:14]), true
		}
	case "yyyyMMdd":
		if len(s) == 8 && isAllDigits(s) {
			return fmt.Sprintf("%s-%s-%s", s[0:4], s[4:6], s[6:8]), true
		}
	}
	return "", false
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
	if imp.cfg.NoQuoteIdentifiers {
		return name
	}
	if imp.isMySQL() {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` // PostgreSQL/Oracle-style
}
