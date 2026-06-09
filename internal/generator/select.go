package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// SelectGenerator builds SELECT statements with pagination.
type SelectGenerator struct {
	batchMethod string // cursor / offset
	pageSize    int
	outputDir   string
	quoteFn     func(string) string
}

// NewSelectGenerator creates a SELECT statement generator.
func NewSelectGenerator(batchMethod string, pageSize int, outputDir string, quoteFn func(string) string) *SelectGenerator {
	return &SelectGenerator{
		batchMethod: batchMethod,
		pageSize:    pageSize,
		outputDir:   outputDir,
		quoteFn:     quoteFn,
	}
}

// Generate generates SELECT statements for all tables in the model.
// Uses cursor-based pagination if primary keys exist, otherwise offset-based.
func (sg *SelectGenerator) Generate(sm *md.SchemaModel) ([]string, error) {
	var files []string
	for _, tbl := range sm.GetTables() {
		path, err := sg.generateForTable(tbl)
		if err != nil {
			return files, err
		}
		files = append(files, path)
	}
	return files, nil
}

func (sg *SelectGenerator) generateForTable(tbl *md.TableDef) (string, error) {
	cols := tbl.GetColumns()
	if len(cols) == 0 {
		return "", fmt.Errorf("table %s.%s has no columns", tbl.TableSchema, tbl.TableName)
	}

	pks := tbl.GetPrimaryKeys()
	pagination := sg.buildPagination(tbl, pks)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("-- SELECT for %s.%s\n", tbl.TableSchema, tbl.TableName))
	b.WriteString(fmt.Sprintf("-- Batch size: %d | Method: %s\n", sg.pageSize, sg.batchMethod))

	if sg.pageSize > 0 {
		b.WriteString(fmt.Sprintf("-- Replace $PAGE_SIZE with %d\n", sg.pageSize))
		b.WriteString("-- Replace $OFFSET with (batch_number * page_size)\n")
	}

	// Build column list
	quotedCols := make([]string, 0, len(cols))
	for _, col := range cols {
		q := col.ColumnName
		if sg.quoteFn != nil {
			q = sg.quoteFn(q)
		}
		quotedCols = append(quotedCols, q)
	}

	b.WriteString(fmt.Sprintf("SELECT %s\n", strings.Join(quotedCols, ", ")))
	b.WriteString(fmt.Sprintf("FROM %s.%s", sg.quoteIdent(tbl.TableSchema), sg.quoteIdent(tbl.TableName)))

	// Add pagination WHERE clause for cursor-based
	if len(pagination) > 0 {
		b.WriteString(pagination)
	}

	b.WriteString(";\n")

	if err := os.MkdirAll(sg.outputDir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(sg.outputDir,
		fmt.Sprintf("%s.%s.select.sql", strings.ToLower(tbl.TableSchema), strings.ToLower(tbl.TableName)))
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

func (sg *SelectGenerator) buildPagination(tbl *md.TableDef, pks []*md.PrimaryKeyDef) string {
	if sg.batchMethod == "cursor" && len(pks) > 0 {
		clauses := make([]string, len(pks))
		for i, pk := range pks {
			clauses[i] = fmt.Sprintf("%s > $LAST_%s",
				sg.quoteIdent(pk.ColumnName), strings.ToUpper(pk.ColumnName))
		}
		return fmt.Sprintf("\nWHERE (%s)", strings.Join(clauses, ", "))
	}
	// Offset-based
	return fmt.Sprintf("\n-- LIMIT $PAGE_SIZE OFFSET $OFFSET")
}

func (sg *SelectGenerator) quoteIdent(name string) string {
	if sg.quoteFn != nil {
		return sg.quoteFn(name)
	}
	return name
}
