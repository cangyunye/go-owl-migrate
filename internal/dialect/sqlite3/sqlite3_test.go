package sqlite3

import (
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func TestSQLite3_Name(t *testing.T) {
	d := New()
	if d.Name() != "sqlite3" {
		t.Errorf("Name() = %q, want sqlite3", d.Name())
	}
}

func TestSQLite3_TypeMapping(t *testing.T) {
	d := New()
	tests := []struct {
		raw    string
		length int
		want   dialect.LogicalBase
	}{
		{"TEXT", 0, dialect.LBVarchar},
		{"VARCHAR", 100, dialect.LBVarchar},
		{"INTEGER", 0, dialect.LBInt},
		{"INT", 0, dialect.LBInt},
		{"BIGINT", 0, dialect.LBInt},
		{"REAL", 0, dialect.LBFloat},
		{"FLOAT", 0, dialect.LBFloat},
		{"BLOB", 0, dialect.LBBLOB},
		{"NUMERIC", 0, dialect.LBNumeric},
		{"DECIMAL", 0, dialect.LBNumeric},
		{"BOOLEAN", 0, dialect.LBBoolean},
		{"DATE", 0, dialect.LBDate},
		{"TIMESTAMP", 0, dialect.LBTimestamp},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			lt := d.ToLogicalType(tt.raw, tt.length, 0, 0)
			if lt.Base != tt.want {
				t.Errorf("ToLogicalType(%q) base = %v, want %v", tt.raw, lt.Base, tt.want)
			}
		})
	}
}

func TestSQLite3_ReverseTypeMapping(t *testing.T) {
	d := New()
	tests := []struct {
		base dialect.LogicalBase
		want string
	}{
		{dialect.LBVarchar, "TEXT"},
		{dialect.LBInt, "INTEGER"},
		{dialect.LBBigInt, "INTEGER"},
		{dialect.LBFloat, "REAL"},
		{dialect.LBNumeric, "INTEGER"},
		{dialect.LBBLOB, "BLOB"},
		{dialect.LBBoolean, "INTEGER"},
		{dialect.LBTimestamp, "TEXT"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := d.FromLogicalType(dialect.LogicalType{Base: tt.base})
			if got != tt.want {
				t.Errorf("FromLogicalType(%v) = %q, want %q", tt.base, got, tt.want)
			}
		})
	}
}

func TestSQLite3_Quoting(t *testing.T) {
	d := New()
	if q := d.Quote("my_table"); q != `"my_table"` {
		t.Errorf("Quote = %q, want \"my_table\"", q)
	}
	if q := d.Unquote(`"my_table"`); q != "my_table" {
		t.Errorf("Unquote = %q, want my_table", q)
	}
}

func TestSQLite3_Features(t *testing.T) {
	d := New()
	if !d.SupportsTransactionalDDL() {
		t.Error("SQLite3 should support transactional DDL")
	}
	if !d.SupportsIfNotExists() {
		t.Error("SQLite3 should support IF NOT EXISTS")
	}
	if d.SupportsJSONIndex() {
		t.Error("SQLite3 should not support JSON index")
	}
	if !d.TruncateIsTransactional() {
		t.Error("SQLite3 TRUNCATE should be transactional")
	}
}

func TestSQLite3_DDLTable(t *testing.T) {
	d := New()
	tbl, _ := md.NewTableDef("main", "users")
	col1, _ := md.NewColumnDef("main", "users", "id", 1, "INTEGER")
	col1.Nullable = "NO"
	col2, _ := md.NewColumnDef("main", "users", "name", 2, "TEXT")
	col2.Nullable = "YES"
	tbl.AddColumn(col1)
	tbl.AddColumn(col2)

	sql, err := d.BuildCreateTable(tbl, dialect.BuildOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, "CREATE TABLE") {
		t.Error("DDL should contain CREATE TABLE")
	}
	if !strings.Contains(sql, `"main"."users"`) {
		t.Errorf("DDL should contain quoted schema.table: %s", sql)
	}
	if !strings.Contains(sql, `"id" INTEGER NOT NULL`) {
		t.Errorf("DDL should contain id column: %s", sql)
	}
	if !strings.Contains(sql, `"name" TEXT`) {
		t.Errorf("DDL should contain name column: %s", sql)
	}
}

func TestSQLite3_DDLTableIfNotExists(t *testing.T) {
	d := New()
	tbl, _ := md.NewTableDef("main", "t")
	col, _ := md.NewColumnDef("main", "t", "c", 1, "INTEGER")
	tbl.AddColumn(col)

	sql, err := d.BuildCreateTable(tbl, dialect.BuildOptions{IncludeIfNotExists: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, "IF NOT EXISTS") {
		t.Errorf("DDL should contain IF NOT EXISTS: %s", sql)
	}
}

func TestSQLite3_DDLIndex(t *testing.T) {
	d := New()
	idx := &md.IndexDef{
		TableSchema: "main", TableName: "users",
		IndexName: "idx_name", ColumnName: "name",
		OrdinalPosition: 1, Uniqueness: "NONUNIQUE",
	}
	sql, err := d.BuildCreateIndex([]*md.IndexDef{idx}, dialect.BuildOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, "CREATE INDEX") {
		t.Error("DDL should contain CREATE INDEX")
	}
	if !strings.Contains(sql, `"idx_name"`) {
		t.Error("DDL should contain index name")
	}
}

func TestSQLite3_DDLUniqueIndex(t *testing.T) {
	d := New()
	idx := &md.IndexDef{
		TableSchema: "main", TableName: "users",
		IndexName: "idx_unique", ColumnName: "email",
		OrdinalPosition: 1, Uniqueness: "UNIQUE",
	}
	sql, err := d.BuildCreateIndex([]*md.IndexDef{idx}, dialect.BuildOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, "CREATE UNIQUE INDEX") {
		t.Errorf("DDL should contain UNIQUE: %s", sql)
	}
}

func TestSQLite3_DDLView(t *testing.T) {
	d := New()
	v := &md.ViewDef{
		ViewSchema:     "main",
		ViewName:       "active_users",
		ViewDefinition: "SELECT * FROM users WHERE active = 1",
	}
	sql, err := d.BuildCreateView(v, dialect.BuildOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, "CREATE VIEW") {
		t.Error("DDL should contain CREATE VIEW")
	}
	if !strings.Contains(sql, "SELECT * FROM users") {
		t.Errorf("DDL should contain view definition: %s", sql)
	}
}

func TestSQLite3_Pagination(t *testing.T) {
	d := New()
	clause := d.BuildPaginationClause(100, 0)
	if !strings.Contains(clause, "LIMIT 100") {
		t.Errorf("pagination should contain LIMIT: %q", clause)
	}
	if !strings.Contains(clause, "OFFSET 0") {
		t.Errorf("pagination should contain OFFSET: %q", clause)
	}
}

func TestSQLite3_SchemaMapping(t *testing.T) {
	d := New()
	tbl, _ := md.NewTableDef("other", "t")
	col, _ := md.NewColumnDef("other", "t", "c", 1, "INTEGER")
	tbl.AddColumn(col)

	sql, err := d.BuildCreateTable(tbl, dialect.BuildOptions{
		SchemaMapping: map[string]string{"other": "main"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, `"main"."t"`) {
		t.Errorf("DDL should use mapped schema: %s", sql)
	}
}

func TestSQLite3_NoQuote(t *testing.T) {
	d := New()
	tbl, _ := md.NewTableDef("main", "t")
	col, _ := md.NewColumnDef("main", "t", "c", 1, "INTEGER")
	tbl.AddColumn(col)

	sql, err := d.BuildCreateTable(tbl, dialect.BuildOptions{NoQuoteIdentifiers: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(sql, `"`) {
		t.Errorf("no-quote mode should not contain quotes: %s", sql)
	}
}
