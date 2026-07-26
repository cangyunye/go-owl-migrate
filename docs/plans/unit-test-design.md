# Unit Test Design — owl-migrate Web Service

## Conventions

Follow existing codebase conventions:
- **Standard `testing` package only** — no testify, no gomock, no external assertion libraries.
- **Table-driven tests with `t.Run`** for multi-case functions.
- **`t.Helper()`** on all fixture/setup functions.
- **`t.TempDir()`** for all temporary files/directories.
- **`t.Cleanup()`** for resource teardown (DB handles, temp files, goroutine shutdown).
- **`github.com/DATA-DOG/go-sqlmock v1.5.2`** (already in `go.mod`) for fake SQL databases.
- **`net/http/httptest`** (stdlib) for HTTP handler testing.

## Modules Under Test

### 1. `internal/service/config_test.go`

**Test setup**: `t.TempDir()` for writing YAML files, `os.WriteFile` to seed config inputs.

| Test | Description | Input | Expected |
|------|-------------|-------|----------|
| `TestConfigService_LoadFromBytes` | Parse and validate valid YAML | Bytes of valid `migrate.example.yaml` | Returns `*Config` with all sections populated |
| `TestConfigService_LoadFromBytes_Invalid` | Parse invalid YAML | Bytes with bad target_dialect | Returns error containing validation message |
| `TestConfigService_ToYAML` | Round-trip: parse → serialize → parse → equal | Any valid config | Second parse equals first |
| `TestConfigService_DefaultConfig` | Returns config with all defaults | Call `DefaultConfig()` | `TargetDialect=""`, `LogLevel="info"`, nil sections omitted |
| `TestConfigService_MergePatch` | Partial update via JSON patch | Base config + `{"ddl":{"include_comments":true}}` | Only `IncludeComments` changes, rest unchanged |
| `TestConfigService_ToBuildOptions` | Convert `Config` → `dialect.BuildOptions` | Config with `SchemaMapping`, `IncludeIfNotExists`, etc. | `BuildOptions` struct matches |
| `TestConfigService_ConnectTimeout` | Parse timeout string from `DBConfig` | `"30s"`, `""`, `"5m"` | `30s`, `0`, `5m` durations |
| `TestConfigService_OpenDB` (table-driven) | Build `*sql.DB` per dialect | oracle, postgres, mysql, sqlite3 DSNs | Non-nil `*sql.DB`, no connect attempt in unit test |

**Mock DB usage**: Use `sqlmock.New()` to verify DSN parsing path when constructing config: `config_test.go:375` (`config.Load`) already has tests for that.

**Prior art**: `internal/config/config_test.go` — YAML parse + table filter matching.

---

### 2. `internal/service/metadata_test.go`

**Test setup**: `t.TempDir()` with CSV files copied from `testdata/csv/`.

| Test | Description | Input | Expected |
|------|-------------|-------|----------|
| `TestMetadataService_Load_CSV` | Load from CSV metadata directory | Config with `metadata.type=csv`, path to `testdata/csv/` | SchemaModel with EMP, DEPT, BONUS tables |
| `TestMetadataService_Load_XLSX` | Load from XLSX metadata file | Config with `metadata.type=xlsx`, path to `testdata/xlsx/` | SchemaModel with expected tables |
| `TestMetadataService_Load_CSV_CaseInsensitive` | Column name matching | Config with `column_name_matching=case_insensitive` | Columns matched regardless of CSV header case |
| `TestMetadataService_BuildPKMap` | Build PK map for cursor pagination | SchemaModel with EMP(PK=EMPNO), DEPT(PK=DEPTNO) | `map["SCOTT.EMP"]=["EMPNO"], map["SCOTT.DEPT"]=["DEPTNO"]` |
| `TestMetadataService_FilterTables` | Filter tables by include list | All tables + `["EMP", "DEPT"]` | Only EMP, DEPT returned |
| `TestMetadataService_FilterTables_Wildcard` | Wildcard include | All tables + `["*"]` | All tables returned |
| `TestMetadataService_FilterTables_ExcludeGlob` | Exclude by glob + include all | All tables + `include=["*"], exclude.glob=["*_LOG"]` | Tables matching `*_LOG` filtered out |

**Prior art**: `internal/metadata/csv/parser_test.go` + `internal/metadata/csv/loader_test.go` — CSV parsing. This service wraps existing functionality, so tests verify correct dispatch and error handling, not re-test CSV parsing itself.

---

### 3. `internal/service/migrate_test.go`

**Test setup**: uses `go-sqlmock` for source/target DB interactions. Creates a `MigrateService` with mock connections.

| Test | Description | Input | Expected |
|------|-------------|-------|----------|
| `TestMigrateService_Run_SQLOutputMode` | Run migration in SQL output mode | Config with `sql_out` flag, mock source DB with 2 tables | Export runs, INSERT SQL files generated to temp dir |
| `TestMigrateService_Run_DirectMode` | Run full export→import pipeline | Config with source+target DBs, mock both | Export rows written to CSV, import reads CSV, writes to mock target |
| `TestMigrateService_Run_EmptyTables` | Migrate with zero tables | SchemaModel with empty tables map | Succeeds with 0 rows, empty report |
| `TestMigrateService_Run_ExportError` | Export fails per table | Mock source DB returns query error | Error propagated, partial checkpoint saved |
| `TestMigrateService_Run_CancelContext` | Context cancelled mid-migration | `ctx, cancel := context.WithCancel(...); cancel()` after export start | Context error propagated, no import executed |

**Prior art**: `internal/transfer/importer/importer_policy_test.go` — sqlmock-based DB interaction pattern.

---

### 4. `internal/service/job_test.go` (SQLite Operations)

**Test setup**: real SQLite in `t.TempDir()` / `:memory:`. SQLite driver already required for these tests.

| Test | Description | Input | Expected |
|------|-------------|-------|----------|
| `TestJobManager_CreateJob` | Insert a job row | Job type, config JSON | Row exists in `jobs` table with status `running` |
| `TestJobManager_WriteEvent` | Insert progress event | Job ID, event data | Row exists in `progress_events` with auto-incremented seq |
| `TestJobManager_WriteCheckpoint` | UPSERT checkpoint | `(job_id, schema, table)` + data | Row updated on second call with same key |
| `TestJobManager_GetJob` | Read job by ID | Valid job ID | Returns `Job` struct with all fields |
| `TestJobManager_ListJobs` | List all jobs ordered | 3 jobs inserted out of order | Returned in `created_at DESC` |
| `TestJobManager_GetEvents` | Paginated event read | Job ID + last seen seq | Returns only events with `seq > last_seen_seq` |
| `TestJobManager_MarkInterrupted` | Bulk update running→interrupted | 2 running + 1 completed jobs | 2 updated, 1 unchanged |
| `TestJobManager_ConcurrentWrites` | Concurrent event writes | 5 goroutines each inserting 100 events | No SQLITE_BUSY, all rows present, seq ordered |

**Prior art**: No existing SQLite tests in project, but standard `database/sql` + `go-sqlite3` pattern.

---

### 5. `internal/server/serve/handlers_test.go`

**Test setup**: `httptest.NewServer` with a handler that wraps the Serve API router. Mock the service layer (or use a test SQLite).

| Test | Description | Input | Expected |
|------|-------------|-------|----------|
| `TestHandlers_GetJobs` | List jobs endpoint | `GET /api/v1/jobs` | 200 + JSON array |
| `TestHandlers_GetJob` | Get single job | `GET /api/v1/jobs/{id}` (existing) | 200 + JSON job object |
| `TestHandlers_GetJob_NotFound` | Non-existent job | `GET /api/v1/jobs/nonexistent` | 404 |
| `TestHandlers_PostConfig` | Save config | `PUT /api/v1/config` with valid JSON | 200 |
| `TestHandlers_PostConfig_Invalid` | Invalid config | `PUT /api/v1/config` with bad YAML | 400 + validation errors |
| `TestHandlers_GetDialects` | List dialects | `GET /api/v1/dialects` | 200 + array of dialect names |
| `TestHandlers_GetConfig` | Get current config | `GET /api/v1/config` | 200 + JSON config object |
| `TestHandlers_StaticFiles` | Serve embedded static | `GET /static/css/style.css` | 200 + CSS content |
| `TestHandlers_SPAFallback` | SPA catch-all | `GET /config` (no template) | Falls back to `index.html` |

**Prior art**: No existing HTTP tests in the project. Stdlib `net/http/httptest` is the established Go pattern.

---

### 6. `internal/server/serve/websocket_test.go`

**Test setup**: `httptest.NewServer` with WebSocket upgrade handler. Use `coder/websocket` client to connect.

| Test | Description | Input | Expected |
|------|-------------|-------|----------|
| `TestWebSocket_CatchUpReplay` | Replay existing events on connect | Pre-populated events in DB | Client receives all events in seq order |
| `TestWebSocket_StreamNewEvents` | Stream new events as they arrive | Connect first, then write event to DB | Client receives new event within 1 second |
| `TestWebSocket_NoEvents` | Connect to job with no events | Empty progress_events for job | Connection accepted, no messages sent until event arrives |
| `TestWebSocket_MultipleClients` | Two clients watch same job | 2 WebSocket connections, 1 event written | Both clients receive the event |
| `TestWebSocket_DisconnectEviction` | Client disconnect removes subscriber | Connect, close client, write event | No broadcast attempt for disconnected client |
| `TestWebSocket_ThreeFailures` | Three send failures evict | Mock a connection that fails writes | Subscriber removed from set |
| `TestWebSocket_ReconnectDeduplication` | Reconnect replays all + client deduplicates | Disconnect after 3 events, reconnect | Client receives seq 1-3 again + new events |

**Prior art**: Net-new. Use `coder/websocket` client in tests, SQLite for event storage.

---

### 7. `internal/server/master/master_test.go`

| Test | Description | Input | Expected |
|------|-------------|-------|----------|
| `TestMaster_SpawnWorker` | Create worker subprocess | Job type "export" + config + `--progress-db` | Worker PID recorded in SQLite, status `running` |
| `TestMaster_CancelWorker` | Cancel via IPC | `DELETE /api/v1/jobs/{id}` on master IPC | Worker receives SIGTERM, status becomes `cancelled` |
| `TestMaster_WorkerExitsSuccess` | Worker completes naturally | Worker writes `status='completed'` to SQLite on exit | Master detects exit via PID monitor, status updated |
| `TestMaster_WorkerExitsFailure` | Worker exits with error | Worker writes `status='failed'` to SQLite on exit | Master detects exit, status updated |
| `TestMaster_WorkerCrash` | Worker killed without writing status | `kill -9` worker PID | PID monitor detects dead PID, marks `interrupted` |
| `TestMaster_PortSelection` | Auto-select IPC port | No port specified, ports in 25430-25439 range | Selects an available port |
| `TestMaster_PortSelection_Exhausted` | All tries fail | All ports in ranges occupied | Returns error prompting manual port |

**Prior art**: Net-new. Uses `os/exec` for spawning test workers (run `owl-migrate migrate` with `--dry-run` or a no-op mode for fast tests).

---

### 8. `internal/cmd/serve_test.go`

| Test | Description | Input | Expected |
|------|-------------|-------|----------|
| `TestServeCommand_Flags` | CLI flag parsing | `serve --port 9090 --host 0.0.0.0` | Correct flag values parsed |
| `TestServeCommand_DefaultPorts` | Default values | `serve` (no flags) | port=8080, host=127.0.0.1 |

**Prior art**: `internal/cmd/export_test.go` — Cobra command registration tests.

---

### 9. Worker `--progress-db` flag

| Test | Description | Input | Expected |
|------|-------------|-------|----------|
| `TestWorker_ProgressDB_Writes` | Worker writes to SQLite | Worker with `--progress-db --job-id` | `progress_events` and `job_checkpoints` rows exist |
| `TestWorker_ProgressDB_Absent` | Worker without flags | Worker without `--progress-db` | No SQLite writes, behavior identical to current CLI |
| `TestWorker_ParentDeath_Heartbeat` | Worker detects stale heartbeat | Remove heartbeat file | Worker writes `interrupted`, exits within 20s |
| `TestWorker_ParentDeath_Alive` | Worker with live heartbeat | Heartbeat file updated every 5s | Worker runs normally |

---

### 10. Session-level DB test

| Test | Description |
|------|-------------|
| `TestSessionDB_BusyTimeout` | Writer blocked → waits within timeout → succeeds |
| `TestSessionDB_BusyTimeoutExceeded` | Writer blocked >5000ms → returns SQLITE_BUSY |
| `TestSessionDB_WALMode` | Verify journal_mode=wal is set |

---

## Test File Inventory (Net-New)

```
internal/service/
  config_test.go          # 8 test functions
  metadata_test.go        # 6 test functions
  migrate_test.go         # 5 test functions
  job_test.go             # 8 test functions (SQLite)

internal/server/serve/
  handlers_test.go        # 10 test functions
  websocket_test.go       # 7 test functions

internal/server/master/
  master_test.go          # 7 test functions

internal/cmd/
  serve_test.go           # 2 test functions
  worker_progress_test.go # 4 test functions (build tag: none, uses SQLite)
  session_db_test.go      # 3 test functions (build tag: none, uses SQLite)
```

**Total**: ~60 unit test functions across 10 test files.

---

## Test Data Fixtures

- Reuse `testdata/csv/` (SCOTT schema) for metadata loading tests.
- Use `t.TempDir()` for all generated files (config YAMLs, output directories, SQLite databases).
- For worker progress tests: compile a small `testdata/worker-config.yaml` or construct in-memory `*Config` structs using `buildScenarioConfig()`.
- No new test data files needed beyond what already exists.
