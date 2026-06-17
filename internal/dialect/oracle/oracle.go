package oracle

import (
	"fmt"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// ── Type Mapper ──

type OracleTypeMapper struct{}

func (OracleTypeMapper) Name() string { return "oracle" }

func (OracleTypeMapper) ToLogicalType(rawType string, length, precision, scale int) dialect.LogicalType {
	upper := strings.ToUpper(rawType)
	switch {
	case upper == "VARCHAR2" || upper == "NVARCHAR2":
		return dialect.LogicalType{Base: dialect.LBVarchar, Length: length}
	case upper == "CHAR" || upper == "NCHAR":
		return dialect.LogicalType{Base: dialect.LBChar, Length: length}
	case upper == "NUMBER" && scale == 0 && precision <= 4:
		return dialect.LogicalType{Base: dialect.LBSmallInt, Precision: precision}
	case upper == "NUMBER" && scale == 0 && precision <= 9:
		return dialect.LogicalType{Base: dialect.LBInt, Precision: precision}
	case upper == "NUMBER" && scale == 0 && precision <= 18:
		return dialect.LogicalType{Base: dialect.LBBigInt, Precision: precision}
	case upper == "NUMBER" && scale > 0:
		return dialect.LogicalType{Base: dialect.LBNumeric, Precision: precision, Scale: scale}
	case upper == "NUMBER":
		return dialect.LogicalType{Base: dialect.LBNumeric, Precision: precision, Scale: scale}
	case upper == "BINARY_FLOAT":
		return dialect.LogicalType{Base: dialect.LBFloat}
	case upper == "BINARY_DOUBLE":
		return dialect.LogicalType{Base: dialect.LBDouble}
	case upper == "DATE":
		return dialect.LogicalType{Base: dialect.LBDatetime} // Oracle DATE includes time
	case upper == "TIMESTAMP":
		return dialect.LogicalType{Base: dialect.LBTimestamp}
	case upper == "TIMESTAMP WITH TIME ZONE":
		return dialect.LogicalType{Base: dialect.LBTimestampTZ}
	case upper == "TIMESTAMP WITH LOCAL TIME ZONE":
		return dialect.LogicalType{Base: dialect.LBTimestampTZ}
	case upper == "CLOB" || upper == "NCLOB" || upper == "LONG":
		return dialect.LogicalType{Base: dialect.LBCLOB}
	case upper == "BLOB" || upper == "LONG RAW":
		return dialect.LogicalType{Base: dialect.LBBLOB}
	case upper == "RAW":
		return dialect.LogicalType{Base: dialect.LBVarBinary, Length: length}
	case upper == "ROWID" || upper == "UROWID":
		return dialect.LogicalType{Base: dialect.LBRowID, Length: length}
	case upper == "XMLTYPE":
		return dialect.LogicalType{Base: dialect.LBXML}
	case upper == "BFILE":
		return dialect.LogicalType{Base: dialect.LBVarBinary}
	default:
		return dialect.LogicalType{Base: dialect.LBVarchar, Length: length}
	}
}

func (OracleTypeMapper) FromLogicalType(lt dialect.LogicalType) string {
	switch lt.Base {
	case dialect.LBVarchar:
		if lt.Length > 4000 {
			return "CLOB"
		}
		return fmt.Sprintf("VARCHAR2(%d)", lt.Length)
	case dialect.LBChar:
		return fmt.Sprintf("CHAR(%d)", lt.Length)
	case dialect.LBSmallInt:
		return fmt.Sprintf("NUMBER(%d,0)", lt.Precision)
	case dialect.LBInt:
		return fmt.Sprintf("NUMBER(%d,0)", lt.Precision)
	case dialect.LBBigInt:
		return fmt.Sprintf("NUMBER(%d,0)", lt.Precision)
	case dialect.LBNumeric:
		if lt.Scale > 0 {
			return fmt.Sprintf("NUMBER(%d,%d)", lt.Precision, lt.Scale)
		}
		return "NUMBER"
	case dialect.LBFloat:
		return "BINARY_FLOAT"
	case dialect.LBDouble:
		return "BINARY_DOUBLE"
	case dialect.LBDatetime:
		return "DATE"
	case dialect.LBTimestamp:
		return "TIMESTAMP"
	case dialect.LBTimestampTZ:
		return "TIMESTAMP WITH TIME ZONE"
	case dialect.LBCLOB:
		return "CLOB"
	case dialect.LBBLOB:
		return "BLOB"
	case dialect.LBVarBinary:
		return fmt.Sprintf("RAW(%d)", lt.Length)
	case dialect.LBJSON:
		return "CLOB"
	case dialect.LBXML:
		return "XMLTYPE"
	case dialect.LBBoolean:
		return "NUMBER(1)"
	case dialect.LBRowID:
		return "ROWID"
	default:
		return "VARCHAR2(4000)"
	}
}

// ── Quoter ──

type OracleQuoter struct{}

func (OracleQuoter) Quote(name string) string     { return fmt.Sprintf(`"%s"`, strings.ToUpper(name)) }
func (OracleQuoter) Unquote(quoted string) string { return strings.Trim(quoted, `"`) }

// ── Features ──

type OracleFeatures struct{}

func (OracleFeatures) SupportsTransactionalDDL() bool { return false }
func (OracleFeatures) SupportsIfNotExists() bool      { return false }
func (OracleFeatures) MaxIdentifierLength() int       { return 128 }
func (OracleFeatures) SupportsJSONIndex() bool        { return false }
func (OracleFeatures) TruncateIsTransactional() bool  { return false }

// ── DDL Builder ──

type OracleDDLBuilder struct{}

func (OracleDDLBuilder) BuildCreateTable(t *md.TableDef, opts dialect.BuildOptions) (string, error) {
	var b strings.Builder

	quote := func(name string) string {
		if opts.NoQuoteIdentifiers {
			return name
		}
		return fmt.Sprintf(`"%s"`, strings.ToUpper(name))
	}

	b.WriteString("CREATE TABLE ")
	b.WriteString(fmt.Sprintf("%s.%s", quote(t.TableSchema), quote(t.TableName)))
	b.WriteString(" (\n")
	cols := t.GetColumns()
	for i, col := range cols {
		b.WriteString("  ")
		b.WriteString(quote(col.ColumnName))
		b.WriteString(" ")
		b.WriteString(col.DataType)
		if col.DataLength > 0 && col.DataType == "VARCHAR2" {
			// already printed from CSV
		}
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

func (OracleDDLBuilder) BuildCreateIndex(idxs []*md.IndexDef, opts dialect.BuildOptions) (string, error) {
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
		return `"` + strings.ToUpper(s) + `"`
	}

	var b strings.Builder
	b.WriteString("CREATE ")

	// Oracle: BITMAP is an index type, not a uniqueness qualifier
	if strings.EqualFold(first.IndexType, "BITMAP") {
		b.WriteString("BITMAP ")
	} else if first.Uniqueness == "UNIQUE" {
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
func (OracleDDLBuilder) BuildCreateView(v *md.ViewDef, opts dialect.BuildOptions) (string, error) {
	schema := v.ViewSchema
	if m, ok := opts.SchemaMapping[schema]; ok {
		schema = m
	}
	quote := func(s string) string {
		if opts.NoQuoteIdentifiers {
			return s
		}
		return `"` + strings.ToUpper(s) + `"`
	}
	return fmt.Sprintf("CREATE VIEW %s.%s AS %s", quote(schema), quote(v.ViewName), v.ViewDefinition), nil
}
func (OracleDDLBuilder) BuildCreateTrigger(trg *md.TriggerDef, opts dialect.BuildOptions) (string, error) {
	schema := trg.TableSchema
	if m, ok := opts.SchemaMapping[schema]; ok {
		schema = m
	}
	quote := func(s string) string {
		if opts.NoQuoteIdentifiers {
			return s
		}
		return `"` + strings.ToUpper(s) + `"`
	}
	triggerType := trg.TriggerType // BEFORE/AFTER/INSTEAD OF
	triggerEvent := trg.TriggerEvent
	forEach := trg.ForEach
	if forEach == "" {
		forEach = "ROW"
	}
	return fmt.Sprintf("CREATE OR REPLACE TRIGGER %s\n%s %s\nON %s.%s\nFOR EACH %s\n%s",
		quote(trg.TriggerName), triggerType, triggerEvent,
		quote(schema), quote(trg.TableName), forEach, trg.TriggerBody), nil
}
func (OracleDDLBuilder) BuildCreateFunction(fn *md.FunctionDef, opts dialect.BuildOptions) (string, error) {
	if fn.FunctionType == "PROCEDURE" {
		return fmt.Sprintf("CREATE OR REPLACE PROCEDURE %s %s", fn.FunctionName, fn.FunctionBody), nil
	}
	return fmt.Sprintf("CREATE OR REPLACE FUNCTION %s RETURN %s AS %s", fn.FunctionName, fn.ReturnType, fn.FunctionBody), nil
}
func (OracleDDLBuilder) BuildCreateSequence(seq *md.SequenceDef, opts dialect.BuildOptions) (string, error) {
	schema := seq.SequenceSchema
	if m, ok := opts.SchemaMapping[schema]; ok {
		schema = m
	}
	quote := func(s string) string {
		if opts.NoQuoteIdentifiers {
			return s
		}
		return `"` + strings.ToUpper(s) + `"`
	}
	cycle := "NOCYCLE"
	if strings.EqualFold(seq.Cycle, "YES") {
		cycle = "CYCLE"
	}
	return fmt.Sprintf("CREATE SEQUENCE %s.%s START WITH %d INCREMENT BY %d MINVALUE %d MAXVALUE %d %s CACHE %d",
		quote(schema), quote(seq.SequenceName),
		seq.StartValue, seq.IncrementBy, seq.MinValue, seq.MaxValue,
		cycle, seq.CacheSize), nil
}
func (OracleDDLBuilder) BuildCreateMView(mv *md.MViewDef, opts dialect.BuildOptions) (string, error) {
	schema := mv.MViewSchema
	if m, ok := opts.SchemaMapping[schema]; ok {
		schema = m
	}
	quote := func(s string) string {
		if opts.NoQuoteIdentifiers {
			return s
		}
		return `"` + strings.ToUpper(s) + `"`
	}
	return fmt.Sprintf("CREATE MATERIALIZED VIEW %s.%s AS %s", quote(schema), quote(mv.MViewName), mv.MViewQuery), nil
}
func (OracleDDLBuilder) BuildCreateSynonym(syn *md.SynonymDef, opts dialect.BuildOptions) (string, error) {
	schema := syn.SynonymSchema
	if m, ok := opts.SchemaMapping[schema]; ok {
		schema = m
	}
	targetSchema := syn.TargetSchema
	if m, ok := opts.SchemaMapping[targetSchema]; ok {
		targetSchema = m
	}
	quote := func(s string) string {
		if opts.NoQuoteIdentifiers {
			return s
		}
		return `"` + strings.ToUpper(s) + `"`
	}
	sql := "CREATE"
	if strings.EqualFold(syn.IsPublic, "YES") {
		sql += " PUBLIC"
	}
	sql += fmt.Sprintf(" SYNONYM %s.%s FOR %s.%s",
		quote(schema), quote(syn.SynonymName),
		quote(targetSchema), quote(syn.TargetName))
	return sql, nil
}
func (OracleDDLBuilder) BuildCreatePackage(pkg *md.PackageDef, opts dialect.BuildOptions) (string, error) {
	return fmt.Sprintf("CREATE OR REPLACE PACKAGE %s AS\n%s\nEND %s;", pkg.PackageName, pkg.PackageSpec, pkg.PackageName), nil
}
func (OracleDDLBuilder) BuildCreatePackageBody(pkg *md.PackageBodyDef, opts dialect.BuildOptions) (string, error) {
	return fmt.Sprintf("CREATE OR REPLACE PACKAGE BODY %s AS\n%s\nEND %s;", pkg.PackageName, pkg.PackageBody, pkg.PackageName), nil
}

// ── DML Helper ──

type OracleDMLHelper struct{}

func (OracleDMLHelper) BuildPaginationClause(pageSize, offset int) string {
	return fmt.Sprintf("OFFSET %d ROWS FETCH NEXT %d ROWS ONLY", offset, pageSize)
}
func (OracleDMLHelper) BuildCursorPagination(columns []string, lastValues []any) string {
	return ""
}
func (OracleDMLHelper) FormatValue(val any, colType dialect.LogicalType) string {
	return fmt.Sprintf("%v", val)
}

// ── Dialect ──

func New() dialect.Dialect {
	return dialect.Dialect{
		TypeMapper:       OracleTypeMapper{},
		IdentifierQuoter: OracleQuoter{},
		Features:         OracleFeatures{},
		DDLBuilder:       OracleDDLBuilder{},
		DMLHelper:        OracleDMLHelper{},
	}
}
