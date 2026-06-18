//go:build duckdb

package duckdb

import (
	"fmt"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

type DuckDBTypeMapper struct{}

func (DuckDBTypeMapper) Name() string { return "duckdb" }

func (DuckDBTypeMapper) ToLogicalType(rawType string, length, precision, scale int) dialect.LogicalType {
	upper := strings.ToUpper(rawType)
	switch {
	case upper == "VARCHAR" || upper == "TEXT" || upper == "STRING" || upper == "CLOB":
		return dialect.LogicalType{Base: dialect.LBVarchar}
	case upper == "CHAR" || upper == "BPCHAR":
		return dialect.LogicalType{Base: dialect.LBChar}
	case upper == "TINYINT" || upper == "INT1":
		return dialect.LogicalType{Base: dialect.LBSmallInt}
	case upper == "SMALLINT" || upper == "INT2":
		return dialect.LogicalType{Base: dialect.LBSmallInt}
	case upper == "INTEGER" || upper == "INT" || upper == "INT4" || upper == "INT32":
		return dialect.LogicalType{Base: dialect.LBInt}
	case upper == "BIGINT" || upper == "INT8" || upper == "INT64":
		return dialect.LogicalType{Base: dialect.LBBigInt}
	case upper == "HUGEINT" || upper == "INT128":
		return dialect.LogicalType{Base: dialect.LBNumeric}
	case upper == "FLOAT" || upper == "REAL" || upper == "FLOAT4":
		return dialect.LogicalType{Base: dialect.LBFloat}
	case upper == "DOUBLE" || upper == "FLOAT8":
		return dialect.LogicalType{Base: dialect.LBDouble}
	case upper == "DECIMAL" || upper == "NUMERIC":
		return dialect.LogicalType{Base: dialect.LBNumeric, Precision: precision, Scale: scale}
	case upper == "BOOLEAN" || upper == "BOOL":
		return dialect.LogicalType{Base: dialect.LBBoolean}
	case upper == "DATE":
		return dialect.LogicalType{Base: dialect.LBDate}
	case upper == "TIME":
		return dialect.LogicalType{Base: dialect.LBTime}
	case strings.HasPrefix(upper, "TIMESTAMP"):
		return dialect.LogicalType{Base: dialect.LBTimestamp}
	case upper == "BLOB" || upper == "BYTEA":
		return dialect.LogicalType{Base: dialect.LBBLOB}
	case upper == "JSON":
		return dialect.LogicalType{Base: dialect.LBJSON}
	case strings.HasPrefix(upper, "INTERVAL"):
		return dialect.LogicalType{Base: dialect.LBInterval}
	default:
		return dialect.LogicalType{Base: dialect.LBVarchar}
	}
}

func (DuckDBTypeMapper) FromLogicalType(lt dialect.LogicalType) string {
	switch lt.Base {
	case dialect.LBVarchar:
		return "VARCHAR"
	case dialect.LBChar:
		return "CHAR"
	case dialect.LBSmallInt:
		return "SMALLINT"
	case dialect.LBInt:
		return "INTEGER"
	case dialect.LBBigInt:
		return "BIGINT"
	case dialect.LBFloat:
		return "FLOAT"
	case dialect.LBDouble:
		return "DOUBLE"
	case dialect.LBNumeric:
		if lt.Scale > 0 {
			return fmt.Sprintf("DECIMAL(%d,%d)", lt.Precision, lt.Scale)
		}
		return "BIGINT"
	case dialect.LBBoolean:
		return "BOOLEAN"
	case dialect.LBDate:
		return "DATE"
	case dialect.LBTime:
		return "TIME"
	case dialect.LBTimestamp, dialect.LBTimestampTZ:
		return "TIMESTAMP"
	case dialect.LBBLOB:
		return "BLOB"
	case dialect.LBJSON:
		return "JSON"
	case dialect.LBInterval:
		return "INTERVAL"
	default:
		return "VARCHAR"
	}
}

type DuckDBQuoter struct{}

func (DuckDBQuoter) Quote(name string) string     { return `"` + name + `"` }
func (DuckDBQuoter) Unquote(quoted string) string { return strings.Trim(quoted, `"`) }

type DuckDBFeatures struct{}

func (DuckDBFeatures) SupportsTransactionalDDL() bool { return true }
func (DuckDBFeatures) SupportsIfNotExists() bool      { return true }
func (DuckDBFeatures) MaxIdentifierLength() int       { return 0 }
func (DuckDBFeatures) SupportsJSONIndex() bool        { return true }
func (DuckDBFeatures) TruncateIsTransactional() bool  { return true }

type DuckDBDDLBuilder struct{}

func (DuckDBDDLBuilder) BuildCreateTable(t *md.TableDef, opts dialect.BuildOptions) (string, error) {
	schema := t.TableSchema
	if m, ok := opts.SchemaMapping[schema]; ok {
		schema = m
	}
	quote := func(name string) string {
		if opts.NoQuoteIdentifiers {
			return name
		}
		return `"` + name + `"`
	}
	var b strings.Builder
	b.WriteString("CREATE TABLE ")
	if opts.IncludeIfNotExists {
		b.WriteString("IF NOT EXISTS ")
	}
	b.WriteString(fmt.Sprintf("%s.%s (\n", quote(schema), quote(t.TableName)))
	cols := t.GetColumns()
	for i, col := range cols {
		b.WriteString("  ")
		b.WriteString(quote(col.ColumnName))
		b.WriteString(" ")
		b.WriteString(col.DataType)
		if col.Nullable == "NO" {
			b.WriteString(" NOT NULL")
		}
		if hasDef, defVal := col.HasDefault(); hasDef {
			b.WriteString(" DEFAULT " + defVal)
		}
		if i < len(cols)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString(")")
	return b.String(), nil
}

func (DuckDBDDLBuilder) BuildCreateIndex(idxs []*md.IndexDef, opts dialect.BuildOptions) (string, error) {
	if len(idxs) == 0 {
		return "", nil
	}
	first := idxs[0]
	quote := func(s string) string {
		if opts.NoQuoteIdentifiers {
			return s
		}
		return `"` + s + `"`
	}
	var b strings.Builder
	b.WriteString("CREATE ")
	if first.Uniqueness == "UNIQUE" {
		b.WriteString("UNIQUE ")
	}
	b.WriteString("INDEX ")
	b.WriteString(quote(first.IndexName))
	b.WriteString(" ON ")
	b.WriteString(quote(first.TableSchema) + "." + quote(first.TableName))
	b.WriteString(" (")
	for i, idx := range idxs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quote(idx.ColumnName))
	}
	b.WriteString(")")
	return b.String(), nil
}

func (DuckDBDDLBuilder) BuildCreateView(v *md.ViewDef, opts dialect.BuildOptions) (string, error) {
	schema := v.ViewSchema
	if m, ok := opts.SchemaMapping[schema]; ok {
		schema = m
	}
	quote := func(s string) string {
		if opts.NoQuoteIdentifiers {
			return s
		}
		return `"` + s + `"`
	}
	return fmt.Sprintf("CREATE VIEW %s.%s AS %s", quote(schema), quote(v.ViewName), v.ViewDefinition), nil
}

func (DuckDBDDLBuilder) BuildCreateSequence(seq *md.SequenceDef, opts dialect.BuildOptions) (string, error) {
	schema := seq.SequenceSchema
	if m, ok := opts.SchemaMapping[schema]; ok {
		schema = m
	}
	quote := func(s string) string {
		if opts.NoQuoteIdentifiers {
			return s
		}
		return `"` + s + `"`
	}
	cycle := "NO CYCLE"
	if strings.EqualFold(seq.Cycle, "YES") {
		cycle = "CYCLE"
	}
	return fmt.Sprintf("CREATE SEQUENCE %s.%s START WITH %d INCREMENT BY %d MINVALUE %d MAXVALUE %d %s CACHE %d",
		quote(schema), quote(seq.SequenceName),
		seq.StartValue, seq.IncrementBy, seq.MinValue, seq.MaxValue,
		cycle, seq.CacheSize), nil
}

// Unsupported DDL stubs
func (DuckDBDDLBuilder) BuildCreateTrigger(trg *md.TriggerDef, opts dialect.BuildOptions) (string, error)         { return "", nil }
func (DuckDBDDLBuilder) BuildCreateSynonym(syn *md.SynonymDef, opts dialect.BuildOptions) (string, error)         { return "", nil }
func (DuckDBDDLBuilder) BuildCreateMView(mv *md.MViewDef, opts dialect.BuildOptions) (string, error)              { return "", nil }
func (DuckDBDDLBuilder) BuildCreateFunction(fn *md.FunctionDef, opts dialect.BuildOptions) (string, error)        { return "", nil }
func (DuckDBDDLBuilder) BuildCreatePackage(pkg *md.PackageDef, opts dialect.BuildOptions) (string, error)         { return "", nil }
func (DuckDBDDLBuilder) BuildCreatePackageBody(pkg *md.PackageBodyDef, opts dialect.BuildOptions) (string, error) { return "", nil }

type DuckDBDMLHelper struct{}

func (DuckDBDMLHelper) BuildPaginationClause(pageSize, offset int) string {
	return fmt.Sprintf("LIMIT %d OFFSET %d", pageSize, offset)
}
func (DuckDBDMLHelper) BuildCursorPagination(columns []string, lastValues []any) string { return "" }
func (DuckDBDMLHelper) FormatValue(val any, colType dialect.LogicalType) string {
	return fmt.Sprintf("%v", val)
}

func New() dialect.Dialect {
	return dialect.Dialect{
		TypeMapper:       DuckDBTypeMapper{},
		IdentifierQuoter: DuckDBQuoter{},
		Features:         DuckDBFeatures{},
		DDLBuilder:       DuckDBDDLBuilder{},
		DMLHelper:        DuckDBDMLHelper{},
	}
}
