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
	batchMethod      string // cursor / offset
	pageSize         int
	outputDir        string
	quoteFn          func(string) string
	includeRowNumber bool
	addExportColumns bool
	oracleRowNum     bool
	paginationFn     func(pageSize, offset int) string
}

// NewSelectGenerator creates a SELECT statement generator.
func NewSelectGenerator(batchMethod string, pageSize int, outputDir string, quoteFn func(string) string, includeRowNumber, addExportColumns, oracleRowNum bool) *SelectGenerator {
	return &SelectGenerator{
		batchMethod:      batchMethod,
		pageSize:         pageSize,
		outputDir:        outputDir,
		quoteFn:          quoteFn,
		includeRowNumber: includeRowNumber,
		addExportColumns: addExportColumns,
		oracleRowNum:     oracleRowNum,
	}
}

// WithPagination sets a dialect-specific pagination clause builder used for
// offset-based SELECT generation.
func (sg *SelectGenerator) WithPagination(fn func(pageSize, offset int) string) *SelectGenerator {
	sg.paginationFn = fn
	return sg
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
		if sg.batchMethod == "cursor" && len(pks) > 0 {
			b.WriteString("-- Substitute $LAST_<COL> with the last row's key values from the previous batch\n")
		} else {
			b.WriteString(fmt.Sprintf("-- Advance pages by increasing OFFSET in steps of %d\n", sg.pageSize))
		}
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
	if sg.includeRowNumber {
		if sg.oracleRowNum {
			quotedCols = append(quotedCols, "ROWNUM AS rn")
		} else {
			quotedCols = append(quotedCols, fmt.Sprintf("ROW_NUMBER() OVER (ORDER BY %s) AS rn", strings.Join(sg.orderCols(tbl, pks), ", ")))
		}
	}
	if sg.addExportColumns {
		quotedCols = append(quotedCols, fmt.Sprintf("'%s.%s' AS __export_source", tbl.TableSchema, tbl.TableName))
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
		quoted := make([]string, len(pks))
		placeholders := make([]string, len(pks))
		for i, pk := range pks {
			quoted[i] = sg.quoteIdent(pk.ColumnName)
			placeholders[i] = fmt.Sprintf("$LAST_%s", strings.ToUpper(pk.ColumnName))
		}
		var where string
		if len(pks) == 1 {
			where = fmt.Sprintf("%s > %s", quoted[0], placeholders[0])
		} else {
			where = fmt.Sprintf("(%s) > (%s)", strings.Join(quoted, ", "), strings.Join(placeholders, ", "))
		}
		return fmt.Sprintf("\nWHERE %s\nORDER BY %s", where, strings.Join(quoted, ", "))
	}

	var sb strings.Builder
	sb.WriteString("\n")
	if len(pks) > 0 {
		quoted := make([]string, len(pks))
		for i, pk := range pks {
			quoted[i] = sg.quoteIdent(pk.ColumnName)
		}
		sb.WriteString("ORDER BY ")
		sb.WriteString(strings.Join(quoted, ", "))
		sb.WriteString("\n")
	}
	if sg.paginationFn != nil {
		sb.WriteString(sg.paginationFn(sg.pageSize, 0))
	} else {
		sb.WriteString(fmt.Sprintf("LIMIT %d OFFSET 0", sg.pageSize))
	}
	return sb.String()
}

func (sg *SelectGenerator) quoteIdent(name string) string {
	if sg.quoteFn != nil {
		return sg.quoteFn(name)
	}
	return name
}

func (sg *SelectGenerator) orderCols(tbl *md.TableDef, pks []*md.PrimaryKeyDef) []string {
	if len(pks) > 0 {
		cols := make([]string, len(pks))
		for i, pk := range pks {
			cols[i] = sg.quoteIdent(pk.ColumnName)
		}
		return cols
	}
	if cols := tbl.GetColumns(); len(cols) > 0 {
		return []string{sg.quoteIdent(cols[0].ColumnName)}
	}
	return nil
}
