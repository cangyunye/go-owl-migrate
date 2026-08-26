# Phase 0: Single-Node Web Service Hardening — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the six P0 defects and supporting items in the existing `serve` mode so a single cross-compiled binary is safe and functional for team-shared single-node deployment.

**Architecture:** No structural change — same serve HTTP + master IPC + subprocess-worker topology with SQLite job store. Tasks harden the data layer (pure-Go SQLite, schema `node_id`), state durability (generation registry in DB instead of memory), security (token-less safe defaults, body limits, same-origin policy, credential masking), and deployment (embedded docs, single-instance lock, build guardrails).

**Tech Stack:** Go 1.25+, stdlib `net/http`, `modernc.org/sqlite`, nhooyr.io/websocket (existing), SQLite via `database/sql`.

**Spec:** `docs/plans/2026-08-25-web-service-single-node-iteration.md` (§4–§7, §9, §12).

## Global Constraints

- Module path `github.com/cangyunye/go-owl-migrate`; all builds and tests run with `CGO_ENABLED=0`.
- No new runtime dependencies except `modernc.org/sqlite` (and its transitive deps). `github.com/mattn/go-sqlite3` stays in `go.mod` — it is still used by the optional `sqlite3` dialect build tag (`internal/cmd/driver_sqlite3.go`), which is out of scope here.
- Existing API paths under `/api/v1/*` must not change shape; additive changes only.
- Repo convention: Go doc comments on exported identifiers (match surrounding style); tests use plain `testing` + `httptest`, no assertion frameworks.
- Test command pattern: `CGO_ENABLED=0 go test ./internal/service/ ./internal/server/... -count=1`.
- Commit messages: lowercase imperative, prefix with area (e.g. `service:`, `serve:`, `build:`), matching `git log --oneline` style.
- Do not touch `web/templates/`, `web/static/`, or the SPA work — that is Phase 1+.

---

### Task 1: Switch job store to modernc.org/sqlite (pure Go)

**Files:**
- Modify: `internal/service/job.go:1-56` (imports + `NewJobStore`)
- Modify: `Makefile` (build targets)
- Modify: `go.mod` / `go.sum`

**Interfaces:**
- Consumes: nothing new — `database/sql` API only.
- Produces: `service.NewJobStore(dbPath string) (*JobStore, error)` with unchanged signature; every later task assumes the store works under `CGO_ENABLED=0`.

**Why:** With `CGO_ENABLED=0` (the silent default on cross-builds without a C toolchain), `mattn/go-sqlite3` compiles as a no-op stub and every `serve` feature fails at runtime. This task is the red→green cycle for that.

- [ ] **Step 1: Reproduce the failure (red)**

```bash
CGO_ENABLED=0 go test ./internal/service/ -run TestJobStore_CreateJob$ -count=1
```

Expected: FAIL with `Binary was compiled with 'CGO_ENABLED=0', go-sqlite3 requires cgo to work. This is a stub`.

- [ ] **Step 2: Add the pure-Go driver**

```bash
go get modernc.org/sqlite@latest && go mod tidy
```

- [ ] **Step 3: Swap driver name and DSN pragmas**

In `internal/service/job.go`, change the import and `NewJobStore`:

```go
import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)
```

```go
func NewJobStore(dbPath string) (*JobStore, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	s := &JobStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	return s, nil
}
```

Notes: driver name is `sqlite` (not `sqlite3`); modernc uses `_pragma=name(value)` instead of `_name=value`. Leave `internal/cmd/e2e_sqlite3_test.go` and `internal/cmd/driver_sqlite3.go` untouched (different feature, build-tagged).

- [ ] **Step 4: Run the affected suites (green)**

```bash
CGO_ENABLED=0 go test ./internal/service/ ./internal/server/... -count=1
```

Expected: PASS (all JobStore tests, serve handlers, websocket, master, e2e).

- [ ] **Step 5: Add build guardrails to Makefile**

Prepend `CGO_ENABLED=0` to the cross-compilable targets so a missing C toolchain can never silently downgrade again. Change these four recipes (`make build` native included, since nothing in the default build needs CGO):

```make
build:
	@mkdir -p $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)
	CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Built: $(BUILD_DIR)/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)"
```

```make
build/linux:
	@mkdir -p $(BUILD_DIR)/linux-amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/linux-amd64/$(BINARY_NAME) $(MAIN_PATH)

build/darwin-arm64:
	@mkdir -p $(BUILD_DIR)/darwin-arm64
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/darwin-arm64/$(BINARY_NAME) $(MAIN_PATH)

build/windows:
	@mkdir -p $(BUILD_DIR)/windows-amd64
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/windows-amd64/$(BINARY_NAME).exe $(MAIN_PATH)
```

Leave `build/sqlite3` and `build/duckdb` untouched (they legitimately require CGO and say so).

- [ ] **Step 6: Verify cross-build now works end to end**

```bash
make build/linux && file build/linux-amd64/owl-migrate
```

Expected: build succeeds on macOS with no C cross toolchain; `file` reports an ELF 64-bit executable (statically linked).

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/service/job.go Makefile
git commit -m "service: switch job store to pure-Go modernc.org/sqlite"
```

---

### Task 2: Add `node_id` column to job tables (2.0 seam)

**Files:**
- Modify: `internal/service/job.go` (`migrate`, new helper `addNodeIDColumns`)
- Test: `internal/service/job_test.go`

**Interfaces:**
- Consumes: `NewJobStore` from Task 1.
- Produces: columns `jobs.node_id`, `job_checkpoints.node_id`, `progress_events.node_id` (`TEXT NOT NULL DEFAULT 'local'`) on both fresh and pre-existing databases. No Go API changes.

- [ ] **Step 1: Write the failing test (upgrade path for old DBs)**

Append to `internal/service/job_test.go`:

```go
func TestJobStore_NodeIDUpgrade(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "upgrade.db")

	// Create a legacy database lacking node_id columns.
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE jobs (job_id TEXT PRIMARY KEY, type TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'running', config TEXT, pid INTEGER DEFAULT 0, created_at TEXT DEFAULT (datetime('now')), finished_at TEXT)`,
		`CREATE TABLE job_checkpoints (job_id TEXT NOT NULL, schema TEXT NOT NULL, table_name TEXT NOT NULL, exported INTEGER DEFAULT 0, exported_rows INTEGER DEFAULT 0, imported INTEGER DEFAULT 0, imported_rows INTEGER DEFAULT 0, status TEXT DEFAULT '', error TEXT DEFAULT '', PRIMARY KEY (job_id, schema, table_name))`,
		`CREATE TABLE progress_events (id INTEGER PRIMARY KEY AUTOINCREMENT, job_id TEXT NOT NULL, seq INTEGER NOT NULL, event_type TEXT NOT NULL, schema TEXT DEFAULT '', table_name TEXT DEFAULT '', rows INTEGER DEFAULT 0, message TEXT DEFAULT '', created_at TEXT DEFAULT (datetime('now')))`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("create legacy table: %v", err)
		}
	}
	raw.Close()

	store, err := NewJobStore(dbPath)
	if err != nil {
		t.Fatalf("NewJobStore on legacy db: %v", err)
	}
	defer store.Close()

	if err := store.CreateJob("j1", "migrate", "{}"); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	var nodeID string
	if err := store.db.QueryRow(`SELECT node_id FROM jobs WHERE job_id = 'j1'`).Scan(&nodeID); err != nil {
		t.Fatalf("node_id missing: %v", err)
	}
	if nodeID != "local" {
		t.Errorf("node_id = %q, want \"local\"", nodeID)
	}
}
```

Add `"database/sql"` to the test imports if not present. (`store.db` is unexported but the test is in package `service`, so direct access is fine.)

- [ ] **Step 2: Run test to verify it fails**

```bash
CGO_ENABLED=0 go test ./internal/service/ -run TestJobStore_NodeIDUpgrade -count=1
```

Expected: FAIL with `node_id missing: no such column`.

- [ ] **Step 3: Implement the migration**

In `internal/service/job.go`, add `node_id TEXT NOT NULL DEFAULT 'local'` as the last column in all three `CREATE TABLE IF NOT EXISTS` statements inside `migrate()` (`jobs`, `job_checkpoints`, `progress_events`). Then extend `migrate()` to upgrade existing databases — append after the schema `Exec`:

```go
	if err := s.addNodeIDColumns(); err != nil {
		return err
	}
	return nil
```

(replacing the previous bare `return err` of `migrate()`), and add:

```go
// addNodeIDColumns backfills the 2.0 node_id seam into databases created
// before the column existed. Tables are hardcoded; names never come from input.
func (s *JobStore) addNodeIDColumns() error {
	for _, tbl := range []string{"jobs", "job_checkpoints", "progress_events"} {
		var n int
		q := fmt.Sprintf(`SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name = 'node_id'`, tbl)
		if err := s.db.QueryRow(q).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			q = fmt.Sprintf(`ALTER TABLE %s ADD COLUMN node_id TEXT NOT NULL DEFAULT 'local'`, tbl)
			if _, err := s.db.Exec(q); err != nil {
				return err
			}
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
CGO_ENABLED=0 go test ./internal/service/ -count=1
```

Expected: PASS, including all pre-existing JobStore tests (fresh-DB path).

- [ ] **Step 5: Commit**

```bash
git add internal/service/job.go internal/service/job_test.go
git commit -m "service: add node_id column to job tables for 2.0 node tracking"
```

---

### Task 3: Make `randSuffix()` actually random

**Files:**
- Modify: `internal/server/serve/generate.go:333-335`
- Test: `internal/server/serve/generate_test.go` (create)

**Interfaces:**
- Consumes: nothing.
- Produces: `randSuffix() string` (package-private) used to name generation output dirs; later tasks rely on distinct dirs per call.

**Why:** current implementation is `pid + "-" + len(os.TempDir())` — constant per process, so two generations overwrite the same directory.

- [ ] **Step 1: Write the failing test**

Create `internal/server/serve/generate_test.go`:

```go
package serve

import "testing"

func TestRandSuffixUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		s := randSuffix()
		if s == "" {
			t.Fatal("randSuffix returned empty string")
		}
		if seen[s] {
			t.Fatalf("randSuffix collided after %d calls: %q", i, s)
		}
		seen[s] = true
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
CGO_ENABLED=0 go test ./internal/server/serve/ -run TestRandSuffixUnique -count=1
```

Expected: FAIL with `randSuffix collided`.

- [ ] **Step 3: Implement**

Replace `randSuffix` in `internal/server/serve/generate.go`:

```go
func randSuffix() string {
	var b [4]byte
	rand.Read(b[:])
	return fmt.Sprintf("%d-%x", time.Now().UnixNano(), b[:])
}
```

Add `crypto/rand` and `time` to the imports of `generate.go`.

- [ ] **Step 4: Run test to verify it passes**

```bash
CGO_ENABLED=0 go test ./internal/server/serve/ -run TestRandSuffixUnique -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/server/serve/generate_test.go internal/server/serve/generate.go
git commit -m "serve: make generation output dir suffix actually random"
```

---

### Task 4: Persist generation outputs in SQLite (survive restart, multi-user safe)

**Files:**
- Modify: `internal/service/job.go` (schema + two new methods)
- Test: `internal/service/job_test.go`
- Modify: `internal/server/serve/server.go` (remove `genOutputs` field and accessors)
- Modify: `internal/server/serve/generate.go` (record helper + download handler)
- Modify: `internal/server/serve/exportmetadata.go`, `internal/server/serve/exportoffline.go` (any `setGenOutput` call sites)
- Test: `internal/server/serve/newhandlers_test.go`

**Interfaces:**
- Consumes: Task 1 store.
- Produces (service): `(*JobStore) RecordGeneration(kind, dir string, keep int) (prunedDirs []string, err error)` and `(*JobStore) LatestGeneration(kind string) (dir string, err error)`.
- Produces (serve): HTTP `GET /api/v1/{ddl,select,insert,metadata,export/offline}/download` now resolve their directory from the DB; the in-memory `genOutputs` map, `setGenOutput`, `getGenOutput` are deleted.

- [ ] **Step 1: Write the failing service test**

Append to `internal/service/job_test.go`:

```go
func TestJobStore_GenerationRetention(t *testing.T) {
	store, err := NewJobStore(filepath.Join(t.TempDir(), "gen.db"))
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	defer store.Close()

	var allPruned []string
	for i := 1; i <= 3; i++ {
		pruned, err := store.RecordGeneration("ddl", fmt.Sprintf("/tmp/gen-%d", i), 2)
		if err != nil {
			t.Fatalf("RecordGeneration(%d): %v", i, err)
		}
		allPruned = append(allPruned, pruned...)
	}

	if len(allPruned) != 1 || allPruned[0] != "/tmp/gen-1" {
		t.Fatalf("pruned = %v, want [/tmp/gen-1]", allPruned)
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

Add `"fmt"` to imports if missing.

- [ ] **Step 2: Run test to verify it fails**

```bash
CGO_ENABLED=0 go test ./internal/service/ -run TestJobStore_GenerationRetention -count=1
```

Expected: FAIL — compile error `store.RecordGeneration undefined`.

- [ ] **Step 3: Implement the service layer**

In `internal/service/job.go` `migrate()`, append to the schema string (inside the backtick block, after the `CREATE INDEX` line):

```sql
CREATE TABLE IF NOT EXISTS generation_outputs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT NOT NULL,
    dir        TEXT NOT NULL,
    created_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_gen_kind ON generation_outputs(kind, id);
```

Then add the methods:

```go
// RecordGeneration stores a generation output record, prunes records beyond
// keep for that kind, and returns the pruned dirs so the caller can delete
// them from disk.
func (s *JobStore) RecordGeneration(kind, dir string, keep int) ([]string, error) {
	if _, err := s.db.Exec(
		`INSERT INTO generation_outputs (kind, dir) VALUES (?, ?)`, kind, dir,
	); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(
		`SELECT id, dir FROM generation_outputs WHERE kind = ? AND id NOT IN
		 (SELECT id FROM generation_outputs WHERE kind = ? ORDER BY id DESC LIMIT ?)`,
		kind, kind, keep,
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

// LatestGeneration returns the most recent output dir for kind.
func (s *JobStore) LatestGeneration(kind string) (string, error) {
	var dir string
	err := s.db.QueryRow(
		`SELECT dir FROM generation_outputs WHERE kind = ? ORDER BY id DESC LIMIT 1`, kind,
	).Scan(&dir)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("nothing generated yet for %s", kind)
	}
	if err != nil {
		return "", err
	}
	return dir, nil
}
```

- [ ] **Step 4: Run the service test**

```bash
CGO_ENABLED=0 go test ./internal/service/ -count=1
```

Expected: PASS.

- [ ] **Step 5: Rewire the serve handlers**

In `internal/server/serve/server.go`: delete the `genOutputs map[string]string` field from `Server`, delete `setGenOutput`/`getGenOutput`, and drop `genOutputs: make(map[string]string)` from `NewServer`.

In `internal/server/serve/generate.go`, add:

```go
// genOutputKeep is how many generation outputs are retained per kind; the
// oldest dirs are removed from disk when the limit is exceeded.
const genOutputKeep = 10

// recordGenOutput persists a generation output directory in the job store and
// prunes retired outputs from disk.
func (s *Server) recordGenOutput(kind, dir string) error {
	pruned, err := s.store.RecordGeneration(kind, dir, genOutputKeep)
	for _, d := range pruned {
		os.RemoveAll(d)
	}
	return err
}
```

Replace every `s.setGenOutput(kind, outDir)` call (`generate.go` for `"ddl"`, `"select"`, `"insert"`; `exportmetadata.go` for `"metadata"`; `exportoffline.go` for `"export-offline"` — find them all with `grep -rn "setGenOutput" internal/server/serve/`) with:

```go
	if err := s.recordGenOutput("ddl", outDir); err != nil {
		writeError(w, http.StatusInternalServerError, "record output: "+err.Error())
		return
	}
```

(keep the matching kind string at each site).

In `handleDownloadGen` replace the directory lookup:

```go
func (s *Server) handleDownloadGen(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dir, err := s.store.LatestGeneration(kind)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, kind))
		zw := zip.NewWriter(w)
		defer zw.Close()
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			f, err := zw.Create(e.Name())
			if err != nil {
				continue
			}
			f.Write(data)
		}
	}
}
```

- [ ] **Step 6: Write the serve-level test**

Append to `internal/server/serve/newhandlers_test.go` (merge imports as needed: `archive/zip`, `bytes`, `io`, `net/http`, `net/http/httptest`, `os`, `path/filepath`):

```go
func TestDownloadGen_PersistedAcrossRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "t.db")
	store, err := service.NewJobStore(dbPath)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	outDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outDir, "emp.sql"), []byte("CREATE TABLE emp (id INT);"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordGeneration("ddl", outDir, 10); err != nil {
		t.Fatalf("RecordGeneration: %v", err)
	}

	srv := NewServer(Config{Store: store})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/api/v1/ddl/download")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "emp.sql" {
		t.Fatalf("zip contents wrong: %+v", zr.File)
	}
}
```

- [ ] **Step 7: Run everything and verify generation flows**

```bash
CGO_ENABLED=0 go test ./internal/service/ ./internal/server/... -count=1
```

Expected: PASS. If pre-existing e2e tests exercised generation downloads via the in-memory map, they now cover the DB path.

- [ ] **Step 8: Commit**

```bash
git add internal/service/job.go internal/service/job_test.go \
        internal/server/serve/server.go internal/server/serve/generate.go \
        internal/server/serve/exportmetadata.go internal/server/serve/exportoffline.go \
        internal/server/serve/newhandlers_test.go
git commit -m "serve: persist generation outputs in job store instead of memory"
```

---

### Task 5: Mask credentials in config read endpoint

**Files:**
- Create: `internal/config/mask.go`
- Test: `internal/config/mask_test.go`
- Modify: `internal/server/serve/server.go` (`handleGetConfig`; new `maskConfigMap` helper next to `configToMap`)
- Test: `internal/server/serve/handlers_test.go` (or `newhandlers_test.go`)

**Interfaces:**
- Consumes: nothing.
- Produces: `config.MaskDSN(dsn string) string` — masks the password in URL-form (`postgres://u:p@h/db`, `oracle://u:p@h/svc`) and MySQL native-form (`u:p@tcp(h:3306)/db`) DSNs; returns anything unrecognized unchanged.

Scope: only `GET /api/v1/config` masks. Download/load endpoints return files verbatim (file-equivalent operations; masking would break save round-trips). This matches spec §7 and is recorded in the API contract doc (Task 10).

- [ ] **Step 1: Write the failing unit test**

Create `internal/config/mask_test.go`:

```go
package config

import "testing"

func TestMaskDSN(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"postgres url", "postgres://scott:tiger@db.example:5432/app", "postgres://scott:******@db.example:5432/app"},
		{"oracle url", "oracle://scott:tiger@10.0.0.1:1521/ORCL", "oracle://scott:******@10.0.0.1:1521/ORCL"},
		{"mysql native", "scott:tiger@tcp(127.0.0.1:3306)/app", "scott:******@tcp(127.0.0.1:3306)/app"},
		{"url without password", "postgres://scott@db.example:5432/app", "postgres://scott@db.example:5432/app"},
		{"empty", "", ""},
		{"unrecognized", "some-host:1521", "some-host:1521"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaskDSN(tc.in); got != tc.want {
				t.Errorf("MaskDSN(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
CGO_ENABLED=0 go test ./internal/config/ -run TestMaskDSN -count=1
```

Expected: FAIL — `MaskDSN undefined` (compile error).

- [ ] **Step 3: Implement**

Create `internal/config/mask.go`:

```go
package config

import (
	"net/url"
	"regexp"
	"strings"
)

var mysqlDSNPassword = regexp.MustCompile(`^([^:@/]+):([^@]*)@`)

// MaskDSN replaces the password embedded in a DSN with asterisks. URL-form
// DSNs are parsed; MySQL native-form DSNs (user:pass@tcp(host)/db) are
// handled by pattern. Unrecognized forms are returned unchanged so masking
// never destroys an opaque DSN.
func MaskDSN(dsn string) string {
	if dsn == "" {
		return dsn
	}
	if m := mysqlDSNPassword.FindStringSubmatch(dsn); m != nil && !strings.Contains(m[2], "/") {
		// m[2] containing "/" means we matched a scheme like "postgres://u",
		// not a native MySQL DSN; let url.Parse handle that below.
		return m[1] + ":******@" + dsn[len(m[0]):]
	}
	if u, err := url.Parse(dsn); err == nil && u.User != nil {
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(u.User.Username(), "******")
			return u.String()
		}
	}
	return dsn
}
```

- [ ] **Step 4: Run unit test**

```bash
CGO_ENABLED=0 go test ./internal/config/ -run TestMaskDSN -count=1
```

Expected: PASS.

- [ ] **Step 5: Apply masking in the read endpoint**

In `internal/server/serve/server.go` (next to `configToMap`), add:

```go
// maskConfigMap masks DSN passwords in a serialized config map before it is
// returned by read endpoints.
func maskConfigMap(m map[string]any) {
	for _, key := range []string{"source", "target"} {
		sec, _ := m[key].(map[string]any)
		if sec == nil {
			continue
		}
		if dsn, ok := sec["dsn"].(string); ok && dsn != "" {
			sec["dsn"] = config.MaskDSN(dsn)
		}
	}
}
```

(import `github.com/cangyunye/go-owl-migrate/internal/config`).

In `server.go` `handleGetConfig`, mask before responding:

```go
	m, err := configToMap(s.cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	maskConfigMap(m)
	writeJSON(w, http.StatusOK, m)
```

- [ ] **Step 6: Write the handler test**

Append to `internal/server/serve/handlers_test.go`:

```go
func TestGetConfig_MasksDSNPassword(t *testing.T) {
	srv := NewServer(Config{ConfigPath: filepath.Join(t.TempDir(), "migrate.yaml")})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	putBody, err := json.Marshal(map[string]any{
		"source": map[string]any{"type": "oracle", "dsn": "oracle://scott:tiger@db:1521/ORCL"},
		"target": map[string]any{"type": "postgres", "dsn": "postgres://u:p@h/db"},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/config", bytes.NewReader(putBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", resp.StatusCode)
	}

	resp, err = http.Get(ts.URL + "/api/v1/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "tiger") || strings.Contains(string(body), ":p@h/") {
		t.Fatalf("response leaks password: %s", body)
	}
	if strings.Count(string(body), "******") < 2 {
		t.Fatalf("expected both dsns masked, got: %s", body)
	}
}
```

(add `bytes`, `encoding/json`, `io` imports as needed). `handlePutConfig` persists via `persistConfig` into the temp `ConfigPath`, so the test stays hermetic.

- [ ] **Step 7: Run tests**

```bash
CGO_ENABLED=0 go test ./internal/config/ ./internal/server/serve/ -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/config/mask.go internal/config/mask_test.go \
        internal/server/serve/server.go internal/server/serve/handlers_test.go
git commit -m "serve: mask DSN credentials in GET /api/v1/config"
```

---

### Task 6: Network hardening — CORS removal, WS same-origin, body limits

**Files:**
- Modify: `internal/server/serve/server.go` (remove `withCORS`, add `decodeJSON`)
- Modify: `internal/server/serve/websocket.go` (drop `InsecureSkipVerify`)
- Modify: all serve handlers decoding JSON bodies (`grep -rn "json.NewDecoder(r.Body)" internal/server/serve/` — sites in `server.go`, `configs.go`, `jobs.go`, `generate.go`, `exportmetadata.go`, `exportoffline.go`)
- Test: `internal/server/serve/newhandlers_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: same-origin-only server; helper `decodeJSON(w http.ResponseWriter, r *http.Request, v any, limit int64) bool` used by every JSON-decoding handler; constants `maxBodyBytes = 1 << 20`, `maxConfigBytes = 2 << 20`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/server/serve/newhandlers_test.go`:

```go
func TestBodyLimit_RejectsOversizedPayload(t *testing.T) {
	store, err := service.NewJobStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	srv := NewServer(Config{Store: store})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Post(ts.URL+"/api/v1/metadata/load", "application/json",
		bytes.NewReader(bytes.Repeat([]byte("A"), 2<<20)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "request body too large") {
		t.Fatalf("expected body-limit error, got: %s", body)
	}
}

func TestWebSocket_RejectsForeignOrigin(t *testing.T) {
	store, err := service.NewJobStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	srv := NewServer(Config{Store: store})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	hdr := http.Header{}
	hdr.Set("Origin", "http://evil.example")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/jobs/j1/ws"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err == nil {
		conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("expected foreign-origin dial to be rejected")
	}
}
```

(imports: `bytes`, `context`, `io`, `net/http`, `net/http/httptest`, `path/filepath`, `strings`, `testing`, `time`, `nhooyr.io/websocket`, `service` — merge with what the file already imports.)

- [ ] **Step 2: Run tests to verify they fail**

```bash
CGO_ENABLED=0 go test ./internal/server/serve/ -run "TestBodyLimit|TestWebSocket_RejectsForeignOrigin" -count=1
```

Expected: FAIL both (`status = 200` / dial succeeds).

- [ ] **Step 3: Implement — remove CORS, enforce origin, add limits**

`server.go`:

1. Delete the `withCORS` function; change `Handler()` to `return mux` (drop the `withCORS(mux)` wrap). Same-origin browsers need no CORS headers.
2. Add:

```go
const (
	maxBodyBytes   = 1 << 20 // 1 MiB general request bodies
	maxConfigBytes = 2 << 20 // 2 MiB config YAML uploads
)

// decodeJSON enforces the body limit then decodes JSON, writing a 400 error
// and returning false on failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}
```

3. Replace every `json.NewDecoder(r.Body).Decode(&x)` (plus its `if err != nil { writeError(...invalid JSON...) }` block) across serve handlers with:

```go
	if !decodeJSON(w, r, &req, maxBodyBytes) {
		return
	}
```

using `maxConfigBytes` for `handleUploadConfig` and `handleUploadConfigLegacy`. In `jobs.go` `startJob`, the decode ignores errors today — change it to the same pattern (a malformed/oversized start body is a client error):

```go
	var body struct {
		Mode            string `json:"mode"`
		SkipDDL         bool   `json:"skip_ddl"`
		ContinueOnError bool   `json:"continue_on_error"`
	}
	if !decodeJSON(w, r, &body, maxBodyBytes) {
		return
	}
```

Find all sites with `grep -rn "json.NewDecoder(r.Body)" internal/server/serve/` — do not miss any non-test file.

`websocket.go`: replace the Accept options to enforce same-origin:

```go
	conn, err := websocket.Accept(w, r, nil)
```

(nhooyr's default policy rejects cross-origin requests; it is the `InsecureSkipVerify: true` option that was disabling it.)

- [ ] **Step 4: Run tests**

```bash
CGO_ENABLED=0 go test ./internal/server/serve/ -count=1
```

Expected: PASS including the pre-existing websocket tests (the nhooyr Go client sets a matching Origin automatically).

- [ ] **Step 5: Commit**

```bash
git add internal/server/serve/
git commit -m "serve: drop wildcard CORS, enforce WS same-origin, limit request bodies"
```

---

### Task 7: Single-instance lock for `serve`

**Files:**
- Modify: `internal/paths/paths.go` (add `ServeLockPath`)
- Create: `internal/cmd/serve_lock.go`
- Test: `internal/cmd/serve_lock_test.go`
- Modify: `internal/cmd/serve.go` (acquire before touching the job store)

**Interfaces:**
- Consumes: nothing.
- Produces: `paths.ServeLockPath() string` (`~/.owl/migrate/serve.lock`); package-private `acquireServeLock(path string) error` / `releaseServeLock(path string)` / `processAlive(pid int) bool`.

**Why:** today a second `serve` process runs `MarkRunningAsInterrupted` and corrupts the first instance's live jobs.

- [ ] **Step 1: Write the failing test**

Create `internal/cmd/serve_lock_test.go`:

```go
package cmd

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestServeLock_AcquireReleaseCycle(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "serve.lock")

	if err := acquireServeLock(lock); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if err := acquireServeLock(lock); err == nil {
		t.Fatal("second acquire by live owner must fail")
	}
	releaseServeLock(lock)
	if err := acquireServeLock(lock); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	releaseServeLock(lock)
}

func TestServeLock_TakesOverStaleLock(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "serve.lock")
	// A PID that cannot exist on any sane system.
	if err := os.WriteFile(lock, []byte("999999999"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := acquireServeLock(lock); err != nil {
		t.Fatalf("stale lock takeover failed: %v", err)
	}
	data, _ := os.ReadFile(lock)
	if string(data) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("lock content = %q, want current pid", data)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
CGO_ENABLED=0 go test ./internal/cmd/ -run TestServeLock -count=1
```

Expected: FAIL — `acquireServeLock undefined`.

- [ ] **Step 3: Implement**

`internal/paths/paths.go` — append:

```go
func ServeLockPath() string {
	return filepath.Join(Home(), "serve.lock")
}
```

Create `internal/cmd/serve_lock.go`:

```go
package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// acquireServeLock writes our PID to path, failing if another live process
// holds the lock. Stale locks (unreadable or dead PID) are taken over.
func acquireServeLock(path string) error {
	if data, err := os.ReadFile(path); err == nil {
		pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if convErr == nil && processAlive(pid) {
			return fmt.Errorf("another owl-migrate serve is running (pid %d, lock %s); stop it or remove the lock file", pid, path)
		}
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0644)
}

func releaseServeLock(path string) {
	os.Remove(path)
}

// processAlive probes the PID with signal 0 without delivering anything.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
```

- [ ] **Step 4: Run the lock test**

```bash
CGO_ENABLED=0 go test ./internal/cmd/ -run TestServeLock -count=1
```

Expected: PASS.

- [ ] **Step 5: Wire into `serve.go`**

In `serveCmd()` `RunE`, acquire the lock before opening the job store (after the flag defaults block, before `store, err := service.NewJobStore(dbPath)`):

```go
			lockPath := paths.ServeLockPath()
			if err := acquireServeLock(lockPath); err != nil {
				return err
			}
			defer releaseServeLock(lockPath)
```

- [ ] **Step 6: Run the whole cmd + serve suites**

```bash
CGO_ENABLED=0 go test ./internal/cmd/ ./internal/server/... -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/paths/paths.go internal/cmd/serve_lock.go internal/cmd/serve_lock_test.go internal/cmd/serve.go
git commit -m "serve: single-instance lock prevents stomping live jobs"
```

---

### Task 8: Embed the docs portal in the binary

**Files:**
- Modify: `Makefile` (new `web/docsite` target, wired into builds)
- Create: `web/docsite/index.html` (committed placeholder, overwritten at build)
- Modify: `web/embed.go`
- Modify: `.gitignore`
- Modify: `internal/server/serve/docs.go`
- Test: `internal/server/serve/docs_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `GET /docs/` works from a bare binary with no files on disk. Dev machines with `docs-site/` on disk keep serving the live tree.

Background: `docs-site/index.html` is a single-file portal that fetches markdown at runtime from `${base}` = the `docs/` path next to itself, and needs `vendor/marked.min.js`. `go:embed` cannot reference parent directories, so a Makefile staging step copies `docs-site/` + top-level `docs/*.md` into `web/docsite/` before build.

- [ ] **Step 1: Write the failing test**

Create `internal/server/serve/docs_test.go`:

```go
package serve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// From this package's test working directory no on-disk docs-site/ exists,
// so the embedded fallback must serve the portal.
func TestDocs_EmbeddedFallback(t *testing.T) {
	srv := NewServer(Config{})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/docs/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /docs/ status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(strings.ToLower(string(body)), "<html") {
		t.Fatalf("expected HTML portal, got %.80s", body)
	}

	resp2, err := http.Get(ts.URL + "/docs/docs/index.md")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("GET /docs/docs/index.md status = %d, want 200", resp2.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
CGO_ENABLED=0 go test ./internal/server/serve/ -run TestDocs_EmbeddedFallback -count=1
```

Expected: FAIL with 404 (`docs-site/ directory not found` handler).

- [ ] **Step 3: Create the staging target and placeholder**

`Makefile` — add before the `build:` target:

```make
# Stage the docs portal + markdown into web/docsite/ so go:embed can bundle
# them (go:embed cannot reference parent directories). Regenerated on build.
web/docsite:
	@rm -rf web/docsite
	@mkdir -p web/docsite/docs
	@cp docs-site/index.html web/docsite/
	@cp -R docs-site/vendor web/docsite/vendor
	@cp docs/*.md web/docsite/docs/
	@echo "Docs staged into web/docsite/"
```

Make every binary target depend on it: `build: web/docsite`, `build/linux: web/docsite`, `build/darwin-arm64: web/docsite`, `build/windows: web/docsite` (the sqlite3/duckdb variants too). Add to `.PHONY`: `web/docsite` is a directory target, not phony — instead add it as an order-only prerequisite if desired; simplest correct form here is the plain dependency above.

Commit a placeholder so `go test ./...` and plain `go build` work without running make first — create `web/docsite/index.html`:

```html
<!doctype html>
<html><head><meta charset="utf-8"><title>owl-migrate docs</title></head>
<body><h1>Docs not built</h1><p>Run <code>make web/docsite</code> (or any <code>make build*</code> target) to stage the documentation portal.</p></body></html>
```

and `web/docsite/docs/index.md`:

```markdown
# Docs not built

Run `make web/docsite` to stage the documentation portal.
```

`.gitignore` — append (ignore staged content, keep the two placeholders tracked; note the directory-un-ignore patterns required for git to see files below an ignored dir):

```
web/docsite/*
!web/docsite/index.html
!web/docsite/docs/
web/docsite/docs/*
!web/docsite/docs/index.md
```

Verify with `git status --ignored` after running `make web/docsite`: only `web/docsite/index.html` and `web/docsite/docs/index.md` are tracked; everything else ignored.

- [ ] **Step 4: Embed**

`web/embed.go`:

```go
package web

import "embed"

//go:embed templates/* static/* docs/* docsite/*
var FS embed.FS
```

Note: `go:embed` with `docsite/*` does not match files starting with `.` or `_`; the placeholders and staged content have normal names, so this is fine.

- [ ] **Step 5: Rewrite docs.go with embedded fallback**

Replace `internal/server/serve/docs.go` with:

```go
package serve

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cangyunye/go-owl-migrate/web"
)

func (s *Server) registerDocs(mux *http.ServeMux) {
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/docs/", http.StatusMovedPermanently)
	})

	// Dev/live mode: serve the on-disk docs-site tree so edits are visible
	// without rebuilding. Release binaries run from a bare directory and hit
	// the embedded fallback below.
	if siteDir := findDocsSite(); siteDir != "" {
		if docsDir := resolveDocsDir(siteDir); docsDir != "" {
			mux.Handle("GET /docs/docs/", http.StripPrefix("/docs/docs/", http.FileServer(http.Dir(docsDir))))
		}
		mux.Handle("GET /docs/", http.StripPrefix("/docs/", http.FileServer(http.Dir(siteDir))))
		return
	}

	sub, err := fs.Sub(web.FS, "docsite")
	if err != nil {
		mux.HandleFunc("GET /docs/", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "docs not available", http.StatusNotFound)
		})
		return
	}
	mux.Handle("GET /docs/", http.StripPrefix("/docs/", http.FileServer(http.FS(sub))))
}

func findDocsSite() string {
	candidates := []string{
		"./docs-site",
		"../docs-site",
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "docs-site"))
	}
	for _, c := range candidates {
		if info, err := os.Stat(filepath.Join(c, "index.html")); err == nil && !info.IsDir() {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return ""
}

func resolveDocsDir(siteDir string) string {
	link := filepath.Join(siteDir, "docs")
	if resolved, err := filepath.EvalSymlinks(link); err == nil {
		if info, err := os.Stat(resolved); err == nil && info.IsDir() {
			return resolved
		}
	}
	for _, c := range []string{filepath.Join(siteDir, "..", "docs"), "./docs"} {
		if abs, err := filepath.Abs(c); err == nil {
			if info, err := os.Stat(filepath.Join(abs, "index.md")); err == nil && !info.IsDir() {
				return abs
			}
		}
	}
	return ""
}
```

- [ ] **Step 6: Run the test against staged content**

```bash
make web/docsite
CGO_ENABLED=0 go test ./internal/server/serve/ -run TestDocs_EmbeddedFallback -count=1
```

Expected: PASS (test CWD `internal/server/serve` has no `docs-site/`, so the embed path serves).

- [ ] **Step 7: Commit**

```bash
git add Makefile .gitignore web/embed.go web/docsite/ internal/server/serve/docs.go internal/server/serve/docs_test.go
git commit -m "serve: embed docs portal so single-binary deploys include /docs"
```

---

### Task 9: Graceful cancel + dead code removal

**Files:**
- Modify: `internal/server/master/master.go` (`killProcess`, delete `selectPort`/`isPortFree`)
- Test: `internal/server/master/master_test.go` (delete `TestSelectPort`, add kill tests)
- Modify: `internal/server/serve/websocket.go` (delete `Hub`)
- Modify: `internal/server/serve/server.go` (drop `hub` field)

**Interfaces:**
- Consumes: nothing.
- Produces: `killProcess(pid)` sends SIGTERM, then SIGKILL after `killGrace` (package var, 5s default, shortened in tests). The dead `Hub` subscriber machinery disappears; websocket handlers keep their DB-polling behavior unchanged.

- [ ] **Step 1: Write the failing tests**

In `internal/server/master/master_test.go`, delete `TestSelectPort` entirely (it tests the function being removed). Add:

```go
func TestKillProcess_TermsPromptly(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	killProcess(cmd.Process.Pid)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected sleep to die from a signal")
		}
	case <-time.After(3 * time.Second):
		cmd.Process.Kill()
		t.Fatal("process ignored SIGTERM beyond 3s")
	}
}

func TestKillProcess_EscalatesToKill(t *testing.T) {
	old := killGrace
	killGrace = 300 * time.Millisecond
	t.Cleanup(func() { killGrace = old })

	cmd := exec.Command("sh", "-c", `trap '' TERM; sleep 30`)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	killProcess(cmd.Process.Pid)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		cmd.Process.Kill()
		t.Fatal("process survived SIGTERM + grace period")
	}
}
```

(add `os/exec` and `time` imports if missing).

- [ ] **Step 2: Run tests to verify they fail**

```bash
CGO_ENABLED=0 go test ./internal/server/master/ -run TestKillProcess -count=1
```

Expected: FAIL — `killGrace undefined` (and possibly the escalation test hanging until timeout under the current SIGKILL-only implementation).

- [ ] **Step 3: Implement graceful kill and remove dead code**

In `master.go`, replace `killProcess` and add the grace var:

```go
// killGrace is how long a cancelled worker gets to finish persisting its
// checkpoint after SIGTERM before SIGKILL. Package var so tests can shorten it.
var killGrace = 5 * time.Second

// killProcess asks the worker to terminate, escalating to SIGKILL after the
// grace period. A signal to an already-exited process fails silently.
func killProcess(pid int) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return
	}
	time.AfterFunc(killGrace, func() {
		proc.Signal(syscall.SIGKILL)
	})
}
```

Add `syscall` to imports. Delete the unused `selectPort` and `isPortFree` functions (and the now-unused `net` import if nothing else uses it).

- [ ] **Step 4: Remove the dead Hub**

In `websocket.go`: delete the `Hub` type, `wsClient` type, `NewHub`, `addSubscriber`, `removeSubscriber`, `broadcast`, `hasSubscribers` — keep `handleWebSocket`, `terminalMessage`, `eventToMessage` exactly as they are (the handler already polls the store directly). Drop the `sync` import if unused.

In `server.go`: delete the `hub *Hub` field and the `hub: NewHub(cfg.Store)` line in `NewServer`. In `handleWebSocket` (websocket.go), remove the `s.hub.addSubscriber(...)` / `defer s.hub.removeSubscriber(...)` lines if present.

Verify nothing else references them:

```bash
grep -rn "hub\." internal/server/serve/ | grep -v _test.go
```

Expected: no output.

- [ ] **Step 5: Run tests**

```bash
CGO_ENABLED=0 go test ./internal/server/... -count=1
```

Expected: PASS (websocket e2e tests unchanged — connections still receive replayed events via DB polling).

- [ ] **Step 6: Commit**

```bash
git add internal/server/master/ internal/server/serve/websocket.go internal/server/serve/server.go
git commit -m "master: graceful SIGTERM cancel; remove dead port-selection and hub code"
```

---

### Task 10: API contract doc + end-to-end verification

**Files:**
- Create: `docs/api-contract.md`
- Modify: `docs/index.md` (link the new doc, following its existing list style)

**Interfaces:**
- Consumes: everything above is done.
- Produces: the frozen `/api/v1` contract document promised by spec §9, plus a verified release build.

- [ ] **Step 1: Write `docs/api-contract.md`**

Structure (fill the endpoint list from `Handler()` in `internal/server/serve/server.go` — enumerate every route literally):

````markdown
# owl-migrate Web API Contract (v1)

Status: frozen for the 1.x line. Additive changes only (new optional fields,
new endpoints). Breaking changes ship as `/api/v2` in 2.0.

## Conventions

- Base path: `/api/v1`
- JSON requests/responses unless noted. Errors: `{"error": "<message>"}`
  with an appropriate 4xx/5xx status.
- Request bodies are limited: 2 MiB for config uploads, 1 MiB elsewhere.
  Oversized bodies receive 400 with a `request body too large` message.
- Same-origin only: no CORS headers are emitted; the Web UI is served from
  the same host.
- Authentication is delivered with Phase 1 (token middleware + SPA token
  prompt). Record it here when that phase lands; the contract reserves
  `Authorization: Bearer <token>` and `?token=` for WebSocket upgrades for
  that purpose.

## Security notes

- `GET /api/v1/config` masks DSN passwords (`config.MaskDSN`). Explicit file
  operations — `GET /api/v1/configs/{name}` (download),
  `POST /api/v1/configs/{name}/load` — return YAML verbatim because they are
  file-equivalent operations and masking would break save round-trips.
- Every generation run (DDL/SELECT/INSERT/metadata/export) is recorded in the
  job database; downloads resolve the newest recorded output for their kind
  and survive server restarts. Ten outputs per kind are retained.

## Endpoints

| Method & path | Purpose |
|---|---|
| GET /api/v1/health | Liveness; returns version/commit/build metadata |
| ... one row per route registered in `serve.Server.Handler()` ... |
````

Complete the table by copying each route pattern verbatim from `Handler()`.

- [ ] **Step 2: Link from docs index**

Add the entry to `docs/index.md` following the existing list formatting used there.

- [ ] **Step 3: Full test run**

```bash
CGO_ENABLED=0 go test ./... -count=1
```

Expected: PASS (the build-tagged sqlite3/duckdb e2e tests are skipped without tags).

- [ ] **Step 4: Format and vet**

```bash
go fmt ./... && CGO_ENABLED=0 go vet ./...
```

Expected: no changes reported by `git diff` after fmt; vet clean.

- [ ] **Step 5: Release build + static check**

```bash
make build/linux && file build/linux-amd64/owl-migrate
make build && ./build/darwin-arm64/owl-migrate version 2>/dev/null || ./build/darwin-arm64/owl-migrate --help | head -3
```

Expected: Linux ELF statically linked; native binary runs.

- [ ] **Step 6: Single-binary smoke test (native build)**

```bash
export OWL_MIGRATE_HOME=$(mktemp -d)
./build/darwin-arm64/owl-migrate serve --port 18080 &
SERVE_PID=$!
sleep 1
curl -sf localhost:18080/api/v1/health
curl -sf localhost:18080/docs/ | head -3
curl -sf -o /dev/null -w "%{http_code}\n" localhost:18080/
# second instance must refuse:
./build/darwin-arm64/owl-migrate serve --port 18081; echo "exit=$?"
kill $SERVE_PID
```

Expected: health JSON; docs HTML; `200` for the UI; second instance exits non-zero with the lock message.

- [ ] **Step 7: Commit docs**

```bash
git add docs/api-contract.md docs/index.md
git commit -m "docs: freeze /api/v1 contract and document security behavior"
```

---

## Exit Checklist (spec §11 Phase 0)

- [ ] `CGO_ENABLED=0 go test ./... -count=1` fully green
- [ ] `make build/linux` from macOS produces a statically linked ELF whose `serve` works (verify on a Linux host/container — steps in Task 10 cover the native smoke; run the same curl sequence inside `docker run --rm -v $PWD/build/linux-amd64:/bin/x debian:stable-slim` if Docker is available, otherwise verify on the target server at deploy time)
- [ ] All six P0 items closed: random suffix, persisted generation outputs, masked config read, same-origin + body limits, single-instance lock, embedded docs
- [ ] `docs/api-contract.md` merged
