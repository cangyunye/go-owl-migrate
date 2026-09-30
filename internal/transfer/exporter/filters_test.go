package exporter

import (
	"context"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"go.uber.org/zap"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func newFilterExporter(t *testing.T, filters map[string]string) (*Exporter, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	e := New(db, Config{
		OutputDir:    t.TempDir(),
		PageSize:     100,
		Filters:      filters,
		FiltersCheck: "count",
		DBType:       "postgres",
		Logger:       zap.NewNop(),
	})
	return e, mock
}

func empDef() *md.TableDef {
	return &md.TableDef{TableSchema: "SCOTT", TableName: "EMP"}
}

func TestValidateFilterFragment(t *testing.T) {
	bad := map[string]string{
		"multi-statement":  "a=1; drop table x",
		"line-comment":     "a=1 -- and b",
		"block-comment":    "a=1 /* sneak */",
		"qmark-bind":       "a = ?",
		"numbered-bind":    "a = :1",
		"empty":            "   ",
	}
	for name, f := range bad {
		if err := ValidateFilterFragment(f); err == nil {
			t.Errorf("%s: expected rejection of %q", name, f)
		}
	}
	good := []string{"deptno = 20", "sal > 1000 AND ename LIKE 'A%'", "created >= DATE '2026-01-01'"}
	for _, f := range good {
		if err := ValidateFilterFragment(f); err != nil {
			t.Errorf("rejected valid fragment %q: %v", f, err)
		}
	}
}

func TestResolveFilterSemantics(t *testing.T) {
	filters := map[string]string{
		"SCOTT.EMP":   "exact=1",
		"*.LOG_*":     "logt=1",
		"LOG_*":       "logt=2", // 与 *.LOG_* 对 LOG 表构成歧义
		"SCOTT.*":     "schema=1",
		"EMP":         "bare=1", // 与 SCOTT.EMP 精确键共存：精确优先不参与歧义
	}
	e, _ := newFilterExporter(t, filters)

	if f, err := e.resolveFilter("SCOTT", "EMP"); err != nil || f != "exact=1" {
		t.Errorf("exact must win: %q err=%v", f, err)
	}
	if f, err := e.resolveFilter("HR", "LOG_2026"); err == nil {
		t.Errorf("ambiguous globs must error, got %q", f)
	}
	if f, err := e.resolveFilter("HR", "EMP"); err != nil || f != "bare=1" {
		t.Errorf("bare glob on other schema: %q err=%v", f, err)
	}
	if f, err := e.resolveFilter("HR", "DEPT"); err != nil || f != "" {
		t.Errorf("no match → empty: %q err=%v", f, err)
	}
	// LOG_*（裸表名）与 *.LOG_*（全名）同时命中 OTHER.LOG_2026 → 歧义报错
	if _, err := e.resolveFilter("OTHER", "LOG_2026"); err == nil {
		t.Error("LOG_* (bare) and *.LOG_* both hit OTHER.LOG_2026 → must be ambiguous")
	}
	// 单一 glob 命中则正常返回
	e2, _ := newFilterExporter(t, map[string]string{"*.LOG_*": "logt=1"})
	if f, err := e2.resolveFilter("OTHER", "LOG_2026"); err != nil || f != "logt=1" {
		t.Errorf("single glob: %q err=%v", f, err)
	}
}

func TestBuildBatchQueryWithWhere(t *testing.T) {
	cases := []struct {
		name  string
		pks   []string
		where string
		cursor bool
		offset int
		want  string
	}{
		{"no-pk + where", nil, "deptno = 20", false, 0,
			`SELECT "A" FROM "SCOTT"."EMP" WHERE deptno = 20 LIMIT 100`},
		{"single pk first page + where", []string{"A"}, "deptno = 20", false, 0,
			`SELECT "A" FROM "SCOTT"."EMP" WHERE deptno = 20 ORDER BY "A" LIMIT 100`},
		{"single pk cursor + where", []string{"A"}, "deptno = 20", true, 0,
			`SELECT "A" FROM "SCOTT"."EMP" WHERE deptno = 20 AND "A" > $1 ORDER BY "A" LIMIT 100`},
		{"single pk cursor no where", []string{"A"}, "", true, 0,
			`SELECT "A" FROM "SCOTT"."EMP" WHERE "A" > $1 ORDER BY "A" LIMIT 100`},
		{"no where no pk", nil, "", false, 0,
			`SELECT "A" FROM "SCOTT"."EMP" LIMIT 100`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := newFilterExporter(t, nil)
			got := e.buildBatchQuery(empDef(), []string{`"A"`}, []string{`"A"`}, tc.pks, tc.cursor, tc.offset, tc.where)
			if got != tc.want {
				t.Errorf("\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

func TestBuildOracleLegacyWithWhere(t *testing.T) {
	e, _ := newFilterExporter(t, nil)
	e.cfg.DBType = "oracle"
	e.oracleLegacy = true

	got := e.buildOracleLegacyQuery(`"A"`, `"SCOTT"."EMP"`, nil, []string{`"A"`}, []string{"A"}, true, 0, "deptno = 20")
	if !strings.Contains(got, `WHERE deptno = 20 AND "A" > :1 ORDER BY`) {
		t.Errorf("legacy cursor must fold filter into inner WHERE: %s", got)
	}
	got = e.buildOracleLegacyQuery(`"A"`, `"SCOTT"."EMP"`, nil, nil, nil, false, 0, "deptno = 20")
	if !strings.Contains(got, `FROM (SELECT "A" FROM "SCOTT"."EMP" WHERE deptno = 20) owl_pg__`) &&
		!strings.Contains(got, `SELECT "A" FROM "SCOTT"."EMP" WHERE deptno = 20`) {
		t.Errorf("legacy no-pk must inject into inner query: %s", got)
	}
}

func TestValidateFiltersGate(t *testing.T) {
	e, mock := newFilterExporter(t, map[string]string{"SCOTT.EMP": "deptno = 20"})
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM "SCOTT"\."EMP" WHERE deptno = 20`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(42))

	if err := e.ValidateFilters(context.Background(), []*md.TableDef{empDef()}); err != nil {
		t.Fatalf("ValidateFilters: %v", err)
	}
	if got := e.filterCountFor("SCOTT", "EMP"); got != 42 {
		t.Errorf("filterCountFor = %d, want 42", got)
	}

	// 数据库报错 → 门禁失败并携带表名与条件
	e2, mock2 := newFilterExporter(t, map[string]string{"SCOTT.EMP": "badcol = 1"})
	mock2.ExpectQuery(`SELECT COUNT\(\*\) FROM "SCOTT"\."EMP" WHERE badcol = 1`).
		WillReturnError(errORA00904)
	err := e2.ValidateFilters(context.Background(), []*md.TableDef{empDef()})
	if err == nil || !strings.Contains(err.Error(), "SCOTT.EMP") || !strings.Contains(err.Error(), "badcol") {
		t.Errorf("gate error must name table+filter: %v", err)
	}

	// off → 不发查询、无计数
	e3, mock3 := newFilterExporter(t, map[string]string{"SCOTT.EMP": "deptno = 20"})
	e3.cfg.FiltersCheck = "off"
	mock3.ExpectQuery(`SELECT COUNT`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	if err := e3.ValidateFilters(context.Background(), []*md.TableDef{empDef()}); err != nil {
		t.Fatalf("off must skip gate: %v", err)
	}
	if n := e3.filterCountFor("SCOTT", "EMP"); n != 0 {
		t.Errorf("off mode must not record counts, got %d", n)
	}
}

// errORA00904 mimics an invalid-identifier error from the database.
var errORA00904 = errString("ORA-00904: \"BADCOL\": invalid identifier")

type errString string

func (e errString) Error() string { return string(e) }

func TestAlignColumnsToDef(t *testing.T) {
	e, _ := newFilterExporter(t, nil)
	e.cfg.ColumnRenames = map[string]map[string]string{"OWL_SRC.EMP": {"SAL": "salary"}}
	def := &md.TableDef{
		TableSchema: "OWL_SRC", TableName: "EMP",
		Columns: []*md.ColumnDef{
			{ColumnName: "empno"},
			{ColumnName: "salary"}, // 改名后的输出名（DB 里仍是 SAL）
			{ColumnName: "ename"},
		},
	}
	dbCols := []ColumnInfo{
		{Name: "EMPNO", TypeName: "NUMBER"},
		{Name: "ENAME", TypeName: "VARCHAR2"},
		{Name: "SAL", TypeName: "NUMBER"},
		{Name: "HIREDATE", TypeName: "DATE"},
	}
	cols, sqlNames, err := e.alignColumnsToDef(def, dbCols)
	if err != nil {
		t.Fatalf("alignColumnsToDef: %v", err)
	}
	// 输出头 = def 名（含改名），SQL 名 = DB 名（源名），顺序 = def 顺序
	wantOut := []string{"empno", "salary", "ename"}
	wantSQL := []string{"EMPNO", "SAL", "ENAME"}
	for i := range wantOut {
		if cols[i].Name != wantOut[i] {
			t.Errorf("out[%d] = %s, want %s", i, cols[i].Name, wantOut[i])
		}
		if sqlNames[i] != wantSQL[i] {
			t.Errorf("sql[%d] = %s, want %s", i, sqlNames[i], wantSQL[i])
		}
	}
	if cols[1].TypeName != "NUMBER" {
		t.Errorf("type not carried: %+v", cols[1])
	}

	// def 列在 DB 中不存在 → 明确报错
	bad := &md.TableDef{TableSchema: "S", TableName: "T", Columns: []*md.ColumnDef{{ColumnName: "GHOST"}}}
	if _, _, err := e.alignColumnsToDef(bad, dbCols); err == nil || !strings.Contains(err.Error(), "GHOST") {
		t.Errorf("missing column must error with name: %v", err)
	}

	// def 无列 → 原样透传
	pass, names, err := e.alignColumnsToDef(&md.TableDef{TableSchema: "S", TableName: "T"}, dbCols)
	if err != nil || len(pass) != 4 || names[0] != "EMPNO" {
		t.Errorf("passthrough broken: %v %v %v", pass, names, err)
	}
}
