package cmd

import (
	"context"
	"regexp"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

func empTable(t *testing.T) *md.TableDef {
	t.Helper()
	tbl, err := md.NewTableDef("SCOTT", "EMP")
	if err != nil {
		t.Fatalf("new table: %v", err)
	}
	empno := mustCol(t, "SCOTT", "EMP", "EMPNO", 1, "NUMBER")
	empno.DataPrecision, empno.DataScale, empno.Nullable = 4, 0, "NO"
	ename := mustCol(t, "SCOTT", "EMP", "ENAME", 2, "VARCHAR2")
	ename.DataLength = 10
	sal := mustCol(t, "SCOTT", "EMP", "SAL", 3, "NUMBER")
	sal.DataPrecision, sal.DataScale = 7, 2
	_ = tbl.AddColumn(empno)
	_ = tbl.AddColumn(ename)
	_ = tbl.AddColumn(sal)
	tbl.AddPrimaryKey("PK_EMP", "EMPNO")
	return tbl
}

func TestQualifyColumnType(t *testing.T) {
	tests := []struct {
		name string
		col  func() *md.ColumnDef
		want string
	}{
		{"varchar with length", func() *md.ColumnDef {
			c := mustCol(t, "s", "t", "c", 1, "VARCHAR2")
			c.DataLength = 10
			return c
		}, "VARCHAR2(10)"},
		{"number with precision only", func() *md.ColumnDef {
			c := mustCol(t, "s", "t", "c", 1, "NUMBER")
			c.DataPrecision = 4
			return c
		}, "NUMBER(4)"},
		{"number with precision and scale", func() *md.ColumnDef {
			c := mustCol(t, "s", "t", "c", 1, "DECIMAL")
			c.DataPrecision, c.DataScale = 7, 2
			return c
		}, "DECIMAL(7,2)"},
		{"type already qualified passes through", func() *md.ColumnDef {
			return mustCol(t, "s", "t", "c", 1, "VARCHAR(100)")
		}, "VARCHAR(100)"},
		{"bare type without length stays bare", func() *md.ColumnDef {
			return mustCol(t, "s", "t", "c", 1, "BLOB")
		}, "BLOB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := qualifyColumnType(tt.col(), dialect.BuildOptions{}); got != tt.want {
				t.Errorf("qualifyColumnType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildCreateTableViaDialect_CrossDialect(t *testing.T) {
	cfg := &config.Config{}
	cfg.Target.Type = "postgres"
	cfg.Source.Type = "oracle"
	cfg.DDL.SchemaMapping = map[string]string{"SCOTT": "public"}

	sql, err := buildCreateTableViaDialect(empTable(t), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, `"public"."EMP"`) {
		t.Errorf("schema mapping not applied:\n%s", sql)
	}
	if !strings.Contains(sql, `"EMPNO" SMALLINT NOT NULL`) {
		t.Errorf("NUMBER(4,0) should map to SMALLINT:\n%s", sql)
	}
	if !strings.Contains(sql, `"ENAME" VARCHAR(10)`) {
		t.Errorf("VARCHAR2(10) should map to VARCHAR(10):\n%s", sql)
	}
	if !strings.Contains(sql, `"SAL" NUMERIC(7,2)`) {
		t.Errorf("NUMBER(7,2) should map to NUMERIC(7,2):\n%s", sql)
	}
}

func TestBuildCreateTableViaDialect_SameDialectQualifies(t *testing.T) {
	cfg := &config.Config{}
	cfg.Target.Type = "mysql"
	cfg.Source.Type = "mysql"

	sql, err := buildCreateTableViaDialect(empTable(t), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, "`ENAME` VARCHAR2(10)") {
		t.Errorf("same-dialect should keep source type with length:\n%s", sql)
	}
}

func TestBuildCreateTableViaDialect_TypeOverrideWins(t *testing.T) {
	cfg := &config.Config{}
	cfg.Target.Type = "postgres"
	cfg.Source.Type = "oracle"
	cfg.DDL.TypeOverrides = map[string]string{"NUMBER": "NUMERIC(%p,%s)"}

	sql, err := buildCreateTableViaDialect(empTable(t), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, `"EMPNO" NUMERIC(4,0)`) {
		t.Errorf("type override must take precedence over logical mapping:\n%s", sql)
	}
}

func TestBuildCreateTableViaDialect_SourceDialectConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.Target.Type = "postgres"
	cfg.DDL.SourceDialect = "oracle"

	sql, err := buildCreateTableViaDialect(empTable(t), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sql, `"ENAME" VARCHAR(10)`) {
		t.Errorf("source_dialect should drive conversion:\n%s", sql)
	}
}

func TestConvertSchemaModelForDDL(t *testing.T) {
	// Build a live-extraction style model: bare data_type with length/precision
	// in separate fields (what information_schema returns).
	tbl, _ := md.NewTableDef("test", "t")
	name, _ := md.NewColumnDef("test", "t", "name", 1, "VARCHAR")
	name.DataLength = 50
	tbl.AddColumn(name)
	sal, _ := md.NewColumnDef("test", "t", "sal", 2, "DECIMAL")
	sal.DataPrecision, sal.DataScale = 12, 2
	tbl.AddColumn(sal)
	txt, _ := md.NewColumnDef("test", "t", "note", 3, "TEXT")
	tbl.AddColumn(txt)
	sm := md.NewSchemaModel()
	_ = sm.AddTable(tbl)

	t.Run("same dialect qualifies", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Source.Type = "oceanbase-mysql"
		cfg.DDL.TargetDialect = "oceanbase-mysql"
		tgt, _ := registry.Get("oceanbase-mysql")
		out := convertSchemaModelForDDL(sm, cfg, tgt, dialect.BuildOptions{})
		if out == sm {
			t.Fatal("expected a converted model")
		}
		got := out.GetTables()[0].GetColumns()
		if got[0].DataType != "VARCHAR(50)" {
			t.Errorf("name type = %q, want VARCHAR(50)", got[0].DataType)
		}
		if got[1].DataType != "DECIMAL(12,2)" {
			t.Errorf("sal type = %q, want DECIMAL(12,2)", got[1].DataType)
		}
		if got[2].DataType != "TEXT" {
			t.Errorf("note type = %q, want TEXT", got[2].DataType)
		}
	})

	t.Run("cross dialect converts via LogicalType IR", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Source.Type = "mysql"
		cfg.DDL.TargetDialect = "postgres"
		tgt, _ := registry.Get("postgres")
		out := convertSchemaModelForDDL(sm, cfg, tgt, dialect.BuildOptions{})
		if out == sm {
			t.Fatal("expected a converted model")
		}
		got := out.GetTables()[0].GetColumns()
		if got[0].DataType != "VARCHAR(50)" {
			t.Errorf("name type = %q, want VARCHAR(50)", got[0].DataType)
		}
		if got[1].DataType != "NUMERIC(12,2)" {
			t.Errorf("sal type = %q, want NUMERIC(12,2)", got[1].DataType)
		}
	})

	t.Run("no source dialect leaves model untouched", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.DDL.TargetDialect = "postgres"
		tgt, _ := registry.Get("postgres")
		if out := convertSchemaModelForDDL(sm, cfg, tgt, dialect.BuildOptions{}); out != sm {
			t.Error("model should be returned unchanged when no source dialect is known")
		}
	})
}

func TestBuildCreateTableViaDialect_SameFamilyPreservesDefaults(t *testing.T) {
	// oceanbase-mysql → mysql are distinct dialect names but the same type
	// family: source types and DEFAULTs must be preserved verbatim, not run
	// through the LogicalType IR (which would drop defaults and widen types).
	cfg := &config.Config{}
	cfg.Target.Type = "mysql"
	cfg.Source.Type = "oceanbase-mysql"

	tbl, _ := md.NewTableDef("test", "t_item")
	code, _ := md.NewColumnDef("test", "t_item", "item_code", 1, "VARCHAR")
	code.DataLength = 32
	code.Nullable = "NO"
	tbl.AddColumn(code)
	price, _ := md.NewColumnDef("test", "t_item", "price", 2, "DECIMAL")
	price.DataPrecision, price.DataScale = 10, 2
	price.Nullable = "NO"
	price.DefaultValue = "0.00"
	tbl.AddColumn(price)
	active, _ := md.NewColumnDef("test", "t_item", "active", 3, "TINYINT")
	active.DataLength = 1
	active.Nullable = "NO"
	active.DefaultValue = "1"
	tbl.AddColumn(active)
	note, _ := md.NewColumnDef("test", "t_item", "note", 4, "TEXT")
	tbl.AddColumn(note)
	tbl.AddIndex(&md.IndexDef{TableSchema: "test", TableName: "t_item", IndexName: "PRIMARY",
		Uniqueness: "UNIQUE", ColumnName: "item_code", OrdinalPosition: 1})

	sql, err := buildCreateTableViaDialect(tbl, cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, want := range []string{
		"`price` DECIMAL(10,2) NOT NULL DEFAULT 0.00",
		"`active` TINYINT NOT NULL DEFAULT 1",
		"`note` TEXT",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("DDL missing %q:\n%s", want, sql)
		}
	}
}

func TestBuildCreateTableViaDialect_UnknownTarget(t *testing.T) {
	cfg := &config.Config{}
	cfg.Target.Type = "nosuchdb"
	if _, err := buildCreateTableViaDialect(empTable(t), cfg); err == nil {
		t.Fatal("expected error for unknown target dialect")
	}
}

func TestTargetTypeFamily(t *testing.T) {
	tests := map[string]string{
		"mysql":            "mysql",
		"MySQL":            "mysql",
		"goldendb":         "mysql",
		"goldendb-mysql":   "mysql",
		"oceanbase-mysql":  "mysql",
		"oracle":           "oracle",
		"oceanbase-oracle": "oracle",
		"goldendb-oracle":  "oracle",
		"postgres":         "postgres",
		"opengaussdb":      "postgres",
		"panweidb-oracle":  "postgres",
		"sqlite3":          "sqlite3",
		"duckdb":           "duckdb",
	}
	for in, want := range tests {
		if got := targetTypeFamily(in); got != want {
			t.Errorf("targetTypeFamily(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAlreadyExistsError(t *testing.T) {
	for _, msg := range []string{
		`pq: relation "emp" already exists`,
		"ORA-00955: name is already used by an existing object",
	} {
		if !alreadyExistsError(errString(msg)) {
			t.Errorf("should detect already-exists: %q", msg)
		}
	}
	if alreadyExistsError(errString("ORA-00942: table or view does not exist")) {
		t.Error("should not match unrelated ORA error")
	}
	if alreadyExistsError(nil) {
		t.Error("nil should be false")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestTableExists_DialectSQL(t *testing.T) {
	tests := []struct {
		dbType string
		want   string
	}{
		{"mysql", `FROM information_schema\.tables WHERE table_schema = \? AND table_name = \?`},
		{"goldendb", `FROM information_schema\.tables WHERE table_schema = \? AND table_name = \?`},
		{"oracle", `FROM all_tables WHERE owner = UPPER\(:1\) AND table_name = UPPER\(:2\)`},
		{"oceanbase-oracle", `FROM all_tables WHERE owner = UPPER\(:1\) AND table_name = UPPER\(:2\)`},
		{"postgres", `FROM information_schema\.tables WHERE table_schema = \$1 AND table_name = \$2`},
		{"opengaussdb", `FROM information_schema\.tables WHERE table_schema = \$1 AND table_name = \$2`},
		{"sqlite3", `FROM sqlite_master WHERE type = 'table' AND name = \?`},
		{"duckdb", `FROM duckdb_tables\(\) WHERE schema_name = \? AND table_name = \?`},
	}
	for _, tt := range tests {
		t.Run(tt.dbType, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			defer db.Close()
			mock.ExpectQuery(tt.want).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			got, err := tableExists(context.Background(), db, tt.dbType, "SCOTT", "EMP", false)
			if err != nil {
				t.Fatalf("tableExists: %v", err)
			}
			if !got {
				t.Errorf("tableExists = false, want true")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("expectations: %v", err)
			}
		})
	}
}

func TestTableExists_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta("information_schema")).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	got, err := tableExists(context.Background(), db, "postgres", "public", "emp", false)
	if err != nil {
		t.Fatalf("tableExists: %v", err)
	}
	if got {
		t.Errorf("tableExists = true, want false")
	}
}

func TestTableExists_OracleWireQmark(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	mock.ExpectQuery(`FROM all_tables WHERE owner = UPPER\(\?\) AND table_name = UPPER\(\?\)`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	got, err := tableExists(context.Background(), db, "oceanbase-oracle", "SCOTT", "EMP", true)
	if err != nil {
		t.Fatalf("tableExists: %v", err)
	}
	if !got {
		t.Errorf("tableExists = false, want true")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}
