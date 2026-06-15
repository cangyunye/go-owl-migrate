package panweidb

import (
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	"github.com/cangyunye/go-owl-migrate/internal/dialect/mysql"
	"github.com/cangyunye/go-owl-migrate/internal/dialect/oracle"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// ── PanWeiDB MySQL 模式 (B) ──
// 继承 MySQL 方言，差异：
//   - TRUNCATE 是事务安全的（与标准 MySQL 不同，PG 内核行为）
//   - Features 基于 PG（openGauss 内核）
//   - 标识符引用使用反引号（Dolphin 插件原生支持）

func TestPDBMySQL_Name(t *testing.T) {
	d := NewMySQL()
	if d.Name() != "panweidb-mysql" {
		t.Errorf("Name() = %q, want panweidb-mysql", d.Name())
	}
}

// ── PanWeiDB Oracle 模式 (A) ──
// 继承 Oracle 方言，差异：
//   - TRUNCATE 是事务安全的（与标准 Oracle 不同，PG 内核行为）
//   - Features 基于 PG（openGauss 内核）
//   - 标识符引用使用双引号大写（Oracle 风格）

func TestPDBOracle_Name(t *testing.T) {
	d := NewOracle()
	if d.Name() != "panweidb-oracle" {
		t.Errorf("Name() = %q, want panweidb-oracle", d.Name())
	}
}

// ── 类型映射继承 ──

func TestPDBMySQL_InheritsMySQLTypeMapping(t *testing.T) {
	d := NewMySQL()
	mysqlD := mysql.New()

	types := []string{"VARCHAR", "CHAR", "INT", "BIGINT", "SMALLINT", "TINYINT",
		"FLOAT", "DOUBLE", "DECIMAL", "DATE", "TIME", "DATETIME", "TIMESTAMP",
		"TEXT", "BLOB", "JSON", "YEAR", "BOOLEAN"}

	for _, typ := range types {
		pdbLT := d.ToLogicalType(typ, 100, 10, 2)
		mysqlLT := mysqlD.ToLogicalType(typ, 100, 10, 2)
		if pdbLT.Base != mysqlLT.Base {
			t.Errorf("type %q: PanWeiDB base=%v, MySQL base=%v", typ, pdbLT.Base, mysqlLT.Base)
		}
	}
}

// ── 标识符引用 ──

func TestPDBMySQL_Quoter(t *testing.T) {
	d := NewMySQL()
	got := d.Quote("myTable")
	want := "`myTable`"
	if got != want {
		t.Errorf("Quote(\"myTable\") = %q, want %q", got, want)
	}
	// Unquote round-trip
	unquoted := d.Unquote(got)
	if unquoted != "myTable" {
		t.Errorf("Unquote(%q) = %q, want \"myTable\"", got, unquoted)
	}
}

func TestPDBOracle_Quoter(t *testing.T) {
	d := NewOracle()
	got := d.Quote("emp")
	want := `"EMP"`
	if got != want {
		t.Errorf("Quote(\"emp\") = %q, want %q", got, want)
	}
	unquoted := d.Unquote(got)
	if unquoted != "EMP" {
		t.Errorf("Unquote(%q) = %q, want \"EMP\"", got, unquoted)
	}
}

func TestPDBPG_Quoter(t *testing.T) {
	d := New()
	got := d.Quote("MyTable")
	want := `"mytable"`
	if got != want {
		t.Errorf("Quote(\"MyTable\") = %q, want %q", got, want)
	}
	unquoted := d.Unquote(got)
	if unquoted != "mytable" {
		t.Errorf("Unquote(%q) = %q, want \"mytable\"", got, unquoted)
	}
}

// ── Features 继承（PG 内核行为） ──
// 所有三种模式统一使用 PGFeatures（openGauss 内核）：
//   - TruncateIsTransactional = true（区别于标准 MySQL/Oracle 的 false）
//   - SupportsTransactionalDDL = true
//   - SupportsIfNotExists = true

func TestPDBMySQL_TruncateIsTransactional(t *testing.T) {
	d := NewMySQL()
	if !d.TruncateIsTransactional() {
		t.Error("PanWeiDB MySQL TRUNCATE should be transactional (openGauss PG kernel)")
	}
}

func TestPDBOracle_TruncateIsTransactional(t *testing.T) {
	d := NewOracle()
	if !d.TruncateIsTransactional() {
		t.Error("PanWeiDB Oracle TRUNCATE should be transactional (openGauss PG kernel)")
	}
}

// ── DDL 生成 ──

func TestPDBMySQL_DDLTable(t *testing.T) {
	d := NewMySQL()
	tbl, _ := md.NewTableDef("testdb", "users")
	tbl.Engine = "InnoDB"
	col1, _ := md.NewColumnDef("testdb", "users", "id", 1, "BIGINT")
	col1.Nullable = "NO"
	tbl.AddColumn(col1)
	col2, _ := md.NewColumnDef("testdb", "users", "name", 2, "VARCHAR")
	col2.DataLength = 100
	tbl.AddColumn(col2)

	sql, err := d.BuildCreateTable(tbl, dialect.BuildOptions{SkipPartitions: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, "CREATE TABLE") {
		t.Error("DDL should contain CREATE TABLE")
	}
	if !strings.Contains(sql, "`id`") {
		t.Error("DDL should use backtick quoting for id")
	}
	if !strings.Contains(sql, "`name`") {
		t.Error("DDL should use backtick quoting for name")
	}
	if !strings.Contains(sql, "ENGINE=") {
		t.Error("DDL should contain ENGINE= clause for MySQL mode")
	}
}

func TestPDBOracle_DDLTable(t *testing.T) {
	d := NewOracle()
	tbl, _ := md.NewTableDef("SCOTT", "EMP")
	col1, _ := md.NewColumnDef("SCOTT", "EMP", "EMPNO", 1, "NUMBER")
	col1.Nullable = "NO"
	col1.DataPrecision = 4
	tbl.AddColumn(col1)
	col2, _ := md.NewColumnDef("SCOTT", "EMP", "ENAME", 2, "VARCHAR2")
	col2.DataLength = 10
	tbl.AddColumn(col2)

	sql, err := d.BuildCreateTable(tbl, dialect.BuildOptions{SkipPartitions: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, "CREATE TABLE") {
		t.Error("DDL should contain CREATE TABLE")
	}
	if !strings.Contains(sql, `"EMPNO"`) {
		t.Error("Oracle mode DDL should quote EMPNO with double-quotes")
	}
	if !strings.Contains(sql, `"ENAME"`) {
		t.Error("Oracle mode DDL should quote ENAME with double-quotes")
	}
}

func TestPDBMySQL_EngineClause(t *testing.T) {
	d := NewMySQL()
	tbl, _ := md.NewTableDef("testdb", "orders")
	tbl.Engine = "InnoDB"
	col, _ := md.NewColumnDef("testdb", "orders", "id", 1, "INT")
	col.Nullable = "NO"
	tbl.AddColumn(col)

	sql, err := d.BuildCreateTable(tbl, dialect.BuildOptions{SkipPartitions: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, "ENGINE=InnoDB") {
		t.Errorf("DDL should contain ENGINE=InnoDB, got: %s", sql)
	}
}

// ── 分页语法 ──

func TestPDBMySQL_Pagination(t *testing.T) {
	d := NewMySQL()
	got := d.BuildPaginationClause(100, 20)
	want := "LIMIT 20, 100"
	if got != want {
		t.Errorf("BuildPaginationClause(100, 20) = %q, want %q", got, want)
	}
}

func TestPDBOracle_Pagination(t *testing.T) {
	d := NewOracle()
	got := d.BuildPaginationClause(100, 20)
	want := "OFFSET 20 ROWS FETCH NEXT 100 ROWS ONLY"
	if got != want {
		t.Errorf("BuildPaginationClause(100, 20) = %q, want %q", got, want)
	}
}

func TestPDBPG_Pagination(t *testing.T) {
	d := New()
	got := d.BuildPaginationClause(100, 20)
	want := "LIMIT 100 OFFSET 20"
	if got != want {
		t.Errorf("BuildPaginationClause(100, 20) = %q, want %q", got, want)
	}
}

// ── Dialect 结构完整性 ──

func TestPDBMySQL_CanRegister(t *testing.T) {
	d := NewMySQL()
	if d.TypeMapper == nil || d.IdentifierQuoter == nil ||
		d.Features == nil || d.DDLBuilder == nil || d.DMLHelper == nil {
		t.Error("PanWeiDB MySQL dialect is missing required components")
	}
}

func TestPDBOracle_CanRegister(t *testing.T) {
	d := NewOracle()
	if d.TypeMapper == nil || d.IdentifierQuoter == nil ||
		d.Features == nil || d.DDLBuilder == nil || d.DMLHelper == nil {
		t.Error("PanWeiDB Oracle dialect is missing required components")
	}
}

func TestPDB_CanRegister(t *testing.T) {
	d := New()
	if d.TypeMapper == nil || d.IdentifierQuoter == nil ||
		d.Features == nil || d.DDLBuilder == nil || d.DMLHelper == nil {
		t.Error("PanWeiDB PG dialect is missing required components")
	}
}

func TestPDBOracle_InheritsOracleTypeMapping(t *testing.T) {
	d := NewOracle()
	oracleD := oracle.New()

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
		{"BINARY_FLOAT", 0, 0, 0},
		{"BINARY_DOUBLE", 0, 0, 0},
		{"DATE", 0, 0, 0},
		{"TIMESTAMP", 0, 0, 0},
		{"TIMESTAMP WITH TIME ZONE", 0, 0, 0},
		{"CLOB", 0, 0, 0},
		{"BLOB", 0, 0, 0},
	}

	for _, tt := range types {
		pdbLT := d.ToLogicalType(tt.raw, tt.len, tt.prec, tt.scale)
		oraLT := oracleD.ToLogicalType(tt.raw, tt.len, tt.prec, tt.scale)
		if pdbLT.Base != oraLT.Base {
			t.Errorf("type %q: PanWeiDB base=%v, Oracle base=%v", tt.raw, pdbLT.Base, oraLT.Base)
		}
	}
}
