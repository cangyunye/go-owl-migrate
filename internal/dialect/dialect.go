package dialect

import md "github.com/cangyunye/go-owl-migrate/internal/metadata"

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
	AddRowIDColumn     bool
	IdentityToSerial   bool
	SkipPartitions     bool
	QuoteAllIdentifiers bool
}

// DDLBuilder generates DDL statements.
type DDLBuilder interface {
	BuildCreateTable(t *md.TableDef, opts BuildOptions) (string, error)
	BuildCreateIndex(idx *md.IndexDef) (string, error)
	BuildCreateView(v *md.ViewDef) (string, error)
	BuildCreateTrigger(trg *md.TriggerDef) (string, error)
	BuildCreateFunction(fn *md.FunctionDef) (string, error)
	BuildCreateSequence(seq *md.SequenceDef) (string, error)
	BuildCreateMView(mv *md.MViewDef) (string, error)
	BuildCreateSynonym(syn *md.SynonymDef) (string, error)
	BuildCreatePackage(pkg *md.PackageDef) (string, error)
	BuildCreatePackageBody(pkg *md.PackageBodyDef) (string, error)
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
