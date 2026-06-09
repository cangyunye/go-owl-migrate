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

func (PGQuoter) Quote(name string) string   { return fmt.Sprintf(`"%s"`, strings.ToLower(name)) }
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
	b.WriteString("CREATE TABLE ")
	if opts.IncludeIfNotExists {
		b.WriteString("IF NOT EXISTS ")
	}
	b.WriteString(fmt.Sprintf(`"%s"."%s"`, strings.ToLower(schema), strings.ToLower(t.TableName)))
	b.WriteString(" (\n")
	cols := t.GetColumns()
	for i, col := range cols {
		b.WriteString("  ")
		b.WriteString(fmt.Sprintf(`"%s"`, strings.ToLower(col.ColumnName)))
		b.WriteString(" ")
		// Use dialect mapping for type
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
		b.WriteString(fmt.Sprintf(";\nCOMMENT ON TABLE %q.%q IS '%s'", schema, t.TableName, t.TableComment))
	}
	return b.String(), nil
}

func (PGDDLBuilder) BuildCreateIndex(idx *md.IndexDef) (string, error) { return "", nil }
func (PGDDLBuilder) BuildCreateView(v *md.ViewDef) (string, error)    { return "", nil }
func (PGDDLBuilder) BuildCreateTrigger(trg *md.TriggerDef) (string, error) { return "", nil }
func (PGDDLBuilder) BuildCreateFunction(fn *md.FunctionDef) (string, error) { return "", nil }
func (PGDDLBuilder) BuildCreateSequence(seq *md.SequenceDef) (string, error) { return "", nil }
func (PGDDLBuilder) BuildCreateMView(mv *md.MViewDef) (string, error)     { return "", nil }
func (PGDDLBuilder) BuildCreateSynonym(syn *md.SynonymDef) (string, error) { return "", nil }
func (PGDDLBuilder) BuildCreatePackage(pkg *md.PackageDef) (string, error)       { return "", nil }
func (PGDDLBuilder) BuildCreatePackageBody(pkg *md.PackageBodyDef) (string, error) { return "", nil }

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
