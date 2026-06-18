package sqlite3

import (
	"fmt"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// ── TypeMapper ──

type SQLite3TypeMapper struct{}

func (SQLite3TypeMapper) Name() string { return "sqlite3" }

func (SQLite3TypeMapper) ToLogicalType(rawType string, length, precision, scale int) dialect.LogicalType {
	upper := strings.ToUpper(rawType)
	switch {
	case upper == "TEXT" || upper == "VARCHAR" || upper == "CHARACTER VARYING" || upper == "CLOB":
		return dialect.LogicalType{Base: dialect.LBVarchar}
	case upper == "INTEGER" || upper == "INT" || upper == "BIGINT" || upper == "SMALLINT" || upper == "TINYINT":
		return dialect.LogicalType{Base: dialect.LBInt}
	case upper == "REAL" || upper == "FLOAT" || upper == "DOUBLE":
		return dialect.LogicalType{Base: dialect.LBFloat}
	case upper == "NUMERIC" || upper == "DECIMAL":
		return dialect.LogicalType{Base: dialect.LBNumeric, Precision: precision, Scale: scale}
	case upper == "BLOB":
		return dialect.LogicalType{Base: dialect.LBBLOB}
	case upper == "BOOLEAN":
		return dialect.LogicalType{Base: dialect.LBBoolean}
	case upper == "DATE":
		return dialect.LogicalType{Base: dialect.LBDate}
	case upper == "DATETIME" || upper == "TIMESTAMP":
		return dialect.LogicalType{Base: dialect.LBTimestamp}
	default:
		return dialect.LogicalType{Base: dialect.LBVarchar}
	}
}

func (SQLite3TypeMapper) FromLogicalType(lt dialect.LogicalType) string {
	switch lt.Base {
	case dialect.LBVarchar:
		return "TEXT"
	case dialect.LBChar:
		return "TEXT"
	case dialect.LBInt, dialect.LBBigInt, dialect.LBSmallInt:
		return "INTEGER"
	case dialect.LBFloat, dialect.LBDouble:
		return "REAL"
	case dialect.LBNumeric:
		if lt.Scale > 0 {
			return "REAL"
		}
		return "INTEGER"
	case dialect.LBBLOB:
		return "BLOB"
	case dialect.LBBoolean:
		return "INTEGER"
	case dialect.LBDate, dialect.LBDatetime, dialect.LBTimestamp, dialect.LBTimestampTZ:
		return "TEXT"
	case dialect.LBJSON:
		return "TEXT"
	default:
		return "TEXT"
	}
}

// ── IdentifierQuoter ──

type SQLite3Quoter struct{}

func (SQLite3Quoter) Quote(name string) string     { return `"` + name + `"` }
func (SQLite3Quoter) Unquote(quoted string) string { return strings.Trim(quoted, `"`) }

// ── Features ──

type SQLite3Features struct{}

func (SQLite3Features) SupportsTransactionalDDL() bool { return true }
func (SQLite3Features) SupportsIfNotExists() bool      { return true }
func (SQLite3Features) MaxIdentifierLength() int       { return 0 } // no hard limit
func (SQLite3Features) SupportsJSONIndex() bool        { return false }
func (SQLite3Features) TruncateIsTransactional() bool  { return true }

// ── DDLBuilder ──

type SQLite3DDLBuilder struct{}

func (SQLite3DDLBuilder) BuildCreateTable(t *md.TableDef, opts dialect.BuildOptions) (string, error) {
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

func (SQLite3DDLBuilder) BuildCreateIndex(idxs []*md.IndexDef, opts dialect.BuildOptions) (string, error) {
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

func (SQLite3DDLBuilder) BuildCreateView(v *md.ViewDef, opts dialect.BuildOptions) (string, error) {
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

func (SQLite3DDLBuilder) BuildCreateTrigger(trg *md.TriggerDef, opts dialect.BuildOptions) (string, error) {
	schema := trg.TableSchema
	if m, ok := opts.SchemaMapping[schema]; ok {
		schema = m
	}
	quote := func(s string) string {
		if opts.NoQuoteIdentifiers {
			return s
		}
		return `"` + s + `"`
	}
	forEach := trg.ForEach
	if forEach == "" {
		forEach = "ROW"
	}
	return fmt.Sprintf("CREATE TRIGGER %s %s %s ON %s.%s FOR EACH %s %s",
		quote(trg.TriggerName), trg.TriggerType, trg.TriggerEvent,
		quote(schema), quote(trg.TableName), forEach, trg.TriggerBody), nil
}

// Unsupported DDL stubs
func (SQLite3DDLBuilder) BuildCreateSequence(seq *md.SequenceDef, opts dialect.BuildOptions) (string, error)       { return "", nil }
func (SQLite3DDLBuilder) BuildCreateSynonym(syn *md.SynonymDef, opts dialect.BuildOptions) (string, error)         { return "", nil }
func (SQLite3DDLBuilder) BuildCreateMView(mv *md.MViewDef, opts dialect.BuildOptions) (string, error)              { return "", nil }
func (SQLite3DDLBuilder) BuildCreateFunction(fn *md.FunctionDef, opts dialect.BuildOptions) (string, error)        { return "", nil }
func (SQLite3DDLBuilder) BuildCreatePackage(pkg *md.PackageDef, opts dialect.BuildOptions) (string, error)         { return "", nil }
func (SQLite3DDLBuilder) BuildCreatePackageBody(pkg *md.PackageBodyDef, opts dialect.BuildOptions) (string, error) { return "", nil }

// ── DMLHelper ──

type SQLite3DMLHelper struct{}

func (SQLite3DMLHelper) BuildPaginationClause(pageSize, offset int) string {
	return fmt.Sprintf("LIMIT %d OFFSET %d", pageSize, offset)
}

func (SQLite3DMLHelper) BuildCursorPagination(columns []string, lastValues []any) string { return "" }

func (SQLite3DMLHelper) FormatValue(val any, colType dialect.LogicalType) string {
	return fmt.Sprintf("%v", val)
}

// ── Dialect ──

func New() dialect.Dialect {
	return dialect.Dialect{
		TypeMapper:       SQLite3TypeMapper{},
		IdentifierQuoter: SQLite3Quoter{},
		Features:         SQLite3Features{},
		DDLBuilder:       SQLite3DDLBuilder{},
		DMLHelper:        SQLite3DMLHelper{},
	}
}
