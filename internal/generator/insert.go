package generator

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// InsertConfig controls INSERT SQL generation.
type InsertConfig struct {
	OutputDir      string
	BatchSize      int // VALUES rows per INSERT
	TruncateBefore bool
	Dialect        string // oracle/postgres/mysql
	NullMarker     string
	CSVDelimiter   string
	NoQuoteIdentifiers bool
}

// InsertGenerator reads CSV data files and generates INSERT SQL statements.
type InsertGenerator struct {
	cfg InsertConfig
}

// NewInsertGenerator creates an INSERT SQL generator.
func NewInsertGenerator(cfg InsertConfig) *InsertGenerator {
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 100
	}
	if cfg.NullMarker == "" {
		cfg.NullMarker = "\\N"
	}
	if cfg.CSVDelimiter == "" {
		cfg.CSVDelimiter = ","
	}
	return &InsertGenerator{cfg: cfg}
}

// Generate reads CSV files and writes INSERT SQL for each table.
func (g *InsertGenerator) Generate(tables []*md.TableDef, sourceDir string) ([]string, error) {
	os.MkdirAll(g.cfg.OutputDir, 0755)
	var files []string

	for _, tbl := range tables {
		path, err := g.generateForTable(tbl, sourceDir)
		if err != nil {
			return files, err
		}
		if path != "" {
			files = append(files, path)
		}
	}
	return files, nil
}

func (g *InsertGenerator) generateForTable(tbl *md.TableDef, sourceDir string) (string, error) {
	// Read CSV data
	filename := fmt.Sprintf("%s.%s.csv", strings.ToLower(tbl.TableSchema), strings.ToLower(tbl.TableName))
	srcPath := filepath.Join(sourceDir, filename)
	f, err := os.Open(srcPath)
	if err != nil {
		return "", fmt.Errorf("open CSV %s: %w", srcPath, err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	// Read header
	header, err := reader.Read()
	if err != nil {
		return "", fmt.Errorf("read header from %s: %w", srcPath, err)
	}

	// Read all rows
	var allRows [][]string
	for {
		record, err := reader.Read()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return "", err
		}
		allRows = append(allRows, record)
	}

	if len(allRows) == 0 {
		return "", nil
	}

	// Generate INSERT SQL
	q := g.getQuoter()
	outFilename := fmt.Sprintf("%s.%s.insert.sql", strings.ToLower(tbl.TableSchema), strings.ToLower(tbl.TableName))
	outPath := filepath.Join(g.cfg.OutputDir, outFilename)
	out, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	defer out.Close()

	write := func(s string) { out.WriteString(s + "\n") }

	// Optional truncate
	if g.cfg.TruncateBefore {
		write(fmt.Sprintf("TRUNCATE TABLE %s.%s;", q(tbl.TableSchema), q(tbl.TableName)))
		write("")
	}

	// BEGIN transaction if PG/Oracle
	if g.cfg.Dialect == "postgres" || g.cfg.Dialect == "oracle" {
		write("BEGIN;")
		write("")
	}

	// Column list
	quotedCols := make([]string, len(header))
	for i, h := range header {
		quotedCols[i] = q(h)
	}
	colList := strings.Join(quotedCols, ", ")

	// Batch INSERTs
	totalRows := len(allRows)
	for i := 0; i < totalRows; i += g.cfg.BatchSize {
		end := i + g.cfg.BatchSize
		if end > totalRows {
			end = totalRows
		}
		batch := allRows[i:end]

		write(fmt.Sprintf("INSERT INTO %s.%s (%s)",
			q(tbl.TableSchema), q(tbl.TableName), colList))
		write("VALUES")

		for j, row := range batch {
			vals := make([]string, len(row))
			for k, v := range row {
				vals[k] = g.formatSQLValue(v)
			}
			comma := ","
			if j == len(batch)-1 {
				comma = ";"
			}
			write(fmt.Sprintf("  (%s)%s", strings.Join(vals, ", "), comma))
		}
		write("")

		// COMMIT every batch if in transaction mode
		if g.cfg.Dialect != "mysql" && end < totalRows {
			write("COMMIT;")
			write("BEGIN;")
			write("")
		}
	}

	// Final COMMIT
	if g.cfg.Dialect == "postgres" || g.cfg.Dialect == "oracle" {
		write("COMMIT;")
	}

	return outPath, nil
}

func (g *InsertGenerator) formatSQLValue(v string) string {
	return FormatSQLValue(v, g.cfg.NullMarker, g.cfg.Dialect)
}

func (g *InsertGenerator) getQuoter() func(string) string {
	return GetQuoter(g.cfg.Dialect, g.cfg.NoQuoteIdentifiers)
}
