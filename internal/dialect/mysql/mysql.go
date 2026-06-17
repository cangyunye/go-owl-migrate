package mysql

import (
	"fmt"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

type MySQLTypeMapper struct{}

func (MySQLTypeMapper) Name() string { return "mysql" }

func (MySQLTypeMapper) ToLogicalType(rawType string, length, precision, scale int) dialect.LogicalType {
	upper := strings.ToUpper(rawType)
	switch {
	case upper == "VARCHAR":
		return dialect.LogicalType{Base: dialect.LBVarchar, Length: length}
	case upper == "CHAR":
		return dialect.LogicalType{Base: dialect.LBChar, Length: length}
	case upper == "TEXT" || upper == "MEDIUMTEXT" || upper == "LONGTEXT" || upper == "TINYTEXT":
		return dialect.LogicalType{Base: dialect.LBCLOB}
	case upper == "TINYINT":
		return dialect.LogicalType{Base: dialect.LBSmallInt}
	case upper == "SMALLINT":
		return dialect.LogicalType{Base: dialect.LBSmallInt}
	case upper == "MEDIUMINT" || upper == "INT" || upper == "INTEGER":
		return dialect.LogicalType{Base: dialect.LBInt}
	case upper == "BIGINT" || upper == "SERIAL":
		return dialect.LogicalType{Base: dialect.LBBigInt}
	case upper == "FLOAT":
		return dialect.LogicalType{Base: dialect.LBFloat}
	case upper == "DOUBLE" || upper == "DOUBLE PRECISION" || upper == "REAL":
		return dialect.LogicalType{Base: dialect.LBDouble}
	case upper == "DECIMAL" || upper == "NUMERIC" || upper == "FIXED":
		return dialect.LogicalType{Base: dialect.LBNumeric, Precision: precision, Scale: scale}
	case upper == "BOOLEAN" || upper == "BOOL":
		return dialect.LogicalType{Base: dialect.LBBoolean}
	case upper == "DATE":
		return dialect.LogicalType{Base: dialect.LBDate}
	case upper == "TIME":
		return dialect.LogicalType{Base: dialect.LBTime}
	case upper == "DATETIME":
		return dialect.LogicalType{Base: dialect.LBDatetime}
	case upper == "TIMESTAMP":
		return dialect.LogicalType{Base: dialect.LBTimestamp}
	case upper == "YEAR":
		return dialect.LogicalType{Base: dialect.LBSmallInt}
	case upper == "BLOB" || upper == "LONGBLOB" || upper == "MEDIUMBLOB" || upper == "TINYBLOB":
		return dialect.LogicalType{Base: dialect.LBBLOB}
	case upper == "VARBINARY":
		return dialect.LogicalType{Base: dialect.LBVarBinary, Length: length}
	case upper == "BINARY":
		return dialect.LogicalType{Base: dialect.LBBinary, Length: length}
	case upper == "JSON":
		return dialect.LogicalType{Base: dialect.LBJSON}
	case upper == "ENUM":
		return dialect.LogicalType{Base: dialect.LBEnum}
	case upper == "SET":
		return dialect.LogicalType{Base: dialect.LBVarchar, Length: 255}
	default:
		return dialect.LogicalType{Base: dialect.LBVarchar, Length: length}
	}
}

func (MySQLTypeMapper) FromLogicalType(lt dialect.LogicalType) string {
	switch lt.Base {
	case dialect.LBVarchar:
		if lt.Length > 65535 {
			return "LONGTEXT"
		}
		if lt.Length > 0 {
			return fmt.Sprintf("VARCHAR(%d)", lt.Length)
		}
		return "VARCHAR(255)"
	case dialect.LBChar:
		return fmt.Sprintf("CHAR(%d)", lt.Length)
	case dialect.LBSmallInt:
		return "SMALLINT"
	case dialect.LBInt:
		return "INT"
	case dialect.LBBigInt:
		return "BIGINT"
	case dialect.LBNumeric:
		if lt.Scale > 0 {
			return fmt.Sprintf("DECIMAL(%d,%d)", lt.Precision, lt.Scale)
		}
		if lt.Precision > 0 {
			return fmt.Sprintf("DECIMAL(%d)", lt.Precision)
		}
		return "DECIMAL(65,30)"
	case dialect.LBFloat:
		return "FLOAT"
	case dialect.LBDouble:
		return "DOUBLE"
	case dialect.LBDate:
		return "DATE"
	case dialect.LBTime:
		return "TIME"
	case dialect.LBDatetime:
		return "DATETIME"
	case dialect.LBTimestamp:
		return "TIMESTAMP"
	case dialect.LBTimestampTZ:
		return "TIMESTAMP"
	case dialect.LBCLOB:
		return "LONGTEXT"
	case dialect.LBBLOB:
		return "LONGBLOB"
	case dialect.LBBoolean:
		return "TINYINT(1)"
	case dialect.LBJSON:
		return "JSON"
	case dialect.LBEnum:
		return "VARCHAR(255)"
	default:
		return "LONGTEXT"
	}
}

type MySQLQuoter struct{}

func (MySQLQuoter) Quote(name string) string     { return fmt.Sprintf("`%s`", name) }
func (MySQLQuoter) Unquote(quoted string) string { return strings.Trim(quoted, "`") }

type MySQLFeatures struct{}

func (MySQLFeatures) SupportsTransactionalDDL() bool { return false }
func (MySQLFeatures) SupportsIfNotExists() bool      { return true }
func (MySQLFeatures) MaxIdentifierLength() int       { return 64 }
func (MySQLFeatures) SupportsJSONIndex() bool        { return false }
func (MySQLFeatures) TruncateIsTransactional() bool  { return false }

type MySQLDDLBuilder struct{}

func (MySQLDDLBuilder) BuildCreateTable(t *md.TableDef, opts dialect.BuildOptions) (string, error) {
	var b strings.Builder
	schema := t.TableSchema
	if m, ok := opts.SchemaMapping[schema]; ok {
		schema = m
	}

	quote := func(name string) string {
		if opts.NoQuoteIdentifiers {
			return name
		}
		return fmt.Sprintf("`%s`", name)
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
	if t.Engine != "" {
		b.WriteString(" ENGINE=" + t.Engine)
	}
	return b.String(), nil
}

func (MySQLDDLBuilder) BuildCreateIndex(idxs []*md.IndexDef, opts dialect.BuildOptions) (string, error) {
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
		return "`" + s + "`"
	}

	var b strings.Builder
	b.WriteString("CREATE ")
	if first.Uniqueness == "UNIQUE" {
		b.WriteString("UNIQUE ")
	}
	b.WriteString("INDEX ")
	b.WriteString(fmt.Sprintf("%s ON %s.%s (", quote(first.IndexName), quote(schema), quote(first.TableName)))
	for i, idx := range idxs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quote(idx.ColumnName))
	}
	b.WriteString(")")
	return b.String(), nil
}
func (MySQLDDLBuilder) BuildCreateView(v *md.ViewDef, opts dialect.BuildOptions) (string, error) {
	schema := v.ViewSchema
	if m, ok := opts.SchemaMapping[schema]; ok {
		schema = m
	}
	quote := func(s string) string {
		if opts.NoQuoteIdentifiers {
			return s
		}
		return "`" + s + "`"
	}
	return fmt.Sprintf("CREATE VIEW %s.%s AS %s", quote(schema), quote(v.ViewName), v.ViewDefinition), nil
}
func (MySQLDDLBuilder) BuildCreateTrigger(trg *md.TriggerDef, opts dialect.BuildOptions) (string, error) {
	schema := trg.TableSchema
	if m, ok := opts.SchemaMapping[schema]; ok {
		schema = m
	}
	quote := func(s string) string {
		if opts.NoQuoteIdentifiers {
			return s
		}
		return "`" + s + "`"
	}
	return fmt.Sprintf("CREATE TRIGGER %s %s %s ON %s.%s FOR EACH ROW %s",
		quote(trg.TriggerName), trg.TriggerType, trg.TriggerEvent,
		quote(schema), quote(trg.TableName), trg.TriggerBody), nil
}
func (MySQLDDLBuilder) BuildCreateFunction(fn *md.FunctionDef, opts dialect.BuildOptions) (string, error) {
	if fn.FunctionType == "PROCEDURE" {
		return fmt.Sprintf("CREATE PROCEDURE `%s` %s", fn.FunctionName, fn.FunctionBody), nil
	}
	return fmt.Sprintf("CREATE FUNCTION `%s` RETURNS %s %s", fn.FunctionName, fn.ReturnType, fn.FunctionBody), nil
}
func (MySQLDDLBuilder) BuildCreateSequence(seq *md.SequenceDef, opts dialect.BuildOptions) (string, error)       { return "", nil }
func (MySQLDDLBuilder) BuildCreateMView(mv *md.MViewDef, opts dialect.BuildOptions) (string, error)              { return "", nil }
func (MySQLDDLBuilder) BuildCreateSynonym(syn *md.SynonymDef, opts dialect.BuildOptions) (string, error)         { return "", nil }
func (MySQLDDLBuilder) BuildCreatePackage(pkg *md.PackageDef, opts dialect.BuildOptions) (string, error)         { return "", nil }
func (MySQLDDLBuilder) BuildCreatePackageBody(pkg *md.PackageBodyDef, opts dialect.BuildOptions) (string, error) { return "", nil }

type MySQLDMLHelper struct{}

func (MySQLDMLHelper) BuildPaginationClause(pageSize, offset int) string {
	return fmt.Sprintf("LIMIT %d, %d", offset, pageSize)
}
func (MySQLDMLHelper) BuildCursorPagination(columns []string, lastValues []any) string { return "" }
func (MySQLDMLHelper) FormatValue(val any, colType dialect.LogicalType) string {
	return fmt.Sprintf("%v", val)
}

func New() dialect.Dialect {
	return dialect.Dialect{
		TypeMapper:       MySQLTypeMapper{},
		IdentifierQuoter: MySQLQuoter{},
		Features:         MySQLFeatures{},
		DDLBuilder:       MySQLDDLBuilder{},
		DMLHelper:        MySQLDMLHelper{},
	}
}
