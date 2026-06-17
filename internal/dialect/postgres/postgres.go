package postgres

import (
	"fmt"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

type PGTypeMapper struct{}

func (PGTypeMapper) Name() string { return "postgres" }

func (PGTypeMapper) ToLogicalType(rawType string, length, precision, scale int) dialect.LogicalType {
	upper := strings.ToUpper(rawType)
	switch {
	case upper == "VARCHAR" || upper == "CHARACTER VARYING":
		return dialect.LogicalType{Base: dialect.LBVarchar, Length: length}
	case upper == "CHAR" || upper == "CHARACTER":
		return dialect.LogicalType{Base: dialect.LBChar, Length: length}
	case upper == "TEXT":
		return dialect.LogicalType{Base: dialect.LBCLOB}
	case upper == "SMALLINT" || upper == "INT2":
		return dialect.LogicalType{Base: dialect.LBSmallInt}
	case upper == "INTEGER" || upper == "INT" || upper == "INT4":
		return dialect.LogicalType{Base: dialect.LBInt}
	case upper == "BIGINT" || upper == "INT8":
		return dialect.LogicalType{Base: dialect.LBBigInt}
	case upper == "REAL" || upper == "FLOAT4":
		return dialect.LogicalType{Base: dialect.LBFloat}
	case upper == "DOUBLE PRECISION" || upper == "FLOAT8":
		return dialect.LogicalType{Base: dialect.LBDouble}
	case upper == "NUMERIC" || upper == "DECIMAL":
		return dialect.LogicalType{Base: dialect.LBNumeric, Precision: precision, Scale: scale}
	case upper == "BOOLEAN" || upper == "BOOL":
		return dialect.LogicalType{Base: dialect.LBBoolean}
	case upper == "DATE":
		return dialect.LogicalType{Base: dialect.LBDate}
	case upper == "TIME":
		return dialect.LogicalType{Base: dialect.LBTime}
	case upper == "TIMESTAMP":
		return dialect.LogicalType{Base: dialect.LBTimestamp}
	case upper == "TIMESTAMPTZ" || upper == "TIMESTAMP WITH TIME ZONE":
		return dialect.LogicalType{Base: dialect.LBTimestampTZ}
	case upper == "INTERVAL":
		return dialect.LogicalType{Base: dialect.LBInterval}
	case upper == "BYTEA":
		return dialect.LogicalType{Base: dialect.LBBLOB}
	case upper == "JSON" || upper == "JSONB":
		return dialect.LogicalType{Base: dialect.LBJSON}
	case upper == "XML":
		return dialect.LogicalType{Base: dialect.LBXML}
	case upper == "UUID":
		return dialect.LogicalType{Base: dialect.LBVarchar, Length: 36}
	case upper == "INET" || upper == "CIDR" || upper == "MACADDR":
		return dialect.LogicalType{Base: dialect.LBVarchar, Length: length}
	case strings.HasPrefix(upper, "SERIAL"):
		return dialect.LogicalType{Base: dialect.LBBigInt}
	default:
		return dialect.LogicalType{Base: dialect.LBVarchar, Length: length}
	}
}

func (PGTypeMapper) FromLogicalType(lt dialect.LogicalType) string {
	switch lt.Base {
	case dialect.LBVarchar:
		if lt.Length > 0 {
			return fmt.Sprintf("VARCHAR(%d)", lt.Length)
		}
		return "VARCHAR"
	case dialect.LBChar:
		return fmt.Sprintf("CHAR(%d)", lt.Length)
	case dialect.LBSmallInt:
		return "SMALLINT"
	case dialect.LBInt:
		return "INTEGER"
	case dialect.LBBigInt:
		return "BIGINT"
	case dialect.LBNumeric:
		if lt.Scale > 0 {
			return fmt.Sprintf("NUMERIC(%d,%d)", lt.Precision, lt.Scale)
		}
		if lt.Precision > 0 {
			return fmt.Sprintf("NUMERIC(%d)", lt.Precision)
		}
		return "NUMERIC"
	case dialect.LBFloat:
		return "REAL"
	case dialect.LBDouble:
		return "DOUBLE PRECISION"
	case dialect.LBDate:
		return "DATE"
	case dialect.LBTime:
		return "TIME"
	case dialect.LBDatetime:
		return "TIMESTAMP"
	case dialect.LBTimestamp:
		return "TIMESTAMP"
	case dialect.LBTimestampTZ:
		return "TIMESTAMPTZ"
	case dialect.LBCLOB:
		return "TEXT"
	case dialect.LBBLOB:
		return "BYTEA"
	case dialect.LBBoolean:
		return "BOOLEAN"
	case dialect.LBJSON:
		return "JSONB"
	case dialect.LBXML:
		return "XML"
	default:
		return "TEXT"
	}
}

type PGQuoter struct{}

func (PGQuoter) Quote(name string) string     { return fmt.Sprintf(`"%s"`, strings.ToLower(name)) }
func (PGQuoter) Unquote(quoted string) string { return strings.Trim(quoted, `"`) }

type PGFeatures struct{}

func (PGFeatures) SupportsTransactionalDDL() bool { return true }
func (PGFeatures) SupportsIfNotExists() bool      { return true }
func (PGFeatures) MaxIdentifierLength() int       { return 63 }
func (PGFeatures) SupportsJSONIndex() bool        { return true }
func (PGFeatures) TruncateIsTransactional() bool  { return true }

type PGDDLBuilder struct{}

func (PGDDLBuilder) BuildCreateTable(t *md.TableDef, opts dialect.BuildOptions) (string, error) {
	var b strings.Builder
	schema := t.TableSchema
	if m, ok := opts.SchemaMapping[schema]; ok {
		schema = m
	}

	quote := func(name string) string {
		if opts.NoQuoteIdentifiers {
			return name
		}
		return fmt.Sprintf(`"%s"`, strings.ToLower(name))
	}

	b.WriteString("CREATE TABLE ")
	if opts.IncludeIfNotExists {
		b.WriteString("IF NOT EXISTS ")
	}
	b.WriteString(fmt.Sprintf("%s.%s", quote(schema), quote(t.TableName)))
	b.WriteString(" (\n")
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
	if opts.IncludeComments && t.TableComment != "" {
		if opts.NoQuoteIdentifiers {
			b.WriteString(fmt.Sprintf(";\nCOMMENT ON TABLE %s.%s IS '%s'", schema, t.TableName, t.TableComment))
		} else {
			b.WriteString(fmt.Sprintf(";\nCOMMENT ON TABLE %q.%q IS '%s'", schema, t.TableName, t.TableComment))
		}
	}
	return b.String(), nil
}

func (PGDDLBuilder) BuildCreateIndex(idxs []*md.IndexDef, opts dialect.BuildOptions) (string, error) {
	if len(idxs) == 0 {
		return "", nil
	}
	first := idxs[0]

	// Apply schema mapping
	schema := first.TableSchema
	if m, ok := opts.SchemaMapping[schema]; ok {
		schema = m
	}

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
	b.WriteString(quote(schema) + "." + quote(first.TableName))
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
func (PGDDLBuilder) BuildCreateView(v *md.ViewDef, opts dialect.BuildOptions) (string, error) {
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
func (PGDDLBuilder) BuildCreateTrigger(trg *md.TriggerDef, opts dialect.BuildOptions) (string, error) {
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
	// PostgreSQL triggers use EXECUTE FUNCTION (or PROCEDURE) with the trigger body
	return fmt.Sprintf("CREATE TRIGGER %s %s %s ON %s.%s FOR EACH %s EXECUTE FUNCTION %s",
		quote(trg.TriggerName), trg.TriggerType, trg.TriggerEvent,
		quote(schema), quote(trg.TableName), forEach, trg.TriggerBody), nil
}
func (PGDDLBuilder) BuildCreateFunction(fn *md.FunctionDef, opts dialect.BuildOptions) (string, error) {
	lang := fn.Language
	if lang == "" {
		lang = "plpgsql"
	}
	if fn.FunctionType == "PROCEDURE" {
		return fmt.Sprintf("CREATE OR REPLACE PROCEDURE %s AS $$ %s $$ LANGUAGE %s",
			fn.FunctionName, fn.FunctionBody, lang), nil
	}
	return fmt.Sprintf("CREATE OR REPLACE FUNCTION %s RETURNS %s AS $$ %s $$ LANGUAGE %s",
		fn.FunctionName, fn.ReturnType, fn.FunctionBody, lang), nil
}
func (PGDDLBuilder) BuildCreateSequence(seq *md.SequenceDef, opts dialect.BuildOptions) (string, error) {
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
func (PGDDLBuilder) BuildCreateMView(mv *md.MViewDef, opts dialect.BuildOptions) (string, error) {
	schema := mv.MViewSchema
	if m, ok := opts.SchemaMapping[schema]; ok {
		schema = m
	}
	quote := func(s string) string {
		if opts.NoQuoteIdentifiers {
			return s
		}
		return `"` + s + `"`
	}
	return fmt.Sprintf("CREATE MATERIALIZED VIEW %s.%s AS %s", quote(schema), quote(mv.MViewName), mv.MViewQuery), nil
}
func (PGDDLBuilder) BuildCreateSynonym(syn *md.SynonymDef, opts dialect.BuildOptions) (string, error)         { return "", nil }
func (PGDDLBuilder) BuildCreatePackage(pkg *md.PackageDef, opts dialect.BuildOptions) (string, error)         { return "", nil }
func (PGDDLBuilder) BuildCreatePackageBody(pkg *md.PackageBodyDef, opts dialect.BuildOptions) (string, error) { return "", nil }

type PGDMLHelper struct{}

func (PGDMLHelper) BuildPaginationClause(pageSize, offset int) string {
	return fmt.Sprintf("LIMIT %d OFFSET %d", pageSize, offset)
}
func (PGDMLHelper) BuildCursorPagination(columns []string, lastValues []any) string { return "" }
func (PGDMLHelper) FormatValue(val any, colType dialect.LogicalType) string {
	return fmt.Sprintf("%v", val)
}

func New() dialect.Dialect {
	return dialect.Dialect{
		TypeMapper:       PGTypeMapper{},
		IdentifierQuoter: PGQuoter{},
		Features:         PGFeatures{},
		DDLBuilder:       PGDDLBuilder{},
		DMLHelper:        PGDMLHelper{},
	}
}
