package dialect

import (
	"strconv"
	"strings"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// LogicalBase is the database-independent base type.
type LogicalBase int

const (
	LBVarchar LogicalBase = iota
	LBChar
	LBInt
	LBBigInt
	LBSmallInt
	LBNumeric
	LBFloat
	LBDouble
	LBDate
	LBTime
	LBDatetime
	LBTimestamp
	LBTimestampTZ
	LBInterval
	LBBoolean
	LBCLOB
	LBBLOB
	LBJSON
	LBXML
	LBEnum
	LBBinary
	LBVarBinary
	LBIntervalYM
	LBIntervalDS
	LBRowID
)

func (lb LogicalBase) String() string {
	names := map[LogicalBase]string{
		LBVarchar: "VARCHAR", LBChar: "CHAR", LBInt: "INT", LBBigInt: "BIGINT",
		LBSmallInt: "SMALLINT", LBNumeric: "NUMERIC", LBFloat: "FLOAT", LBDouble: "DOUBLE",
		LBDate: "DATE", LBTime: "TIME", LBDatetime: "DATETIME", LBTimestamp: "TIMESTAMP",
		LBTimestampTZ: "TIMESTAMPTZ", LBInterval: "INTERVAL", LBBoolean: "BOOLEAN",
		LBCLOB: "CLOB", LBBLOB: "BLOB", LBJSON: "JSON", LBXML: "XML", LBEnum: "ENUM",
		LBBinary: "BINARY", LBVarBinary: "VARBINARY", LBIntervalYM: "INTERVALYM",
		LBIntervalDS: "INTERVALDS", LBRowID: "ROWID",
	}
	if n, ok := names[lb]; ok {
		return n
	}
	return "UNKNOWN"
}

// LogicalType is a database-independent type with precision/length metadata.
type LogicalType struct {
	Base      LogicalBase
	Length    int
	Precision int
	Scale     int
}

// TypeMapper maps between raw DB types and logical types.
type TypeMapper interface {
	Name() string
	ToLogicalType(rawType string, length, precision, scale int) LogicalType
	FromLogicalType(lt LogicalType) string
}

// IdentifierQuoter quotes identifiers per database rules.
type IdentifierQuoter interface {
	Quote(name string) string
	Unquote(quoted string) string
}

// Features describes database capabilities.
type Features interface {
	SupportsTransactionalDDL() bool
	SupportsIfNotExists() bool
	MaxIdentifierLength() int
	SupportsJSONIndex() bool
	TruncateIsTransactional() bool
}

// BuildOptions controls DDL generation behavior.
type BuildOptions struct {
	TargetDialect      string
	SchemaMapping      map[string]string
	IncludeComments    bool
	IncludeIfNotExists bool
	IncludeDrop        bool
	TypeOverrides      map[string]string
	BooleanMapping     map[string]bool
	EmptyStringToNull  bool
	AddRowIDColumn     bool
	IdentityToSerial   bool
	SkipPartitions     bool
	NoQuoteIdentifiers bool
	// PreserveIdentifierCase quotes identifiers without case folding. Required
	// by the runtime import path, whose quoting keeps the metadata's exact
	// casing (PG folds to lower-case and Oracle to upper-case otherwise).
	PreserveIdentifierCase bool
}

// DDLBuilder generates DDL statements.
type DDLBuilder interface {
	BuildCreateTable(t *md.TableDef, opts BuildOptions) (string, error)
	BuildCreateIndex(idxs []*md.IndexDef, opts BuildOptions) (string, error)
	BuildCreateView(v *md.ViewDef, opts BuildOptions) (string, error)
	BuildCreateTrigger(trg *md.TriggerDef, opts BuildOptions) (string, error)
	BuildCreateFunction(fn *md.FunctionDef, opts BuildOptions) (string, error)
	BuildCreateSequence(seq *md.SequenceDef, opts BuildOptions) (string, error)
	BuildCreateMView(mv *md.MViewDef, opts BuildOptions) (string, error)
	BuildCreateSynonym(syn *md.SynonymDef, opts BuildOptions) (string, error)
	BuildCreatePackage(pkg *md.PackageDef, opts BuildOptions) (string, error)
	BuildCreatePackageBody(pkg *md.PackageBodyDef, opts BuildOptions) (string, error)
}

// DMLHelper generates DML syntax.
type DMLHelper interface {
	BuildPaginationClause(pageSize, offset int) string
	BuildCursorPagination(columns []string, lastValues []any) string
	FormatValue(val any, colType LogicalType) string
}

// Dialect composes all dialect capabilities.
type Dialect struct {
	TypeMapper
	IdentifierQuoter
	Features
	DDLBuilder
	DMLHelper
}

// ApplyTypeOverride returns the configured override for a raw column type, if
// any, substituting %l/%p/%s with the column's length/precision/scale.
func ApplyTypeOverride(rawType string, length, precision, scale int, opts BuildOptions) (string, bool) {
	tmpl, ok := opts.TypeOverrides[strings.ToUpper(strings.TrimSpace(rawType))]
	if !ok {
		return "", false
	}
	r := strings.NewReplacer(
		"%l", strconv.Itoa(length),
		"%p", strconv.Itoa(precision),
		"%s", strconv.Itoa(scale),
	)
	return r.Replace(tmpl), true
}

// RenderDefault renders a column DEFAULT clause, honoring empty_string_to_null
// and boolean_mapping for boolean-typed columns.
func RenderDefault(colType, defVal string, opts BuildOptions, numericBoolean bool) string {
	if opts.EmptyStringToNull && (defVal == "" || defVal == "''") {
		return " DEFAULT NULL"
	}
	if lit, ok := opts.BooleanMapping[defVal]; ok && isBooleanTypeName(colType) {
		if numericBoolean {
			if lit {
				return " DEFAULT 1"
			}
			return " DEFAULT 0"
		}
		if lit {
			return " DEFAULT TRUE"
		}
		return " DEFAULT FALSE"
	}
	return " DEFAULT " + defVal
}

func isBooleanTypeName(t string) bool {
	u := strings.ToUpper(strings.TrimSpace(t))
	return strings.Contains(u, "BOOL") || u == "NUMBER(1)" || u == "TINYINT(1)"
}

// LooksLikeFullDDL reports whether s is already a complete CREATE statement
// (e.g., DBMS_METADATA.GET_DDL or pg_get_functiondef output) rather than a
// body fragment that needs wrapping in a CREATE template.
func LooksLikeFullDDL(s string) bool {
	u := strings.ToUpper(strings.TrimSpace(s))
	return strings.HasPrefix(u, "CREATE ")
}

// PartitionClause returns the partition clause to append to a CREATE TABLE
// statement when partition migration is enabled and the table carries a usable
// PARTITION BY definition; otherwise it returns "".
func PartitionClause(t *md.TableDef, opts BuildOptions) string {
	if opts.SkipPartitions || !strings.EqualFold(t.Partitioned, "YES") {
		return ""
	}
	info := strings.TrimSpace(t.PartitionInfo)
	if info == "" || !strings.Contains(strings.ToUpper(info), "PARTITION BY") {
		return ""
	}
	return "\n" + info
}
