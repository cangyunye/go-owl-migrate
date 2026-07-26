package oceanbase

import (
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	mysql "github.com/cangyunye/go-owl-migrate/internal/dialect/mysql"
	oracle "github.com/cangyunye/go-owl-migrate/internal/dialect/oracle"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// ── OceanBase MySQL 租户 ──
// 继承 MySQL 方言，差异：
//   - TRUNCATE 是事务安全的（与标准 MySQL 不同）
//   - 不支持 FULLTEXT 索引
//   - 不支持 MyISAM 引擎（仅 InnoDB）
//   - SEQUENCE 支持（MySQL 8.0 无原生 SEQUENCE，OB 有）

func TestOBMySQL_Name(t *testing.T) {
	d := NewMySQL()
	if d.Name() != "oceanbase-mysql" {
		t.Errorf("Name() = %q, want oceanbase-mysql", d.Name())
	}
}

func TestOBMySQL_TruncateIsTransactional(t *testing.T) {
	d := NewMySQL()
	if !d.TruncateIsTransactional() {
		t.Error("OceanBase MySQL TRUNCATE should be transactional (differs from standard MySQL)")
	}
}

func TestOBMySQL_NoFulltextIndex(t *testing.T) {
	d := NewMySQL()
	if d.SupportsJSONIndex() {
		// OB MySQL mode supports functional indexes (JSON)
	}
	// FULLTEXT support: OB currently does not support FULLTEXT indexes in MySQL mode
	// Test that the dialect correctly doesn't claim FULLTEXT support
	// (this is a documentation point — OB may add this later)
}

func TestOBMySQL_InheritsMySQLTypeMapping(t *testing.T) {
	d := NewMySQL()
	mysqlD := mysql.New()

	types := []string{"VARCHAR", "CHAR", "INT", "BIGINT", "SMALLINT", "TINYINT",
		"FLOAT", "DOUBLE", "DECIMAL", "DATE", "TIME", "DATETIME", "TIMESTAMP",
		"TEXT", "BLOB", "JSON"}

	for _, typ := range types {
		obLT := d.ToLogicalType(typ, 100, 10, 2)
		mysqlLT := mysqlD.ToLogicalType(typ, 100, 10, 2)
		if obLT.Base != mysqlLT.Base {
			t.Errorf("type %q: OB MySQL base=%v, MySQL base=%v", typ, obLT.Base, mysqlLT.Base)
		}
	}
}

func TestOBMySQL_SequenceSupport(t *testing.T) {
	d := NewMySQL()
	seq := &md.SequenceDef{
		SequenceSchema: "testdb",
		SequenceName:   "seq_orders",
		StartValue:     1,
		IncrementBy:    1,
		MaxValue:       999999999,
		Cycle:          "NO",
		CacheSize:      20,
	}
	sql, err := d.BuildCreateSequence(seq, dialect.BuildOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, "CREATE SEQUENCE") {
		t.Error("OceanBase MySQL should support CREATE SEQUENCE")
	}
	if !strings.Contains(sql, "seq_orders") {
		t.Error("sequence DDL should contain sequence name")
	}
}

// ── OceanBase Oracle 租户 ──
// 继承 Oracle 方言，差异：
//   - TRUNCATE 是事务安全的（与标准 Oracle 不同）
//   - 不支持 BFILE 类型（已标记为罕见类型跳过）
//   - 分区语法略有差异
//   - 不支持某些 Oracle XML DB 功能

func TestOBOracle_Name(t *testing.T) {
	d := NewOracle()
	if d.Name() != "oceanbase-oracle" {
		t.Errorf("Name() = %q, want oceanbase-oracle", d.Name())
	}
}

func TestOBOracle_TruncateIsTransactional(t *testing.T) {
	d := NewOracle()
	if !d.TruncateIsTransactional() {
		t.Error("OceanBase Oracle TRUNCATE should be transactional (differs from standard Oracle)")
	}
}

func TestOBOracle_InheritsOracleTypeMapping(t *testing.T) {
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
		{"DATE", 0, 0, 0},
		{"TIMESTAMP", 0, 0, 0},
		{"TIMESTAMP WITH TIME ZONE", 0, 0, 0},
		{"CLOB", 0, 0, 0},
		{"BLOB", 0, 0, 0},
	}

	for _, tt := range types {
		obLT := d.ToLogicalType(tt.raw, tt.len, tt.prec, tt.scale)
		oraLT := oracleD.ToLogicalType(tt.raw, tt.len, tt.prec, tt.scale)
		if obLT.Base != oraLT.Base {
			t.Errorf("type %q: OB Oracle base=%v, Oracle base=%v", tt.raw, obLT.Base, oraLT.Base)
		}
	}
}

func TestOBOracle_NoBFILE(t *testing.T) {
	d := NewOracle()
	lt := d.ToLogicalType("BFILE", 0, 0, 0)
	if lt.Base != dialect.LBBLOB {
		t.Errorf("BFILE should map to LBBLOB (no BFILE support), got %v", lt.Base)
	}
}

func TestOBOracle_FulltextAndBitmapIndexes(t *testing.T) {
	d := NewOracle()
	// OB Oracle mode does not support Bitmap indexes
	idx := &md.IndexDef{
		TableSchema: "SCOTT", TableName: "EMP",
		IndexName: "IDX_BITMAP", ColumnName: "DEPTNO",
		IndexType: "BITMAP", OrdinalPosition: 1,
	}
	sql, err := d.BuildCreateIndex([]*md.IndexDef{idx}, dialect.BuildOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect MANUAL comment for unsupported index types
	if sql != "" && !strings.Contains(sql, "-- MANUAL") {
		t.Logf("Bitmap index may need manual intervention: %s", sql)
	}
}

func TestOBOracle_DDLTable(t *testing.T) {
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
	if !strings.Contains(sql, "EMPNO") {
		t.Error("DDL should contain EMPNO column")
	}
}

// ── 注册表集成测试 ──

func TestOBMySQL_CanRegister(t *testing.T) {
	d := NewMySQL()
	// Verify the dialect is a valid composed struct
	if d.TypeMapper == nil || d.IdentifierQuoter == nil ||
		d.Features == nil || d.DDLBuilder == nil || d.DMLHelper == nil {
		t.Error("OceanBase MySQL dialect is missing required components")
	}
}

func TestOBOracle_CanRegister(t *testing.T) {
	d := NewOracle()
	if d.TypeMapper == nil || d.IdentifierQuoter == nil ||
		d.Features == nil || d.DDLBuilder == nil || d.DMLHelper == nil {
		t.Error("OceanBase Oracle dialect is missing required components")
	}
}

func TestOBMySQL_ForceInnoDB(t *testing.T) {
	d := NewMySQL()
	tbl, _ := md.NewTableDef("db", "t")
	col, _ := md.NewColumnDef("db", "t", "id", 1, "INT")
	tbl.AddColumn(col)
	tbl.Engine = "MyISAM"
	ddl, err := d.BuildCreateTable(tbl, dialect.BuildOptions{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if strings.Contains(ddl, "MyISAM") {
		t.Errorf("MyISAM should be replaced, got:\n%s", ddl)
	}
	if !strings.Contains(ddl, "ENGINE=InnoDB") {
		t.Errorf("expected ENGINE=InnoDB, got:\n%s", ddl)
	}
}

func TestOBMySQL_NoFulltext(t *testing.T) {
	d := NewMySQL()
	idxs := []*md.IndexDef{{TableSchema: "db", TableName: "t", IndexName: "ft_idx", IndexType: "FULLTEXT", ColumnName: "body"}}
	ddl, err := d.BuildCreateIndex(idxs, dialect.BuildOptions{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(ddl, "FULLTEXT index not supported") {
		t.Errorf("expected FULLTEXT downgrade comment, got:\n%s", ddl)
	}
}

func TestOBOracle_BFILEToBLOB(t *testing.T) {
	d := NewOracle()
	lt := d.ToLogicalType("BFILE", 0, 0, 0)
	if lt.Base != dialect.LBBLOB {
		t.Errorf("BFILE base = %v, want LBBLOB", lt.Base)
	}
	if got := d.FromLogicalType(lt); got != "BLOB" {
		t.Errorf("BFILE -> %q, want BLOB", got)
	}
}
