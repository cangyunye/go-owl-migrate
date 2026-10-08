package plancheck

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite" // 纯 Go sqlite（驱动名 "sqlite"）：默认 go test 即可对真实库跑探针

	"github.com/cangyunye/go-owl-migrate/internal/config"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// ── 测试辅助 ────────────────────────────────────────────────────────────────

// testDBFile 建一个种好语句的 sqlite 文件并返回路径。文件式（而非句柄式）
// 是因为被测代码会 close 掉自己 open 出来的连接，stub 必须每次调用开新句柄。
func testDBFile(t *testing.T, stmts ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plancheck.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}
	return path
}

// useOpenDB 把 openDB 缝隙替换为 stub，测试结束后恢复原实现。
// CheckSourceTables/CheckTargetDDL 走真实探针 SQL，只是连接由缝隙提供——
// sqlite3 方言的 CGo 驱动需 -tags sqlite3，端到端链路由 cmd/serve 侧的门控
// 测试覆盖，这里锁定的是检查逻辑本身的分支。
func useOpenDB(t *testing.T, fn func(config.DBConfig) (*sql.DB, error)) {
	t.Helper()
	orig := openDB
	openDB = fn
	t.Cleanup(func() { openDB = orig })
}

// useOpenDBFile 注入一个每次调用都新开句柄的 sqlite stub。
func useOpenDBFile(t *testing.T, path string) {
	t.Helper()
	useOpenDB(t, func(config.DBConfig) (*sql.DB, error) { return sql.Open("sqlite", path) })
}

// tbl 构造最小 TableDef——探针只用到 schema/table 两个列。
func tbl(schema, name string) *md.TableDef {
	return &md.TableDef{TableSchema: schema, TableName: name}
}

// testCfg 返回 csv 元数据 + sqlite3 源/目标 + SCOTT 映射的基础配置。
// DSN 会被缝隙忽略，但 Type/DSN 非空是离线预检不放过的前置条件。
func testCfg() *config.Config {
	return &config.Config{
		Metadata: config.MetadataConfig{Type: "csv"},
		Source:   config.DBConfig{Type: "sqlite3", DSN: "file:source.db"},
		Target:   config.DBConfig{Type: "sqlite3", DSN: "file:target.db", Schema: "main"},
		DDL:      config.DDLConfig{SchemaMapping: map[string]string{"SCOTT": "scott"}},
	}
}

// openReadOnlyDB 打开一个只读 sqlite 连接——CREATE 必然失败，用于
// CheckTargetDDL 的"探针表建不出来"分支。
func openReadOnlyDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ro.db")
	seed, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	if _, err := seed.Exec("CREATE TABLE IF NOT EXISTS seed (id int)"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}
	ro, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatalf("open readonly db: %v", err)
	}
	t.Cleanup(func() { ro.Close() })
	return ro
}

func checkByName(r *Report, name string) (Check, bool) {
	for _, c := range r.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

// ── CheckSourceTables ───────────────────────────────────────────────────────

// 元数据来自源库实时抽取时天然一致——即使配了源连接也绝不去探测。
func TestCheckSourceTablesDatabaseMetadataPassesThrough(t *testing.T) {
	cfg := testCfg()
	cfg.Metadata.Type = "database"
	c := CheckSourceTables(context.Background(), cfg, []*md.TableDef{tbl("SCOTT", "EMP")})
	if c.Status != StatusPass {
		t.Errorf("metadata.type=database should pass without probing, got %s: %s", c.Status, c.Detail)
	}
}

// 离线元数据 + 未配源连接：跳过校验但要显式警告，不能静默 pass。
func TestCheckSourceTablesOfflineWithoutSourceWarns(t *testing.T) {
	cfg := testCfg()
	cfg.Source = config.DBConfig{}
	c := CheckSourceTables(context.Background(), cfg, []*md.TableDef{tbl("SCOTT", "EMP")})
	if c.Status != StatusWarn {
		t.Errorf("offline without source should warn, got %s: %s", c.Status, c.Detail)
	}
}

// 源库连不上是 fail（阻断），错误信息要可操作。
func TestCheckSourceTablesConnectFailureFails(t *testing.T) {
	useOpenDB(t, func(config.DBConfig) (*sql.DB, error) { return nil, errors.New("dial tcp refused") })
	c := CheckSourceTables(context.Background(), testCfg(), []*md.TableDef{tbl("SCOTT", "EMP")})
	if c.Status != StatusFail {
		t.Errorf("unreachable source should fail, got %s: %s", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "连接源库失败") || !strings.Contains(c.Detail, "dial tcp refused") {
		t.Errorf("detail should carry the driver error, got: %s", c.Detail)
	}
}

// 全部可读 → pass，detail 带表数；WHERE 1=0 的 ErrNoRows 必须算存在而非报错。
func TestCheckSourceTablesAllReadablePasses(t *testing.T) {
	useOpenDBFile(t, testDBFile(t,
		"CREATE TABLE EMP (id int)",
		"CREATE TABLE DEPT (id int)",
	))
	c := CheckSourceTables(context.Background(), testCfg(),
		[]*md.TableDef{tbl("SCOTT", "EMP"), tbl("SCOTT", "DEPT")})
	if c.Status != StatusPass {
		t.Fatalf("existing tables should pass, got %s: %s", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "2 张表") {
		t.Errorf("detail should report the probed table count, got: %s", c.Detail)
	}
}

// 元数据描述的表在源库上不存在 → fail，detail 逐条给出 schema.table → 驱动错误。
func TestCheckSourceTablesMissingTableFails(t *testing.T) {
	useOpenDBFile(t, testDBFile(t, "CREATE TABLE EMP (id int)"))
	c := CheckSourceTables(context.Background(), testCfg(),
		[]*md.TableDef{tbl("SCOTT", "EMP"), tbl("SCOTT", "GHOST")})
	if c.Status != StatusFail {
		t.Fatalf("missing table should fail, got %s: %s", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "1/2 张表") || !strings.Contains(c.Detail, "SCOTT.GHOST") {
		t.Errorf("detail should name the unreadable table with counts, got: %s", c.Detail)
	}
}

// 错误样例封顶 5 条——3000 表 schema 的失败报告不能被几千行错误淹没。
func TestCheckSourceTablesCapsExamplesAtFive(t *testing.T) {
	useOpenDBFile(t, testDBFile(t))
	var tables []*md.TableDef
	for i := 0; i < 7; i++ {
		tables = append(tables, tbl("SCOTT", fmt.Sprintf("GHOST%d", i)))
	}
	c := CheckSourceTables(context.Background(), testCfg(), tables)
	if c.Status != StatusFail {
		t.Fatalf("all-missing tables should fail, got %s", c.Status)
	}
	if n := strings.Count(c.Detail, " → "); n != 5 {
		t.Errorf("detail should cap examples at 5, got %d: %s", n, c.Detail)
	}
}

// 抽样语义：超过 fullProbeLimit 只探排序后的前 100 张——落在抽样窗口外的
// 缺失表不触发 fail（延迟换完备性的既有取舍），但 detail 必须声明抽查过。
func TestCheckSourceTablesSampling(t *testing.T) {
	t.Run("missing table beyond cutoff passes with sampling note", func(t *testing.T) {
		var stmts []string
		var tables []*md.TableDef
		for i := 1; i <= 100; i++ {
			stmts = append(stmts, fmt.Sprintf("CREATE TABLE t%04d (id int)", i))
		}
		for i := 1; i <= 101; i++ { // t0101 不存在，但排序在窗口外
			tables = append(tables, tbl("SCOTT", fmt.Sprintf("t%04d", i)))
		}
		useOpenDBFile(t, testDBFile(t, stmts...))

		c := CheckSourceTables(context.Background(), testCfg(), tables)
		if c.Status != StatusPass {
			t.Fatalf("out-of-window missing table should not fail, got %s: %s", c.Status, c.Detail)
		}
		if !strings.Contains(c.Detail, "抽查前 100") {
			t.Errorf("pass detail must disclose the sampling, got: %s", c.Detail)
		}
	})

	t.Run("missing table inside cutoff fails with sampling note", func(t *testing.T) {
		var stmts []string
		var tables []*md.TableDef
		for i := 2; i <= 101; i++ { // t0001 缺失，落在前 100 的抽样窗口内
			stmts = append(stmts, fmt.Sprintf("CREATE TABLE t%04d (id int)", i))
		}
		for i := 1; i <= 101; i++ {
			tables = append(tables, tbl("SCOTT", fmt.Sprintf("t%04d", i)))
		}
		useOpenDBFile(t, testDBFile(t, stmts...))

		c := CheckSourceTables(context.Background(), testCfg(), tables)
		if c.Status != StatusFail {
			t.Fatalf("in-window missing table should fail, got %s: %s", c.Status, c.Detail)
		}
		if !strings.Contains(c.Detail, "SCOTT.t0001") || !strings.Contains(c.Detail, "抽查前 100") {
			t.Errorf("fail detail should name the table and disclose sampling, got: %s", c.Detail)
		}
	})
}

// ── CheckMappingCoverage ────────────────────────────────────────────────────

func TestCheckMappingCoverage(t *testing.T) {
	t.Run("no target configured passes", func(t *testing.T) {
		cfg := testCfg()
		cfg.Target = config.DBConfig{}
		c := CheckMappingCoverage(cfg, []*md.TableDef{tbl("SCOTT", "EMP")})
		if c.Status != StatusPass {
			t.Errorf("export/offline scenario should pass, got %s: %s", c.Status, c.Detail)
		}
	})

	t.Run("empty metadata passes", func(t *testing.T) {
		c := CheckMappingCoverage(testCfg(), nil)
		if c.Status != StatusPass {
			t.Errorf("no tables should pass, got %s: %s", c.Status, c.Detail)
		}
	})

	t.Run("fully mapped passes", func(t *testing.T) {
		c := CheckMappingCoverage(testCfg(), []*md.TableDef{tbl("SCOTT", "EMP")})
		if c.Status != StatusPass {
			t.Errorf("mapped schema should pass, got %s: %s", c.Status, c.Detail)
		}
	})

	t.Run("unmapped schema warns with suggestion", func(t *testing.T) {
		// 未映射 schema 落原名是 ORA-01031 / schema-not-found 的静默陷阱，
		// warn 文案必须给出可直接粘贴的 schema_mapping 片段。
		c := CheckMappingCoverage(testCfg(), []*md.TableDef{tbl("HR", "EMP")})
		if c.Status != StatusWarn {
			t.Fatalf("unmapped schema should warn, got %s: %s", c.Status, c.Detail)
		}
		if !strings.Contains(c.Detail, "HR") || !strings.Contains(c.Detail, "schema_mapping: {HR:") {
			t.Errorf("detail should name the schema and a copyable mapping, got: %s", c.Detail)
		}
	})

	t.Run("multiple unmapped sorted deterministically", func(t *testing.T) {
		c := CheckMappingCoverage(testCfg(), []*md.TableDef{tbl("SALES", "O"), tbl("HR", "E")})
		if c.Status != StatusWarn {
			t.Fatalf("unmapped schemas should warn, got %s", c.Status)
		}
		if !strings.Contains(c.Detail, "HR, SALES") {
			t.Errorf("unmapped schemas should be sorted, got: %s", c.Detail)
		}
	})
}

// ── CheckTargetDDL ──────────────────────────────────────────────────────────

// 未配目标（导出 / sql-out）直接 pass。
func TestCheckTargetDDLWithoutTargetPasses(t *testing.T) {
	cfg := testCfg()
	cfg.Target = config.DBConfig{}
	c := CheckTargetDDL(context.Background(), cfg, nil)
	if c.Status != StatusPass {
		t.Errorf("no target should pass, got %s: %s", c.Status, c.Detail)
	}
}

// 可建可删 → pass，且探针表必须被清理——残留探针表会污染目标 schema，
// 下一次迁移的表清单里出现 OWL_PLANCHK_* 就是本检查自己制造的脏数据。
func TestCheckTargetDDLPassesAndCleansUp(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "target.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	c := CheckTargetDDL(context.Background(), testCfg(), db)
	if c.Status != StatusPass {
		t.Fatalf("creatable target should pass, got %s: %s", c.Status, c.Detail)
	}
	var n int
	if err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE name LIKE 'OWL_PLANCHK_%'`).Scan(&n); err != nil {
		t.Fatalf("probe leftover query: %v", err)
	}
	if n != 0 {
		t.Errorf("probe table was not cleaned up: %d leftover", n)
	}
}

// 目标账号无 CREATE 权限 → warn（不阻断：预建表 / --skip-ddl 流程合法跳过），
// 但要引用确切的驱动错误，让 Step 4 的失败不再是意外。
func TestCheckTargetDDLCreateFailureWarns(t *testing.T) {
	ro := openReadOnlyDB(t)
	c := CheckTargetDDL(context.Background(), testCfg(), ro)
	if c.Status != StatusWarn {
		t.Fatalf("create failure should warn, got %s: %s", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "试建探针表失败") {
		t.Errorf("detail should explain the probe failure, got: %s", c.Detail)
	}
}

// 目标连不上 → warn 而不是 fail：与连接失败会在建表阶段重试并报错的语义一致。
func TestCheckTargetDDLConnectFailureWarns(t *testing.T) {
	useOpenDB(t, func(config.DBConfig) (*sql.DB, error) { return nil, errors.New("dial tcp refused") })
	c := CheckTargetDDL(context.Background(), testCfg(), nil)
	if c.Status != StatusWarn {
		t.Errorf("unreachable target should warn, got %s: %s", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "连接目标库失败") {
		t.Errorf("detail should explain, got: %s", c.Detail)
	}
}

// ── Report 与 Run 组合 ──────────────────────────────────────────────────────

// OK()/Failures() 的契约：任何 fail 即整体不通过；Failures() 只渲染 fail 行，
// warn 不该混进阻断信息里。
func TestReportOKAndFailures(t *testing.T) {
	allPass := &Report{Checks: []Check{
		{Name: "a", Status: StatusPass},
		{Name: "b", Status: StatusWarn, Detail: "advice"},
	}}
	if !allPass.OK() {
		t.Error("warn must not block")
	}
	if got := allPass.Failures(); got != "" {
		t.Errorf("warn must not appear in failures, got %q", got)
	}

	withFail := &Report{Checks: []Check{
		{Name: "a", Status: StatusPass},
		{Name: "b", Status: StatusFail, Detail: "boom"},
		{Name: "c", Status: StatusWarn, Detail: "advice"},
	}}
	if withFail.OK() {
		t.Error("any fail must block")
	}
	want := "  ✗ b: boom"
	if got := withFail.Failures(); got != want {
		t.Errorf("Failures() = %q, want %q", got, want)
	}
}

// Run 组合契约：SkipTarget 省略目标侧检查（导出 / --sql-out 场景）；
// TruncateWarn 原样进入 Warns；全绿报告 OK。
func TestRunComposition(t *testing.T) {
	useOpenDBFile(t, testDBFile(t, "CREATE TABLE EMP (id int)"))
	matched := []*md.TableDef{tbl("SCOTT", "EMP")}

	t.Run("skip target omits target checks", func(t *testing.T) {
		r := Run(context.Background(), testCfg(), matched, Options{SkipTarget: true})
		if len(r.Checks) != 2 {
			t.Fatalf("SkipTarget should run 2 checks, got %d", len(r.Checks))
		}
		if _, ok := checkByName(r, "目标建表权限"); ok {
			t.Error("SkipTarget must not run the target DDL probe")
		}
		if !r.OK() {
			t.Errorf("healthy source should pass: %+v", r.Checks)
		}
	})

	t.Run("truncate warning is appended verbatim", func(t *testing.T) {
		r := Run(context.Background(), testCfg(), matched,
			Options{TruncateWarn: "目标表将在导入前执行 TRUNCATE"})
		if len(r.Warns) != 1 || r.Warns[0] != "目标表将在导入前执行 TRUNCATE" {
			t.Errorf("TruncateWarn should be appended verbatim, got %v", r.Warns)
		}
	})

	t.Run("full run green on live sqlite", func(t *testing.T) {
		r := Run(context.Background(), testCfg(), matched, Options{})
		if len(r.Checks) != 3 {
			t.Fatalf("full run should have 3 checks, got %d", len(r.Checks))
		}
		if !r.OK() {
			for _, c := range r.Checks {
				t.Errorf("%s: %s — %s", c.Status, c.Name, c.Detail)
			}
		}
	})
}
