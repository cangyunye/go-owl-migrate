# 生成历史记录实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 生成结果（metadata/ddl/select/insert/export-offline）落库来源、时间、明细，页面可列历史、按次浏览/下载，双限制（条数 10 + 7 天）保留并定时清理。

**Architecture:** 扩展 `generation_outputs` 表（补 3 列，沿用 `addNodeIDColumns` 的 ALTER 迁移模式）；store 层提供 Record/Prune/List/Get 四方法；serve 包新增 `generations.go`（policy 常量、sourceLabel 解析、两个只读端点、清理循环）；`handleDownloadGen` 加 `?id=`；前端三处视图加历史面板。

**Tech Stack:** Go 1.25（net/http ServeMux 方法路由、modernc sqlite）、原生 JS ES modules（无框架）。

## Global Constraints

- `genOutputKeep = 10`；`genOutputMaxAge = 7 * 24 * time.Hour`；kinds = `{metadata, ddl, select, insert, export-offline}`。
- 完整 DSN 绝不下库；`source_label` 必须剥掉密码。
- `?token=` 仅放行 `*/download` 路由（已实现的 withAuth 改动，勿回退）。
- SQLite 沿用现有 SELECT→DELETE 模式，不用 RETURNING。
- 前端静态资源 `go:embed` 进二进制，改动后需重建。
- 每个任务跑 `go build ./... && go test ./internal/service/ ./internal/server/serve/`，通过后提交。

---
## 文件结构

- `internal/service/job.go` — 迁移 + GenerationMeta/GenerationRecord + Record/Prune/List/Get
- `internal/service/job_test.go` — store 测试
- `internal/server/serve/generations.go` — 新文件：policy 常量、sourceLabel/sourceMetaFrom/dirStats、handleListGenerations/handleGenerationFiles、pruneAllGenerations、CleanupLoop
- `internal/server/serve/generations_test.go` — 新文件：sourceLabel 单测 + 端点测试
- `internal/server/serve/generate.go` — recordGenOutput 签名、handleDownloadGen `?id=`
- `internal/server/serve/exportmetadata.go` / `exportoffline.go` / `generate.go` — 5 处调用点传 meta
- `internal/server/serve/server.go` — 路由注册、NewServer 启动 prune
- `internal/server/serve/newhandlers_test.go` — RecordGeneration 直呼点更新
- `internal/cmd/serve.go` — `go srv.CleanupLoop(ctx)`
- `web/static/ui/views/exportMetadata.js` / `generator.js` / `export.js` — 历史面板
- `web/static/js/app.js` — 已含 `downloadURL`（Task 前置已完成）

---

### Task 1: Store 迁移 + 四方法

**Files:**
- Modify: `internal/service/job.go`
- Test: `internal/service/job_test.go`

**Interfaces:**
- Produces:
  ```go
  type GenerationMeta struct {
      SourceLabel    string         `json:"source_label"`
      DatasourceName string         `json:"datasource_name,omitempty"`
      Detail         map[string]any `json:"detail"`
  }
  type GenerationRecord struct {
      ID             int64          `json:"id"`
      Kind           string         `json:"kind"`
      Dir            string         `json:"dir"`
      CreatedAt      string         `json:"created_at"`
      SourceLabel    string         `json:"source_label"`
      DatasourceName string         `json:"datasource_name,omitempty"`
      Detail         map[string]any `json:"detail"`
  }
  func (s *JobStore) RecordGeneration(kind, dir string, meta GenerationMeta) error
  func (s *JobStore) PruneGenerations(kind string, keep int, maxAge time.Duration) ([]string, error)
  func (s *JobStore) ListGenerations(kind string) ([]GenerationRecord, error)
  func (s *JobStore) GetGeneration(id int64) (GenerationRecord, error)
  ```
  注：RecordGeneration 不再接收 keep、不再内部 prune（接口精化：recordGenOutput 组合 Prune，职责单一；spec 的"记录后顺带 prune"由组合保持）。

- [ ] **Step 1: 写失败测试**（`internal/service/job_test.go` 追加）
  现有 imports 为 `database/sql, fmt, path/filepath, sync, testing`；新测试需补 `errors`、`slices`、`time`。

```go
func TestJobStore_GenerationOutputsUpgrade(t *testing.T) {
	// 旧库无新列 → NewJobStore 迁移补列
	dbPath := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE generation_outputs (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		kind       TEXT NOT NULL,
		dir        TEXT NOT NULL,
		created_at TEXT DEFAULT (datetime('now'))
	)`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	store, err := NewJobStore(dbPath)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	defer store.Close()

	var cols []string
	rows, err := store.db.Query(`SELECT name FROM pragma_table_info('generation_outputs')`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	rows.Close()
	for _, want := range []string{"source_label", "datasource_name", "detail"} {
		if !slices.Contains(cols, want) {
			t.Errorf("migrated table missing column %q (got %v)", want, cols)
		}
	}
}

func TestJobStore_GenerationMetaRoundTrip(t *testing.T) {
	store, err := NewJobStore(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	defer store.Close()

	meta := GenerationMeta{
		SourceLabel:    "mysql@127.0.0.1:3306/SCOTT",
		DatasourceName: "prod",
		Detail:         map[string]any{"format": "csv", "table_count": float64(3), "file_count": float64(9)},
	}
	if err := store.RecordGeneration("metadata", "/tmp/meta-1", meta); err != nil {
		t.Fatalf("RecordGeneration: %v", err)
	}

	recs, err := store.ListGenerations("metadata")
	if err != nil {
		t.Fatalf("ListGenerations: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("len = %d, want 1", len(recs))
	}
	r := recs[0]
	if r.SourceLabel != meta.SourceLabel || r.DatasourceName != meta.DatasourceName {
		t.Errorf("labels = %q/%q, want %q/%q", r.SourceLabel, r.DatasourceName, meta.SourceLabel, meta.DatasourceName)
	}
	if r.Detail["format"] != "csv" {
		t.Errorf("detail.format = %v, want csv", r.Detail["format"])
	}

	got, err := store.GetGeneration(r.ID)
	if err != nil {
		t.Fatalf("GetGeneration: %v", err)
	}
	if got.Dir != "/tmp/meta-1" || got.Kind != "metadata" {
		t.Errorf("GetGeneration = %+v", got)
	}
	if _, err := store.GetGeneration(9999); !errors.Is(err, ErrNoGeneration) {
		t.Errorf("GetGeneration(9999) err = %v, want ErrNoGeneration", err)
	}
}

func TestJobStore_GenerationPruneAgeAndCount(t *testing.T) {
	store, err := NewJobStore(filepath.Join(t.TempDir(), "prune.db"))
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	defer store.Close()

	// 3 条同 kind；keep=2 → 删最旧 1 条
	for i := 1; i <= 3; i++ {
		if err := store.RecordGeneration("ddl", fmt.Sprintf("/tmp/gen-%d", i), GenerationMeta{}); err != nil {
			t.Fatal(err)
		}
	}
	pruned, err := store.PruneGenerations("ddl", 2, 24*time.Hour)
	if err != nil {
		t.Fatalf("PruneGenerations: %v", err)
	}
	if len(pruned) != 1 || pruned[0] != "/tmp/gen-1" {
		t.Fatalf("count prune = %v, want [/tmp/gen-1]", pruned)
	}

	// 手工把剩余某条改老 → 年龄限制触发
	if _, err := store.db.Exec(`UPDATE generation_outputs SET created_at = '2000-01-01 00:00:00' WHERE dir = '/tmp/gen-2'`); err != nil {
		t.Fatal(err)
	}
	pruned, err = store.PruneGenerations("ddl", 10, 24*time.Hour)
	if err != nil {
		t.Fatalf("PruneGenerations age: %v", err)
	}
	if len(pruned) != 1 || pruned[0] != "/tmp/gen-2" {
		t.Fatalf("age prune = %v, want [/tmp/gen-2]", pruned)
	}

	recs, _ := store.ListGenerations("ddl")
	if len(recs) != 1 || recs[0].Dir != "/tmp/gen-3" {
		t.Errorf("after prune recs = %+v, want only /tmp/gen-3", recs)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/ -run 'TestJobStore_Generation' -count=1`
Expected: 编译失败（`RecordGeneration` 签名不符、方法不存在）。

- [ ] **Step 3: 实现**（`internal/service/job.go`）

3a. `migrate()` 中 `generation_outputs` 建表加列，并在 `addNodeIDColumns` 后追加调用（`job.go` 现有 imports `database/sql, errors, fmt, time` 需补 `encoding/json`）：

```go
CREATE TABLE IF NOT EXISTS generation_outputs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    kind            TEXT NOT NULL,
    dir             TEXT NOT NULL,
    created_at      TEXT DEFAULT (datetime('now')),
    source_label    TEXT NOT NULL DEFAULT '',
    datasource_name TEXT NOT NULL DEFAULT '',
    detail          TEXT NOT NULL DEFAULT '{}'
);
```

```go
	if err := s.addGenOutputColumns(); err != nil {
		return err
	}
```

3b. 新迁移方法（仿 `addNodeIDColumns`）：

```go
// addGenOutputColumns backfills the generation-history seam into databases
// created before the columns existed.
func (s *JobStore) addGenOutputColumns() error {
	for _, col := range []string{"source_label", "datasource_name", "detail"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('generation_outputs') WHERE name = ?`, col).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := s.db.Exec(fmt.Sprintf(`ALTER TABLE generation_outputs ADD COLUMN %s TEXT NOT NULL DEFAULT ''`, col)); err != nil {
				return err
			}
		}
	}
	return nil
}
```

3c. 类型（放在 `JobStore` 定义附近）：

```go
// GenerationMeta carries the display metadata recorded with a generation
// output. The full DSN is never stored — only a password-free label.
type GenerationMeta struct {
	SourceLabel    string         `json:"source_label"`
	DatasourceName string         `json:"datasource_name,omitempty"`
	Detail         map[string]any `json:"detail"`
}

// GenerationRecord is a persisted generation output row.
type GenerationRecord struct {
	ID             int64          `json:"id"`
	Kind           string         `json:"kind"`
	Dir            string         `json:"dir"`
	CreatedAt      string         `json:"created_at"`
	SourceLabel    string         `json:"source_label"`
	DatasourceName string         `json:"datasource_name,omitempty"`
	Detail         map[string]any `json:"detail"`
}
```

3d. 替换现有 `RecordGeneration` 及新增三方法（删除原 `RecordGeneration(kind, dir, keep)` 与其中内联 prune）：

```go
// RecordGeneration stores a generation output record.
func (s *JobStore) RecordGeneration(kind, dir string, meta GenerationMeta) error {
	detail := "{}"
	if meta.Detail != nil {
		if b, err := json.Marshal(meta.Detail); err == nil {
			detail = string(b)
		}
	}
	_, err := s.db.Exec(
		`INSERT INTO generation_outputs (kind, dir, source_label, datasource_name, detail)
		 VALUES (?, ?, ?, ?, ?)`,
		kind, dir, meta.SourceLabel, meta.DatasourceName, detail,
	)
	return err
}

// PruneGenerations removes records beyond keep (by age) or older than maxAge
// for a kind, returning the dirs that were deleted so callers can remove them
// from disk. Both limits apply; either one tripping deletes the row.
func (s *JobStore) PruneGenerations(kind string, keep int, maxAge time.Duration) ([]string, error) {
	cutoff := time.Now().UTC().Add(-maxAge).Format("2006-01-02 15:04:05")
	rows, err := s.db.Query(
		`SELECT id, dir FROM generation_outputs WHERE kind = ?
		 AND (id NOT IN (SELECT id FROM generation_outputs WHERE kind = ? ORDER BY id DESC LIMIT ?)
		      OR created_at < ?)`,
		kind, kind, keep, cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type stale struct {
		id  int64
		dir string
	}
	var stales []stale
	for rows.Next() {
		var p stale
		if err := rows.Scan(&p.id, &p.dir); err != nil {
			return nil, err
		}
		stales = append(stales, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	dirs := make([]string, 0, len(stales))
	for _, p := range stales {
		if _, err := s.db.Exec(`DELETE FROM generation_outputs WHERE id = ?`, p.id); err != nil {
			return dirs, err
		}
		dirs = append(dirs, p.dir)
	}
	return dirs, nil
}

// ListGenerations returns generation records for a kind, newest first.
func (s *JobStore) ListGenerations(kind string) ([]GenerationRecord, error) {
	rows, err := s.db.Query(
		`SELECT id, kind, dir, created_at, source_label, datasource_name, detail
		 FROM generation_outputs WHERE kind = ? ORDER BY id DESC`, kind,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var recs []GenerationRecord
	for rows.Next() {
		var r GenerationRecord
		var detail string
		if err := rows.Scan(&r.ID, &r.Kind, &r.Dir, &r.CreatedAt, &r.SourceLabel, &r.DatasourceName, &detail); err != nil {
			return nil, err
		}
		if detail != "" {
			_ = json.Unmarshal([]byte(detail), &r.Detail)
		}
		recs = append(recs, r)
	}
	return recs, rows.Err()
}

// GetGeneration returns one generation record by id.
func (s *JobStore) GetGeneration(id int64) (GenerationRecord, error) {
	var r GenerationRecord
	var detail string
	err := s.db.QueryRow(
		`SELECT id, kind, dir, created_at, source_label, datasource_name, detail
		 FROM generation_outputs WHERE id = ?`, id,
	).Scan(&r.ID, &r.Kind, &r.Dir, &r.CreatedAt, &r.SourceLabel, &r.DatasourceName, &detail)
	if err == sql.ErrNoRows {
		return r, fmt.Errorf("%w: generation %d", ErrNoGeneration, id)
	}
	if err != nil {
		return r, err
	}
	if detail != "" {
		_ = json.Unmarshal([]byte(detail), &r.Detail)
	}
	return r, nil
}
```

3e. 更新旧测试 `TestJobStore_GenerationRetention`（`internal/service/job_test.go`），把"RecordGeneration 内部 prune"改为"Record 后显式 Prune"：

```go
func TestJobStore_GenerationRetention(t *testing.T) {
	store, err := NewJobStore(filepath.Join(t.TempDir(), "gen.db"))
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	defer store.Close()

	for i := 1; i <= 3; i++ {
		if err := store.RecordGeneration("ddl", fmt.Sprintf("/tmp/gen-%d", i), GenerationMeta{}); err != nil {
			t.Fatalf("RecordGeneration(%d): %v", i, err)
		}
	}
	pruned, err := store.PruneGenerations("ddl", 2, 24*time.Hour)
	if err != nil {
		t.Fatalf("PruneGenerations: %v", err)
	}
	if len(pruned) != 1 || pruned[0] != "/tmp/gen-1" {
		t.Fatalf("pruned = %v, want [/tmp/gen-1]", pruned)
	}
	dir, err := store.LatestGeneration("ddl")
	if err != nil {
		t.Fatalf("LatestGeneration: %v", err)
	}
	if dir != "/tmp/gen-3" {
		t.Errorf("LatestGeneration = %q, want /tmp/gen-3", dir)
	}
	if _, err := store.LatestGeneration("insert"); err == nil {
		t.Error("LatestGeneration for unknown kind should error")
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/service/ -run 'TestJobStore_Generation' -count=1`
Expected: PASS（3 个新测试 + 更新的 retention）。

- [ ] **Step 5: 修 serve 测试直呼点**（`internal/server/serve/newhandlers_test.go` `TestDownloadGen_PersistedAcrossRestart`）

```go
	if err := store.RecordGeneration("ddl", outDir, service.GenerationMeta{}); err != nil {
		t.Fatalf("RecordGeneration: %v", err)
	}
```

- [ ] **Step 6: 全量构建 + 提交**

Run: `go build ./... && go test ./internal/service/ ./internal/server/serve/ -count=1`
Expected: PASS。
```bash
git add internal/service/job.go internal/service/job_test.go internal/server/serve/newhandlers_test.go
git commit -m "feat(service): generation history columns, Record/Prune/List/Get"
```

---

### Task 2: sourceLabel 解析 + dirStats + sourceMetaFrom

**Files:**
- Create: `internal/server/serve/generations.go`
- Create: `internal/server/serve/generations_test.go`

**Interfaces:**
- Produces:
  ```go
  const genOutputMaxAge = 7 * 24 * time.Hour
  var genKinds = []string{"metadata", "ddl", "select", "insert", "export-offline"}
  func sourceLabel(srcType, dsn, schema string) string
  func sourceMetaFrom(src config.DBConfig, schema string) service.GenerationMeta
  func dirStats(dir string) (fileCount int, sizeBytes int64)
  ```

- [ ] **Step 1: 写失败测试**（`generations_test.go`）

```go
package serve

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/config"
)

func TestSourceLabel_NoPasswordLeak(t *testing.T) {
	cases := []struct {
		name, srcType, dsn, schema, want string
	}{
		{"url", "mysql", "mysql://root:secret@127.0.0.1:3306/default_db", "default_db", "mysql@127.0.0.1:3306/default_db"},
		{"oracle user/pass", "oracle", "scott/tiger@127.0.0.1:1521/XEPDB1", "SCOTT", "oracle@127.0.0.1:1521/SCOTT"},
		{"pg keyword", "postgres", "host=127.0.0.1 port=5432 user=postgres password=secret dbname=postgres_db sslmode=disable", "public", "postgres@127.0.0.1:5432/public"},
		{"no dsn", "sqlite3", "", "main", "sqlite3/main"},
		{"empty everything", "", "", "", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sourceLabel(tc.srcType, tc.dsn, tc.schema)
			if got != tc.want {
				t.Errorf("sourceLabel = %q, want %q", got, tc.want)
			}
			for _, secret := range []string{"secret", "tiger", "password="} {
				if contains(got, secret) {
					t.Errorf("label %q leaks secret %q", got, secret)
				}
			}
		})
	}
}

func TestSourceMetaFrom_DatasourceRef(t *testing.T) {
	cfg := config.DBConfig{Type: "oracle", DSN: "datasource:prod-ora", Schema: "SCOTT"}
	m := sourceMetaFrom(cfg, cfg.Schema)
	if m.DatasourceName != "prod-ora" {
		t.Errorf("DatasourceName = %q, want prod-ora", m.DatasourceName)
	}
	if m.SourceLabel != "oracle@prod-ora" {
		t.Errorf("SourceLabel = %q, want oracle@prod-ora", m.SourceLabel)
	}
}

func TestDirStats(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.csv"), []byte("hello"), 0644)
	os.WriteFile(filepath.Join(dir, "b.csv"), []byte("world"), 0644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "sub", "c.csv"), []byte("!"), 0644)
	fc, sz := dirStats(dir)
	if fc != 3 {
		t.Errorf("file_count = %d, want 3", fc)
	}
	if sz != 11 {
		t.Errorf("size = %d, want 11", sz)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/server/serve/ -run 'TestSourceLabel|TestSourceMetaFrom|TestDirStats' -count=1`
Expected: 编译失败（符号未定义）。

- [ ] **Step 3: 实现**（`generations.go`；policy 常量在此定义，Task 4 的端点/清理复用）

```go
package serve

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

// genOutputMaxAge is how long a generation output is retained per kind.
const genOutputMaxAge = 7 * 24 * time.Hour

// genKinds is the full set of generation kinds tracked for history/cleanup.
var genKinds = []string{"metadata", "ddl", "select", "insert", "export-offline"}

var (
	reKvHost = regexp.MustCompile(`(?:^|\s)host=(\S+)`)
	reKvPort = regexp.MustCompile(`(?:^|\s)port=(\S+)`)
)

// sourceLabel builds a password-free display label for a generation output:
// <type>@<host[:port]>[/schema]. DSN passwords are never included.
func sourceLabel(srcType, dsn, schema string) string {
	srcType = strings.TrimSpace(srcType)
	host := ""
	if dsn != "" {
		if u, err := url.Parse(dsn); err == nil && u.Scheme != "" && u.Host != "" {
			host = u.Host
		} else if at := strings.LastIndex(dsn, "@"); at >= 0 {
			rest := dsn[at+1:]
			if i := strings.IndexAny(rest, "/"); i >= 0 {
				rest = rest[:i]
			}
			host = rest
		} else if m := reKvHost.FindStringSubmatch(dsn); m != nil {
			host = m[1]
			if p := reKvPort.FindStringSubmatch(dsn); p != nil {
				host += ":" + p[1]
			}
		}
	}
	label := srcType
	if host != "" {
		label = srcType + "@" + host
	}
	if schema != "" {
		label += "/" + schema
	}
	if label == "" {
		label = "unknown"
	}
	return label
}

// sourceMetaFrom derives the GenerationMeta for a source config, honoring the
// datasource:<name> ref form (the label then shows the ref name, never a DSN).
func sourceMetaFrom(src config.DBConfig, schema string) service.GenerationMeta {
	m := service.GenerationMeta{}
	if strings.HasPrefix(src.DSN, "datasource:") {
		name := strings.TrimPrefix(src.DSN, "datasource:")
		m.DatasourceName = name
		m.SourceLabel = src.Type + "@" + name
	} else {
		m.SourceLabel = sourceLabel(src.Type, src.DSN, schema)
	}
	return m
}

// dirStats counts files and total bytes under dir (directories excluded).
func dirStats(dir string) (fileCount int, sizeBytes int64) {
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			fileCount++
			sizeBytes += info.Size()
		}
		return nil
	})
	return fileCount, sizeBytes
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/server/serve/ -run 'TestSourceLabel|TestSourceMetaFrom|TestDirStats' -count=1`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/server/serve/generations.go internal/server/serve/generations_test.go
git commit -m "feat(serve): generation source labels and dir stats helpers"
```

---

### Task 3: recordGenOutput 传 meta + 5 处调用点

**Files:**
- Modify: `internal/server/serve/generate.go`、`exportmetadata.go`、`exportoffline.go`
- Test: `internal/server/serve/generations_test.go`（追加）

**Interfaces:**
- Consumes: Task 1 `service.GenerationMeta`、Task 2 `sourceMetaFrom`
- Produces: `func (s *Server) recordGenOutput(kind, dir string, meta service.GenerationMeta) error`

- [ ] **Step 1: 写失败测试**（`generations_test.go` 追加）
  本文件 imports 在 Task 2 基础上需补 `fmt`、`os`、`path/filepath`、`service`。

```go
func TestRecordGenOutput_PersistsMetaAndPrunes(t *testing.T) {
	store, err := service.NewJobStore(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	srv := NewServer(Config{Store: store})

	dirs := make([]string, 0, genOutputKeep+1)
	for i := 0; i < genOutputKeep+1; i++ {
		d := filepath.Join(t.TempDir(), fmt.Sprintf("out-%d", i))
		os.MkdirAll(d, 0755)
		dirs = append(dirs, d)
	}
	meta := service.GenerationMeta{SourceLabel: "mysql@h:3306/s", Detail: map[string]any{"format": "csv"}}
	for i, d := range dirs {
		if err := srv.recordGenOutput("metadata", d, meta); err != nil {
			t.Fatalf("recordGenOutput(%d): %v", i, err)
		}
	}

	// 最旧目录已被磁盘删除
	if _, err := os.Stat(dirs[0]); !os.IsNotExist(err) {
		t.Errorf("pruned dir %s still exists", dirs[0])
	}
	recs, err := store.ListGenerations("metadata")
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != genOutputKeep {
		t.Errorf("records = %d, want %d", len(recs), genOutputKeep)
	}
	if recs[0].Detail["format"] != "csv" {
		t.Errorf("detail = %v, want csv", recs[0].Detail)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/server/serve/ -run TestRecordGenOutput_PersistsMetaAndPrunes -count=1`
Expected: 编译失败（recordGenOutput 签名不符）。

- [ ] **Step 3: 改 recordGenOutput**（`generate.go`）

```go
// recordGenOutput persists a generation output directory in the job store,
// prunes retired outputs (count + age) from disk, then removes their dirs.
func (s *Server) recordGenOutput(kind, dir string, meta service.GenerationMeta) error {
	if err := s.store.RecordGeneration(kind, dir, meta); err != nil {
		return err
	}
	pruned, err := s.store.PruneGenerations(kind, genOutputKeep, genOutputMaxAge)
	for _, d := range pruned {
		if rmErr := os.RemoveAll(d); rmErr != nil {
			fmt.Fprintf(os.Stderr, "warning: remove pruned generation dir %s: %v\n", d, rmErr)
		}
	}
	return err
}
```

- [ ] **Step 4: 更新 5 处调用点**

4a. `exportmetadata.go`（`s.recordGenOutput("metadata", outDir)` 处，`src`/`format`/`req.Scope`/`tables`/`files` 均在作用域）：

```go
	meta := sourceMetaFrom(src, targetSchema)
	meta.Detail = map[string]any{
		"format":      format,
		"scope":       req.Scope,
		"table_count": len(tables),
		"file_count":  len(files),
	}
	if err := s.recordGenOutput("metadata", outDir, meta); err != nil {
```

4b. `exportoffline.go`（`s.recordGenOutput("export-offline", outDir)` 处，`cfg`/`format`/`dataTables`/`outputFiles` 在作用域）：

```go
	meta := sourceMetaFrom(cfg.Source, cfg.Source.Schema)
	meta.Detail = map[string]any{
		"format":      format,
		"table_count": len(dataTables),
		"file_count":  len(outputFiles),
	}
	if err := s.recordGenOutput("export-offline", outDir, meta); err != nil {
```

4c. `generate.go` handleGenerateDDL（`s.recordGenOutput("ddl", outDir)` 处，`cfg`/`schema`/`all` 在作用域）：

```go
	meta := sourceMetaFrom(cfg.Source, schema)
	meta.Detail = map[string]any{"file_count": len(all)}
	if err := s.recordGenOutput("ddl", outDir, meta); err != nil {
```

4d. `generate.go` handleGenerateSelect（`cfg`/`files` 在作用域）：

```go
	meta := sourceMetaFrom(cfg.Source, cfg.Source.Schema)
	meta.Detail = map[string]any{"file_count": len(files)}
	if err := s.recordGenOutput("select", outDir, meta); err != nil {
```

4e. `generate.go` handleGenerateInsert（`cfg`/`tables`/`files` 在作用域）：

```go
	meta := sourceMetaFrom(cfg.Source, cfg.Source.Schema)
	meta.Detail = map[string]any{"table_count": len(tables), "file_count": len(files)}
	if err := s.recordGenOutput("insert", outDir, meta); err != nil {
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go build ./... && go test ./internal/server/serve/ -run 'TestRecordGenOutput|TestDownloadGen' -count=1`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add internal/server/serve/generate.go internal/server/serve/exportmetadata.go internal/server/serve/exportoffline.go internal/server/serve/generations_test.go
git commit -m "feat(serve): record source/detail metadata on generation outputs"
```

---

### Task 4: 历史端点 + 按 id 下载 + 启动/定时清理

**Files:**
- Modify: `internal/server/serve/generations.go`、`generate.go`、`server.go`
- Modify: `internal/cmd/serve.go`
- Test: `internal/server/serve/generations_test.go`

**Interfaces:**
- Consumes: Task 1 `ListGenerations`/`GetGeneration`/`PruneGenerations`、Task 2 helpers
- Produces:
  ```go
  func (s *Server) handleListGenerations(w http.ResponseWriter, r *http.Request)
  func (s *Server) handleGenerationFiles(w http.ResponseWriter, r *http.Request)
  func (s *Server) pruneAllGenerations()
  func (s *Server) CleanupLoop(ctx context.Context)
  ```

- [ ] **Step 1: 写失败测试**（`generations_test.go` 追加）
  本文件 imports 在 Task 3 基础上需再补 `io`、`strings`、`net/http`、`net/http/httptest`、`encoding/json`。

```go
func TestGenerationsAPI(t *testing.T) {
	store, err := service.NewJobStore(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	outDir := t.TempDir()
	os.WriteFile(filepath.Join(outDir, "tables.csv"), []byte("x"), 0644)
	store.RecordGeneration("metadata", outDir, service.GenerationMeta{
		SourceLabel: "mysql@h:3306/s", Detail: map[string]any{"format": "csv", "file_count": float64(1)},
	})
	store.RecordGeneration("ddl", t.TempDir(), service.GenerationMeta{})

	srv := NewServer(Config{Store: store})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// 列表：kind 过滤 + 元数据 + 实时大小
	resp, _ := http.Get(ts.URL + "/api/v1/generations?kind=metadata")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d: %s", resp.StatusCode, body)
	}
	var list struct {
		Kind  string `json:"kind"`
		Items []struct {
			ID          int64          `json:"id"`
			SourceLabel string         `json:"source_label"`
			Detail      map[string]any `json:"detail"`
			FileCount   int            `json:"file_count"`
			SizeBytes   int64          `json:"size_bytes"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if list.Kind != "metadata" || len(list.Items) != 1 {
		t.Fatalf("list = %+v", list)
	}
	it := list.Items[0]
	if it.SourceLabel != "mysql@h:3306/s" || it.FileCount != 1 || it.SizeBytes != 1 {
		t.Errorf("item = %+v", it)
	}

	// files：内容可读
	resp2, _ := http.Get(fmt.Sprintf("%s/api/v1/generations/%d/files", ts.URL, it.ID))
	b2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("files status = %d: %s", resp2.StatusCode, b2)
	}
	if !strings.Contains(string(b2), "tables.csv") {
		t.Errorf("files body missing tables.csv: %s", b2)
	}

	// 未知 id → 404
	resp3, _ := http.Get(ts.URL + "/api/v1/generations/9999/files")
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404", resp3.StatusCode)
	}
}

func TestDownloadGen_ByIDAndLatest(t *testing.T) {
	store, err := service.NewJobStore(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	d1 := t.TempDir()
	os.WriteFile(filepath.Join(d1, "old.sql"), []byte("OLD"), 0644)
	store.RecordGeneration("ddl", d1, service.GenerationMeta{})
	d2 := t.TempDir()
	os.WriteFile(filepath.Join(d2, "new.sql"), []byte("NEW"), 0644)
	store.RecordGeneration("ddl", d2, service.GenerationMeta{})

	srv := NewServer(Config{Store: store})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// 缺省 = 最新
	resp, _ := http.Get(ts.URL + "/api/v1/ddl/download")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "NEW") || strings.Contains(string(body), "OLD") {
		t.Errorf("latest download wrong: %s", body)
	}

	// 按 id 取旧的
	recs, _ := store.ListGenerations("ddl")
	resp2, _ := http.Get(fmt.Sprintf("%s/api/v1/ddl/download?id=%d", ts.URL, recs[1].ID))
	b2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if !strings.Contains(string(b2), "OLD") {
		t.Errorf("by-id download wrong: %s", b2)
	}

	// 跨 kind 的 id → 404（metadata 端点上拿 ddl 记录）
	resp3, _ := http.Get(fmt.Sprintf("%s/api/v1/metadata/export/download?id=%d", ts.URL, recs[0].ID))
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-kind id status = %d, want 404", resp3.StatusCode)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/server/serve/ -run 'TestGenerationsAPI|TestDownloadGen_ByID' -count=1`
Expected: 失败（端点不存在 → 404 或匹配错误）。

- [ ] **Step 3: 实现端点与清理**（`generations.go` 追加）

```go
// handleListGenerations lists generation records for a kind with live
// on-disk stats (file count and total size).
func (s *Server) handleListGenerations(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "metadata"
	}
	recs, err := s.store.ListGenerations(kind)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list generations: "+err.Error())
		return
	}
	items := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		fc, sz := dirStats(rec.Dir)
		items = append(items, map[string]any{
			"id":              rec.ID,
			"kind":            rec.Kind,
			"dir":             rec.Dir,
			"created_at":      rec.CreatedAt,
			"source_label":    rec.SourceLabel,
			"datasource_name": rec.DatasourceName,
			"detail":          rec.Detail,
			"file_count":      fc,
			"size_bytes":      sz,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": kind, "items": items})
}

// handleGenerationFiles returns the file contents of one generation output.
func (s *Server) handleGenerationFiles(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid generation id")
		return
	}
	rec, err := s.store.GetGeneration(id)
	if err != nil {
		if errors.Is(err, service.ErrNoGeneration) {
			writeError(w, http.StatusNotFound, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, "lookup generation: "+err.Error())
		}
		return
	}
	entries, err := os.ReadDir(rec.Dir)
	if err != nil {
		writeError(w, http.StatusNotFound, "generation files no longer exist")
		return
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, filepath.Join(rec.Dir, e.Name()))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":           rec.ID,
		"kind":         rec.Kind,
		"created_at":   rec.CreatedAt,
		"source_label": rec.SourceLabel,
		"files":        readGenFiles(files),
	})
}

// pruneAllGenerations enforces retention across every kind; used at startup
// and on the hourly cleanup tick. Errors are non-fatal (stderr only).
func (s *Server) pruneAllGenerations() {
	for _, kind := range genKinds {
		pruned, err := s.store.PruneGenerations(kind, genOutputKeep, genOutputMaxAge)
		for _, d := range pruned {
			if rmErr := os.RemoveAll(d); rmErr != nil {
				fmt.Fprintf(os.Stderr, "warning: remove pruned generation dir %s: %v\n", d, rmErr)
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: prune generations %s: %v\n", kind, err)
		}
	}
}

// CleanupLoop enforces generation retention hourly until ctx is done.
func (s *Server) CleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pruneAllGenerations()
		}
	}
}
```

- [ ] **Step 4: 改 handleDownloadGen 支持 `?id=`**（`generate.go`，替换函数开头到 `entries, err := os.ReadDir(dir)` 之前，其余不变）

```go
func (s *Server) handleDownloadGen(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var (
			dir string
			err error
		)
		if idStr := r.URL.Query().Get("id"); idStr != "" {
			id, perr := strconv.ParseInt(idStr, 10, 64)
			if perr != nil {
				writeError(w, http.StatusBadRequest, "invalid generation id")
				return
			}
			rec, gerr := s.store.GetGeneration(id)
			if gerr == nil && rec.Kind != kind {
				writeError(w, http.StatusNotFound, "generation not found")
				return
			}
			dir = rec.Dir
			err = gerr
		} else {
			dir, err = s.store.LatestGeneration(kind)
		}
		if err != nil {
			if errors.Is(err, service.ErrNoGeneration) {
				writeError(w, http.StatusBadRequest, err.Error())
			} else {
				writeError(w, http.StatusInternalServerError, "lookup generation output: "+err.Error())
			}
			return
		}
		entries, err := os.ReadDir(dir)
		// …（zip 打包逻辑保持原样）
	}
}
```

- [ ] **Step 5: 注册路由 + 启动 prune**（`server.go`）

5a. 在 `mux.HandleFunc("GET /api/v1/metadata/export/download", …)` 附近加：

```go
	mux.HandleFunc("GET /api/v1/generations", s.handleListGenerations)
	mux.HandleFunc("GET /api/v1/generations/{id}/files", s.handleGenerationFiles)
```

5b. `NewServer` 返回前（`return s` 处）加启动 prune：

```go
	// Enforce generation retention at startup so stale dirs don't linger
	// after long uptimes or config changes.
	s.pruneAllGenerations()
	return s
```

- [ ] **Step 6: 挂定时清理**（`internal/cmd/serve.go`，heartbeat ticker goroutine 附近）

```go
			go srv.CleanupLoop(ctx)
```

- [ ] **Step 7: 跑测试确认通过**

Run: `go build ./... && go test ./internal/server/serve/ -count=1`
Expected: PASS。

- [ ] **Step 8: 提交**

```bash
git add internal/server/serve/generations.go internal/server/serve/generate.go internal/server/serve/server.go internal/cmd/serve.go internal/server/serve/generations_test.go
git commit -m "feat(serve): generation history API, by-id download, startup+hourly retention cleanup"
```

---

### Task 5: 前端历史面板

**Files:**
- Modify: `web/static/ui/views/exportMetadata.js`、`generator.js`、`export.js`
- Modify: `web/static/css/style.css`（追加 `.gen-history` 等基础样式）

- [ ] **Step 1: exportMetadata.js 历史面板**

1a. render 的 HTML 追加一个面板（放在 `#em-result` 面板之后、`'</div>';` 收尾之前，模板串以 `+` 拼接）：

```js
        + '<div class="panel reveal" style="--i:3" id="em-history">'
        +   '<div class="panel-head"><span class="panel-title">历史导出 <span class="badge" id="emh-count"></span></span></div>'
        +   '<div class="field-help" style="margin-bottom:10px">仅保留最近 10 次 · 最长 7 天 · 刷新页面后仍可浏览下载</div>'
        +   '<div id="emh-list" class="gen-history"></div>'
        + '</div>'
```

1b. render 内取元素（与其它 `querySelector` 一起）：

```js
    const historyList = root.querySelector('#emh-list');
    const historyCount = root.querySelector('#emh-count');
```

1c. render 末尾（`updateHint();` 之后）调用加载：

```js
    loadHistory();
```

1d. render 内新增函数（放在 `doExport` 定义后面）：

```js
    async function loadHistory() {
        try {
            const data = await window.api.get('/api/v1/generations?kind=metadata');
            const items = data.items || [];
            historyCount.textContent = items.length + ' 次';
            if (!items.length) {
                historyList.innerHTML = '<p class="field-help">暂无历史导出</p>';
                return;
            }
            historyList.innerHTML = items.map(it => {
                const d = (it.detail || {});
                const t = it.created_at ? new Date(it.created_at.replace(' ', 'T') + 'Z').toLocaleString() : '—';
                const src = it.source_label || '未知来源';
                const meta = [d.format, d.table_count != null ? d.table_count + ' 表' : null, it.file_count + ' 文件']
                    .filter(Boolean).join(' · ');
                const size = window.humanSize ? window.humanSize(it.size_bytes) : it.size_bytes + ' B';
                return '<div class="gen-row">'
                    + '<span class="gen-time">' + escapeHtml(t) + '</span>'
                    + '<span class="gen-src">' + escapeHtml(src) + '</span>'
                    + '<span class="gen-meta">' + escapeHtml(meta || '') + '</span>'
                    + '<span class="gen-size">' + escapeHtml(size) + '</span>'
                    + '<span class="gen-actions">'
                    +   '<a href="#" data-browse="' + it.id + '">浏览</a>'
                    +   '<a href="' + window.api.downloadURL('/api/v1/metadata/export/download?id=' + it.id) + '">下载</a>'
                    + '</span>'
                    + '</div>';
            }).join('');

            historyList.querySelectorAll('a[data-browse]').forEach(a => {
                a.addEventListener('click', async (e) => {
                    e.preventDefault();
                    try {
                        const f = await window.api.get('/api/v1/generations/' + a.dataset.browse + '/files');
                        const files = f.files || [];
                        resultEl.style.display = 'block';
                        countEl.textContent = f.source_label + ' · ' + files.length + ' 个文件';
                        filesEl.innerHTML = '<div class="file-tabs">'
                            + files.map(x => '<span class="file-tab" title="' + escapeHtml(String(x.content || '').length) + ' bytes">'
                                + escapeHtml(x.name) + '</span>').join('')
                            + '</div>';
                    } catch (err) {
                        window.toast && window.toast.err('读取历史导出失败', (err && err.message) || '');
                    }
                });
            });
        } catch (e) { /* history is best-effort */ }
    }
```

- [ ] **Step 2: generator.js（ddl/select/insert 共用）历史面板**

2a. render HTML：文件面板后追加：

```js
            + '<div class="panel reveal" style="--i:3" id="gen-history">'
            +   '<div class="panel-head"><span class="panel-title">历史' + cfg.downloadLabel + ' <span class="badge" id="genh-count"></span></span></div>'
            +   '<div id="genh-list" class="gen-history"></div>'
            + '</div>'
```

2b. render 内（取元素 + 加载，放在文件列表逻辑附近）：

```js
        const kind = (endpoint.match(/\/api\/v1\/(\w+)\/generate/) || [])[1] || 'ddl';
        const histList = root.querySelector('#genh-list');
        const histCount = root.querySelector('#genh-count');
        (async function loadGenHistory() {
            try {
                const data = await window.api.get('/api/v1/generations?kind=' + encodeURIComponent(kind));
                const items = data.items || [];
                histCount.textContent = items.length + ' 次';
                if (!items.length) { histList.innerHTML = '<p class="field-help">暂无历史</p>'; return; }
                histList.innerHTML = items.map(it => {
                    const t = it.created_at ? new Date(it.created_at.replace(' ', 'T') + 'Z').toLocaleString() : '—';
                    const src = it.source_label || '未知来源';
                    return '<div class="gen-row">'
                        + '<span class="gen-time">' + escapeHtml(t) + '</span>'
                        + '<span class="gen-src">' + escapeHtml(src) + '</span>'
                        + '<span class="gen-meta">' + (it.file_count || 0) + ' 文件</span>'
                        + '<span class="gen-size">' + (window.humanSize ? window.humanSize(it.size_bytes) : (it.size_bytes + ' B')) + '</span>'
                        + '<span class="gen-actions">'
                        +   '<a href="#" data-browse="' + it.id + '">浏览</a>'
                        +   '<a href="' + window.api.downloadURL(endpoint.replace(/\/generate$/, '/download') + '?id=' + it.id) + '">下载</a>'
                        + '</span>'
                        + '</div>';
                }).join('');
                histList.querySelectorAll('a[data-browse]').forEach(a => {
                    a.addEventListener('click', async (e) => {
                        e.preventDefault();
                        try {
                            const f = await window.api.get('/api/v1/generations/' + a.dataset.browse + '/files');
                            const files = f.files || [];
                            listEl.innerHTML = files.map(x => '<div class="file-tab">'
                                + escapeHtml(x.name) + '</div>').join('');
                        } catch (err) {
                            window.toast && window.toast.err('读取历史失败', (err && err.message) || '');
                        }
                    });
                });
            } catch (e) { /* history is best-effort */ }
        })();
```

注意：`generator.js` 需确认 `escapeHtml` 已导入；若未导入，在文件顶部补 `import { escapeHtml } from '../util.js';`（与 `exportMetadata.js` 同款）。

- [ ] **Step 3: export.js（离线导出）历史面板**

3a. render HTML：`#off-result` 面板后追加：

```js
        + '<div class="panel reveal" style="--i:4" id="off-history">'
        +   '<div class="panel-head"><span class="panel-title">历史离线导出 <span class="badge" id="offh-count"></span></span></div>'
        +   '<div id="offh-list" class="gen-history"></div>'
        + '</div>'
```

3b. render 内取元素 + 加载（放在 `startOffline` 定义附近）：

```js
    const offHistList = root.querySelector('#offh-list');
    const offHistCount = root.querySelector('#offh-count');
    (async function loadOffHistory() {
        try {
            const data = await window.api.get('/api/v1/generations?kind=export-offline');
            const items = data.items || [];
            offHistCount.textContent = items.length + ' 次';
            if (!items.length) { offHistList.innerHTML = '<p class="field-help">暂无历史</p>'; return; }
            offHistList.innerHTML = items.map(it => {
                const t = it.created_at ? new Date(it.created_at.replace(' ', 'T') + 'Z').toLocaleString() : '—';
                const src = it.source_label || '未知来源';
                return '<div class="gen-row">'
                    + '<span class="gen-time">' + escapeHtml(t) + '</span>'
                    + '<span class="gen-src">' + escapeHtml(src) + '</span>'
                    + '<span class="gen-meta">' + (it.file_count || 0) + ' 文件</span>'
                    + '<span class="gen-size">' + (window.humanSize ? window.humanSize(it.size_bytes) : (it.size_bytes + ' B')) + '</span>'
                    + '<span class="gen-actions">'
                    +   '<a href="#" data-browse="' + it.id + '">浏览</a>'
                    +   '<a href="' + window.api.downloadURL('/api/v1/export/offline/download?id=' + it.id) + '">下载</a>'
                    + '</span>'
                    + '</div>';
            }).join('');
            offHistList.querySelectorAll('a[data-browse]').forEach(a => {
                a.addEventListener('click', async (e) => {
                    e.preventDefault();
                    try {
                        const f = await window.api.get('/api/v1/generations/' + a.dataset.browse + '/files');
                        const files = f.files || [];
                        root.querySelector('#off-files').innerHTML = files.map(x => '<div class="file-tab">'
                            + escapeHtml(x.name) + '</div>').join('');
                    } catch (err) {
                        window.toast && window.toast.err('读取历史失败', (err && err.message) || '');
                    }
                });
            });
        } catch (e) { /* history is best-effort */ }
    })();
```

注意：`export.js` 需确认 `escapeHtml` 已导入；若未导入，文件顶部补 `import { escapeHtml } from '../util.js';`。

- [ ] **Step 4: 语法校验**

Run:
```bash
for f in exportMetadata generator export; do cp "web/static/ui/views/$f.js" "/tmp/$f.mjs"; node --check "/tmp/$f.mjs" && echo "$f OK"; done
```
Expected: 三个 OK。

- [ ] **Step 5: 样式**（`web/static/css/style.css` 末尾追加）

```css
/* generation history rows */
.gen-history { display: flex; flex-direction: column; gap: 6px; }
.gen-row {
    display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
    padding: 8px 10px; border: 1px solid var(--border, #333);
    border-radius: 8px; font-size: 13px;
}
.gen-row .gen-time { min-width: 150px; color: var(--text-dim, #999); }
.gen-row .gen-src { min-width: 140px; }
.gen-row .gen-meta { flex: 1; }
.gen-row .gen-size { min-width: 70px; text-align: right; color: var(--text-dim, #999); }
.gen-row .gen-actions { display: flex; gap: 10px; }
.gen-row .gen-actions a { color: var(--accent, #4da3ff); }
```

- [ ] **Step 6: 提交**

```bash
git add web/static/ui/views/exportMetadata.js web/static/ui/views/generator.js web/static/ui/views/export.js web/static/css/style.css
git commit -m "feat(web): generation history panels with browse/download per export"
```

---

### Task 6: 全量验证 + 冒烟

- [ ] **Step 1: 全量测试**

Run: `go build ./... && go test ./... -count=1`
Expected: 全 PASS。

- [ ] **Step 2: 真实二进制冒烟（认证 + 空态）**

```bash
go build -o /tmp/owl-migrate-hist ./cmd/migrate
rm -rf /tmp/owl-hist && OWL_MIGRATE_HOME=/tmp/owl-hist /tmp/owl-migrate-hist serve --port 18099 --token t123 --temp-dir /tmp/owl-hist/temp &
sleep 2
curl -s -H "Authorization: Bearer t123" "http://127.0.0.1:18099/api/v1/generations?kind=metadata"
# 期望 {"kind":"metadata","items":[]}
curl -s -o /dev/null -w "%{http_code}\n" "http://127.0.0.1:18099/api/v1/metadata/export/download?token=t123&id=9999"
# 期望 400（无此记录，认证已通过 → 非 401）
curl -s -o /dev/null -w "%{http_code}\n" "http://127.0.0.1:18099/api/v1/generations/9999/files"
# 期望 404
pkill -f owl-migrate-hist
```

Expected: 列表空 items、`?token=` 下载路由认证通过（非 401）、未知 id 404。带真实库的导出→历史冒烟依赖 `testdata/db` docker 环境，如无则跳过、以单测为准。

- [ ] **Step 3: 浏览器手动走查**（用户 serve 重建后）：元数据导出 → 历史面板出现记录 → 刷新页面 → 面板仍在 → 浏览/下载可用；DDL 页同样。

- [ ] **Step 4: 提交收尾**（只提交本计划相关改动，勿碰用户资料文件）

```bash
git add docs/superpowers/plans/2026-09-01-generation-history.md
git commit -m "docs: generation history implementation plan" 2>/dev/null || echo "plan already committed"
```
