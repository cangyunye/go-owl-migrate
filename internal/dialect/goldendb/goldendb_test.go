package goldendb

import (
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	mysql "github.com/cangyunye/go-owl-migrate/internal/dialect/mysql"
	oracle "github.com/cangyunye/go-owl-migrate/internal/dialect/oracle"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// ── GoldenDB MySQL 租户 ──

func TestGoldenDBMySQL_Name(t *testing.T) {
	d := NewMySQL()
	if d.Name() != "goldendb-mysql" {
		t.Errorf("Name() = %q, want goldendb-mysql", d.Name())
	}
}

func TestGoldenDBMySQL_InheritsMySQLTypeMapping(t *testing.T) {
	d := NewMySQL()

	tests := []struct {
		rawType   string
		length    int
		precision int
		scale     int
		wantBase  dialect.LogicalBase
	}{
		{"VARCHAR", 100, 0, 0, dialect.LBVarchar},
		{"INT", 0, 0, 0, dialect.LBInt},
		{"BIGINT", 0, 0, 0, dialect.LBBigInt},
		{"DECIMAL", 0, 10, 2, dialect.LBNumeric},
		{"DATETIME", 0, 0, 0, dialect.LBDatetime},
		{"TEXT", 0, 0, 0, dialect.LBCLOB},
		{"JSON", 0, 0, 0, dialect.LBJSON},
		{"ENUM", 0, 0, 0, dialect.LBEnum},
	}

	for _, tt := range tests {
		t.Run(tt.rawType, func(t *testing.T) {
			lt := d.ToLogicalType(tt.rawType, tt.length, tt.precision, tt.scale)
			if lt.Base != tt.wantBase {
				t.Errorf("ToLogicalType(%q) base = %v, want %v", tt.rawType, lt.Base, tt.wantBase)
			}
		})
	}
}

func TestGoldenDBMySQL_InheritsMySQLQuoting(t *testing.T) {
	d := NewMySQL()
	if q := d.Quote("table_name"); q != "`table_name`" {
		t.Errorf("Quote = %q, want `table_name`", q)
	}
	if q := d.Quote("column"); q != "`column`" {
		t.Errorf("Quote = %q, want `column`", q)
	}
}

func TestGoldenDBMySQL_Features(t *testing.T) {
	d := NewMySQL()
	if d.SupportsTransactionalDDL() {
		t.Error("GoldenDB MySQL should not support transactional DDL")
	}
	if !d.SupportsIfNotExists() {
		t.Error("GoldenDB MySQL should support IF NOT EXISTS")
	}
	if d.MaxIdentifierLength() != 64 {
		t.Errorf("MaxIdentifierLength = %d, want 64", d.MaxIdentifierLength())
	}
	if d.TruncateIsTransactional() {
		t.Error("GoldenDB MySQL TRUNCATE should not be transactional")
	}
}

func TestGoldenDBMySQL_Pagination(t *testing.T) {
	d := NewMySQL()
	clause := d.BuildPaginationClause(5000, 0)
	if !strings.Contains(clause, "5000") {
		t.Errorf("pagination should contain page size: %q", clause)
	}
	if !strings.Contains(clause, "LIMIT") {
		t.Errorf("pagination should contain LIMIT: %q", clause)
	}
}

func TestGoldenDBMySQL_DDLTable(t *testing.T) {
	d := NewMySQL()
	tbl, _ := md.NewTableDef("testdb", "users")
	col1, _ := md.NewColumnDef("testdb", "users", "id", 1, "INT")
	col1.Nullable = "NO"
	col2, _ := md.NewColumnDef("testdb", "users", "name", 2, "VARCHAR")
	col2.DataLength = 100
	tbl.AddColumn(col1)
	tbl.AddColumn(col2)

	sql, err := d.BuildCreateTable(tbl, dialect.BuildOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, "CREATE TABLE") {
		t.Error("DDL should contain CREATE TABLE")
	}
	if !strings.Contains(sql, "`testdb`.`users`") {
		t.Error("DDL should quote identifiers with backticks")
	}
	if !strings.Contains(sql, "`id`") {
		t.Error("DDL should contain column id")
	}
}

// ── GoldenDB Oracle 租户 ──

func TestGoldenDBOracle_Name(t *testing.T) {
	d := NewOracle()
	if d.Name() != "goldendb-oracle" {
		t.Errorf("Name() = %q, want goldendb-oracle", d.Name())
	}
}

func TestGoldenDBOracle_InheritsOracleTypeMapping(t *testing.T) {
	d := NewOracle()

	tests := []struct {
		rawType   string
		length    int
		precision int
		scale     int
		wantBase  dialect.LogicalBase
	}{
		{"VARCHAR2", 100, 0, 0, dialect.LBVarchar},
		{"NUMBER", 0, 4, 0, dialect.LBSmallInt},
		{"NUMBER", 0, 10, 0, dialect.LBBigInt}, // 10 > 9, falls to BIGINT
		{"NUMBER", 0, 10, 2, dialect.LBNumeric},
		{"DATE", 0, 0, 0, dialect.LBDatetime},
		{"CLOB", 0, 0, 0, dialect.LBCLOB},
		{"BLOB", 0, 0, 0, dialect.LBBLOB},
		{"TIMESTAMP", 0, 0, 0, dialect.LBTimestamp},
	}

	for _, tt := range tests {
		t.Run(tt.rawType, func(t *testing.T) {
			lt := d.ToLogicalType(tt.rawType, tt.length, tt.precision, tt.scale)
			if lt.Base != tt.wantBase {
				t.Errorf("ToLogicalType(%q) base = %v, want %v", tt.rawType, lt.Base, tt.wantBase)
			}
		})
	}
}

func TestGoldenDBOracle_InheritsOracleQuoting(t *testing.T) {
	d := NewOracle()
	q := d.Quote("EMP")
	if !strings.Contains(q, "EMP") {
		t.Errorf("Oracle-style quoting should uppercase: %q", q)
	}
}

func TestGoldenDBOracle_Features(t *testing.T) {
	d := NewOracle()
	if d.SupportsTransactionalDDL() {
		t.Error("GoldenDB Oracle should not support transactional DDL")
	}
	if d.SupportsIfNotExists() {
		t.Error("GoldenDB Oracle should not support IF NOT EXISTS")
	}
	if d.TruncateIsTransactional() {
		t.Error("GoldenDB Oracle TRUNCATE should not be transactional")
	}
}

func TestGoldenDBOracle_Pagination(t *testing.T) {
	d := NewOracle()
	clause := d.BuildPaginationClause(5000, 0)
	if !strings.Contains(clause, "FETCH") {
		t.Errorf("Oracle-style pagination should use FETCH: %q", clause)
	}
	if !strings.Contains(clause, "5000") {
		t.Errorf("pagination should contain page size: %q", clause)
	}
}

func TestGoldenDBOracle_DDLTable(t *testing.T) {
	d := NewOracle()
	tbl, _ := md.NewTableDef("SCOTT", "EMP")
	col1, _ := md.NewColumnDef("SCOTT", "EMP", "EMPNO", 1, "NUMBER")
	col1.Nullable = "NO"
	col1.DataPrecision = 4
	tbl.AddColumn(col1)

	sql, err := d.BuildCreateTable(tbl, dialect.BuildOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, "CREATE TABLE") {
		t.Error("DDL should contain CREATE TABLE")
	}
	if !strings.Contains(sql, "EMPNO") {
		t.Error("DDL should contain EMPNO column")
	}
}

// ── 验证 GoldenDB 与父方言的类型映射一致性 ──

func TestGoldenDBMySQL_EqualToNativeMySQL(t *testing.T) {
	gdb := NewMySQL()
	mysqlD := mysql.New()

	types := []string{"VARCHAR", "CHAR", "INT", "BIGINT", "SMALLINT", "TINYINT",
		"FLOAT", "DOUBLE", "DECIMAL", "DATE", "TIME", "DATETIME", "TIMESTAMP",
		"TEXT", "BLOB", "JSON", "ENUM"}

	for _, typ := range types {
		gdbLT := gdb.ToLogicalType(typ, 100, 10, 2)
		mysqlLT := mysqlD.ToLogicalType(typ, 100, 10, 2)
		if gdbLT.Base != mysqlLT.Base {
			t.Errorf("type %q: GoldenDB base=%v, MySQL base=%v", typ, gdbLT.Base, mysqlLT.Base)
		}
	}
}

func TestGoldenDBOracle_EqualToNativeOracle(t *testing.T) {
	gdb := NewOracle()
	oracleD := oracle.New()

	// Import oracle package
	_ = oracleD

	types := []struct {
		raw   string
		len   int
		prec  int
		scale int
	}{
		{"VARCHAR2", 100, 0, 0},
		{"CHAR", 10, 0, 0},
		{"NUMBER", 0, 4, 0},
		{"NUMBER", 0, 10, 0},
		{"NUMBER", 0, 18, 0},
		{"NUMBER", 0, 10, 2},
		{"DATE", 0, 0, 0},
		{"TIMESTAMP", 0, 0, 0},
		{"CLOB", 0, 0, 0},
		{"BLOB", 0, 0, 0},
	}

	for _, tt := range types {
		gdbLT := gdb.ToLogicalType(tt.raw, tt.len, tt.prec, tt.scale)
		oraLT := oracleD.ToLogicalType(tt.raw, tt.len, tt.prec, tt.scale)
		if gdbLT.Base != oraLT.Base {
			t.Errorf("type %q: GoldenDB Oracle base=%v, Oracle base=%v", tt.raw, gdbLT.Base, oraLT.Base)
		}
	}
}
