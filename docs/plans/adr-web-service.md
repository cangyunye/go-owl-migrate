# Architecture Decision Records — owl-migrate Web Service

## ADR-001: Progress Reporting Pattern for Service Layer

**Status**: Accepted  
**Date**: 2026-07-26  

### Decision

Progress events flow through a shared SQLite3 database (`owl-migrate.db`) with a three-table schema: `jobs`, `job_checkpoints`, `progress_events`. Both CLI and Web consumers read from the same tables. Per-table granularity — N tables produce N progress events. Events are written in transactions with `BEGIN IMMEDIATE` and a `_busy_timeout=5000` SQLite pragma.

### Rationale

- Single-file SQLite in WAL mode provides concurrent reads without blocking, serialized writes are acceptable for the low-frequency progress rate (one event per table completion).
- Three-table separation gives clean concerns: job lifecycle, per-table checkpoint state, and event stream.
- Avoids callbacks or channels that couple the service layer to specific consumers.
- Already has sqlite3 in the dependency tree via `go-sqlite3` driver.

### Schema

```sql
CREATE TABLE jobs (
    job_id      TEXT PRIMARY KEY,
    type        TEXT NOT NULL,  -- migrate | export | import
    status      TEXT NOT NULL,  -- running | completed | interrupted | failed | cancelling
    config      TEXT,           -- JSON snapshot
    pid         INTEGER,
    created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMP
);

CREATE TABLE job_checkpoints (
    job_id        TEXT NOT NULL,
    schema        TEXT NOT NULL,
    table_name    TEXT NOT NULL,
    exported      BOOLEAN DEFAULT FALSE,
    exported_rows INTEGER DEFAULT 0,
    imported      BOOLEAN DEFAULT FALSE,
    imported_rows INTEGER DEFAULT 0,
    status        TEXT,         -- SUCCESS | FAIL
    error         TEXT,
    PRIMARY KEY (job_id, schema, table_name)
);

CREATE TABLE progress_events (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id    TEXT NOT NULL,
    seq       INTEGER NOT NULL,
    event_type TEXT NOT NULL,   -- export_start | export_complete | import_start | import_complete | checkpoint
    schema    TEXT,
    table_name TEXT,
    rows      INTEGER,
    message   TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

---

## ADR-002: Job Persistence Strategy

**Status**: Accepted  
**Date**: 2026-07-26  

### Decision

All job state and progress is persisted in a single global SQLite3 file (`owl-migrate.db`) in the working directory. No per-job databases. On server startup, all jobs with `status = 'running'` are bulk-updated to `status = 'interrupted'`. Users may resume interrupted jobs via a "Resume" button which spawns a new job with checkpoint reuse.

### Rationale

- Single-file administration: one file to backup, clean, or inspect.
- SQLite WAL mode handles the expected write load (master writes job lifecycle changes infrequently, worker writes progress events at table granularity).
- No external database dependency — the binary remains self-contained.
- Resuming from checkpoint rather than mid-job "pause" avoids complex goroutine coordination.

---

## ADR-003: Cancellation Flow

**Status**: Accepted  
**Date**: 2026-07-26  

### Decision

Only cancel is supported (no mid-operation pause). Cancellation is a REST endpoint, not an in-band WebSocket message. The flow:

1. User clicks "Cancel" → `DELETE /api/v1/jobs/{id}` on serve
2. Serve relays to master via IPC: `DELETE http://127.0.0.1:<ipc>/api/v1/jobs/{id}`
3. Master sends `SIGTERM` to the worker PID
4. Worker catches signal → cancels context → writes final checkpoint → updates `jobs.status = 'cancelled'`
5. WebSocket receives final `cancelled` event

### Rationale

- WebSocket is reserved for progress streaming, not control commands.
- REST cancellation is idempotent and observable (HTTP status codes).
- No need for custom pause/resume channels in the export/import pipeline — simpler context cancellation.

---

## ADR-004: WebSocket Protocol

**Status**: Accepted  
**Date**: 2026-07-26  

### Decision

Use `coder/websocket` Go library. WebSocket is unidirectional (server→client) for progress streaming. One connection per job viewer. Protocol is JSON messages:

```
Client → ws://host/api/v1/jobs/{id}/ws  (connect only, no initial message)

← {"type":"progress","seq":1,"event":"export_start","schema":"SCOTT","table":"EMP"}
← {"type":"progress","seq":2,"event":"export_complete","schema":"SCOTT","table":"EMP","rows":5000}
← {"type":"checkpoint","schema":"SCOTT","table":"EMP","exported":true,"exported_rows":5000}
← {"type":"complete","status":"SUCCESS","report":{...}}
← {"type":"error","status":"FAIL","error":"connection refused"}
← {"type":"cancelled","status":"CANCELLED"}
```

Server closes WebSocket cleanly after a terminal event.

### Rationale

- `coder/websocket` has clean context integration, minimal API surface, active maintenance.
- JSON messages are human-readable and debuggable.
- Non-terminal events are append-only; client has no state machine — just render what arrives.

---

## ADR-005: Frontend Architecture

**Status**: Accepted  
**Date**: 2026-07-26  

### Decision

Multi-page HTML with Go `html/template` for server-side skeleton rendering, JS for client-side data fetching and DOM manipulation. No frontend framework. No Node.js build step. All templates and static assets embedded via `//go:embed`. Routing uses Go 1.22+ `net/http` enhanced ServeMux.

### Rationale

- The frontend is a draft for a professional designer to restyle. Plain HTML+CSS+JS is the easiest to hand off.
- No build pipeline: `go build` is the entire build.
- Server-side template rendering for page skeleton avoids a JS router.
- JS fetches data from API endpoints and renders dynamic content (progress bars, tables, forms).

### Template inventory

`templates/base.html` — shared layout with sidebar navigation
`templates/index.html` — scenario entry page (card-based navigation)
`templates/config.html` — config form (JS-populated)
`templates/migrate.html` — migration launcher with WebSocket progress
`templates/export.html` — export launcher
`templates/import.html` — import launcher
`templates/jobs.html` — job history table
`templates/job-detail.html` — per-job detail: checkpoints + events
`templates/ddl.html` — DDL generator with preview
`static/css/style.css` — shared stylesheet
`static/js/lib.js` — shared JS: API client, WebSocket helper

---

## ADR-006: Process Architecture — Three-Process Model

**Status**: Accepted  
**Date**: 2026-07-26  

### Decision

`owl-migrate serve` starts a three-process tree:

```
owl-migrate serve
  ├── Master process
  │   ├── Manages SQLite (initialize, schema migrations)
  │   ├── Binds IPC HTTP server (auto-selected port, see ADR-007)
  │   ├── Spawns Serve child process
  │   ├── Accepts job requests from Serve via IPC
  │   ├── Spawns Worker child processes for migrate/export/import
  │   ├── Monitors Worker PIDs via heartbeat file
  │   └── Graceful shutdown: SIGTERM → Serve → Workers → exit
  │
  ├── Serve process (child of Master)
  │   ├── Binds user-facing HTTP (configured port, see ADR-007)
  │   ├── Serves templates + static files
  │   ├── Reads SQLite (read-only) for job data
  │   ├── Relays job commands to Master via IPC HTTP
  │   └── Manages WebSocket hub with per-job fan-out
  │
  └── Worker process (child of Master, one per job)
      ├── Runs owl-migrate migrate/export/import with --progress-db --job-id --parent-pid
      ├── Writes progress_events + job_checkpoints to shared SQLite
      ├── Checks parent PID liveness via heartbeat file
      └── Writes final status to jobs table on exit
```

### Rationale

- Crash isolation: a failing migration does not crash the web UI.
- The web UI can be restarted independently; workers continue running.
- Master as the single spawn point avoids PID management race conditions.
- Workers naturally inherit the CLI's behavior — same binary, same flags, same config file.

### Operations that run as Worker child processes

- `migrate` (end-to-end pipeline)
- `export data` (online DB export)
- `import data` (CSV → target DB)

### Operations that run synchronously in-process (Serve)

- `validate` (metadata validation)
- `export ddl` (DDL generation)
- `gen-select` (SELECT generation)
- `export insert` (INSERT SQL generation)

---

## ADR-007: Port Allocation and Configuration Priority

**Status**: Accepted  
**Date**: 2026-07-26  

### Decision

**Serve port** (user-facing, for browser):
1. CLI flag: `owl-migrate serve --port 8080 --host 0.0.0.0`
2. `.env` file: `OWL_MIGRATE_SERVE_HOST`, `OWL_MIGRATE_SERVE_PORT`
3. Environment variables: `OWL_MIGRATE_SERVE_HOST`, `OWL_MIGRATE_SERVE_PORT`
4. Default: `127.0.0.1:8080`

**Master IPC port** (internal, for serve↔master communication):
1. CLI flag: `owl-migrate serve --master-ipc-port 25430`
2. `.env` file: `OWL_MIGRATE_MASTER_IPC_PORT`
3. Environment variable: `OWL_MIGRATE_MASTER_IPC_PORT`
4. Auto-select: try 25430-25439 → 25400-25499 → 25000-25999 → 26000+ (increment by 1) → error prompt user to specify

**Startup sequence**:
1. Master binds IPC port
2. Master writes IPC URL to env before spawning Serve
3. Master spawns Serve as child
4. Serve verifies IPC health: `GET http://127.0.0.1:<ipc>/health`
5. Serve binds user-facing port
6. Serve opens browser

### Environment variables (.env file)

```env
OWL_MIGRATE_SERVE_HOST=127.0.0.1
OWL_MIGRATE_SERVE_PORT=8080
OWL_MIGRATE_MASTER_IPC_PORT=25430
OWL_MIGRATE_TEMP_DIR=./output/temp/
OWL_MIGRATE_LOG_LEVEL=info
```

### Rationale

- `OWL_MIGRATE_` prefix avoids collisions with `go-owl`, `go-owl-metrics`, `go-owl-tui`.
- Auto-selection with a known range (25430+ is unassigned IANA range) minimizes user configuration.
- Fallback chain ensures the server always starts without port conflicts.

---

## ADR-008: Master-Serve IPC Protocol

**Status**: Accepted  
**Date**: 2026-07-26  

### Decision

IPC between Serve and Master uses HTTP over localhost loopback (`127.0.0.1`). JSON payloads.

### Endpoints (Master IPC server)

**POST /api/v1/jobs** — Start a new job
```json
// Request
{
  "type": "migrate",
  "config": { /* full Config struct */ },
  "resume_from": "job-abc123"   // optional
}
// Response 201
{
  "job_id": "job-def456",
  "pid": 12345,
  "status": "running",
  "created_at": "2026-07-26T10:00:00Z"
}
```

**DELETE /api/v1/jobs/{id}** — Cancel a running job
```json
// Response 200
{
  "job_id": "job-def456",
  "status": "cancelling"
}
```

**GET /health** — Liveness check
```json
// Response 200
{"status": "ok", "uptime": "1h23m"}
```

### Rationale

- HTTP over localhost is cross-platform (Windows/Linux/macOS) with zero OS-specific IPC code.
- JSON is debuggable with `curl` during development.
- No new dependencies — `net/http` standard library.

---

## ADR-009: WebSocket Hub Architecture

**Status**: Accepted  
**Date**: 2026-07-26  

### Decision

Global hub pattern — one `Hub` struct with `map[jobID]*SubscriberSet`. A background goroutine polls SQLite `progress_events` for new events (500ms interval per job with active subscribers) and fans out to all WebSocket connections for that job.

On WebSocket connect:
1. Client sends no initial message — just opens `ws://host/api/v1/jobs/{id}/ws`
2. Server replays all existing events for the job (catch-up for reconnects)
3. Server begins streaming new events as they arrive

On WebSocket disconnect or send failure (3 consecutive failures):
1. Connection removed from subscriber set
2. If subscriber set becomes empty, stop polling for that job

If WebSocket reconnects:
1. Server replays all events from seq=1
2. Client deduplicates by `seq` field
3. Streaming resumes

### Rationale

- Global hub is simpler to reason about than per-job hub lifecycle management.
- Poll-based event fan-out from SQLite avoids complex channel plumbing between worker and WebSocket layer.
- Three-failure removal prevents dead connections from accumulating.
- Catch-up replay handles browser refresh, tab close/reopen, and serve restart transparently.

---

## ADR-010: Config Wizard Page Design

**Status**: Accepted  
**Date**: 2026-07-26  

### Decision

The homepage presents scenario entry cards (not a dropdown). Each card maps to a `buildScenarioConfig()` scenario from the CLI `init` command:

| Card | Scenario | Config sections populated |
|---|---|---|
| "Migrate" | migrate | metadata, source, target, ddl, export, import |
| "Export DDL" | export-ddl | metadata, source (if database), ddl |
| "Generate SELECT" | gen-select | metadata, source (if database), ddl, select_gen |
| "Export Data" | export | metadata, source, export |
| "Import Data" | import | metadata, target, ddl, import |
| "Generate INSERT" | export-insert | metadata, ddl, import.csv |
| "Validate" | validate | metadata, source, ddl |
| "Full Config" | full | all 8 sections |

Clicking a card navigates to the config form pre-populated with that scenario's defaults. The user fills in only the relevant fields for that scenario.

### Rationale

- Mirrors the CLI's `init --scenario <name>` exactly.
- Each scenario has different required fields — showing only relevant ones reduces user confusion.
- The "Full Config" card exists for power users who want every option visible.

---

## ADR-011: Worker Progress Logging Format

**Status**: Accepted  
**Date**: 2026-07-26  

### Decision

Workers produce structured per-table progress logs at the following granularity:

**Export phase** (per table):
```
[EXPORT] SCOTT.EMP: starting (goroutine worker-1)
[EXPORT] SCOTT.EMP: batch 1, 5000 rows read
[EXPORT] SCOTT.EMP: batch 2, 5000 rows read
[EXPORT] SCOTT.EMP: completed, 14000 rows in 3.2s
```

**Import phase** (per table):
```
[IMPORT] SCOTT.EMP: starting (goroutine worker-1)
[IMPORT] SCOTT.EMP: batch 1, 1000 rows inserted (committed)
[IMPORT] SCOTT.EMP: batch 2, 1000 rows inserted (committed)
[IMPORT] SCOTT.EMP: batch 14, 1000 rows inserted, 2 rows skipped
[IMPORT] SCOTT.EMP: completed, 13998/14000 rows, 2 skipped, 0 errors in 5.1s
```

**Error reporting**:
```
[IMPORT] SCOTT.DEPT: ERROR batch 12 row 45: duplicate key violation (PK=40)
[IMPORT] SCOTT.DEPT: ERROR batch 12 row 89: value too long for column DNAME(14 bytes, max 10)
```

**Final summary** (terminal table):
```
+-------------+----------+-----------+--------+---------+--------+--------+
| SCHEMA      | TABLE    | EXPORTED  | IMPORT | SKIPPED | ERRORS | STATUS |
+-------------+----------+-----------+--------+---------+--------+--------+
| SCOTT       | EMP      |    14,000 | 13,998 |       2 |      0 | PASS   |
| SCOTT       | DEPT     |         4 |      0 |       0 |      2 | FAIL   |
| SCOTT       | BONUS    |         0 |      0 |       0 |      0 | PASS   |
+-------------+----------+-----------+--------+---------+--------+--------+
TOTAL: 3 tables, 14,004 rows exported, 13,998 rows imported, 2 skipped, 2 errors
```

**Logging decisions**:
- Batch-level for import (every `commit_interval` rows): useful for long tables. Export batch-level only if `page_size >= 10000`; otherwise only start/complete.
- Goroutine naming: `[EXPORT] schema.table (worker-N)` — N is the exporter worker pool index. Helps trace which goroutine handles which table in parallel mode.
- Skipped rows: logged per batch summary (not per row) to avoid log flooding.
- Errors: logged immediately with row context for debugging. Repeated identical errors are deduplicated (max 10 per table).
- Final summary: always printed regardless of success/failure. Uses Unicode box-drawing characters.

### Rationale

- Batch-level import progress is essential for large tables (100k+ rows with commit_interval=1000 → 100 log lines per table is reasonable).
- Export progress only emits start/complete for most tables; batch-level only for tables with page_size ≥ 10000 to keep log output proportional.
- Error deduplication avoids "1000 duplicate key errors" flooding the log while still capturing the first 10 occurrences for root cause analysis.
- Terminal table format matches the existing `MigrationReport.Print()` style.

---

## ADR-012: Worker Flags for Progress Integration

**Status**: Accepted  
**Date**: 2026-07-26  

### Decision

Three new flags added to `migrate_cmd.go`, `export_data.go`, and `import.go`:

```
--progress-db <path>     Path to shared SQLite database for progress events
--job-id <id>            Job identifier for this run
--parent-pid <pid>       Master process PID for orphan detection
```

When `--progress-db` is set, the worker opens the SQLite database (read-write, `_busy_timeout=5000`, WAL mode) and writes `progress_events` and `job_checkpoints` rows atomically in transactions on each table completion.

When all three flags are absent, behavior is identical to the current CLI (no SQLite writes, only `fmt.Printf` and `migrate_progress.json`).

### Rationale

- Zero breaking changes to existing CLI usage.
- Same flags for migrate, export, and import — uniform worker behavior.
- Existing `migrate_progress.json` checkpoint logic is preserved; `job_checkpoints` table is an additional copy written to the shared database for the web UI to consume.

---

## ADR-013: Worker Parent-Death Detection

**Status**: Accepted  
**Date**: 2026-07-26  

### Decision

Cross-platform heartbeat file mechanism:

1. Master writes `/tmp/owl-migrate-master.heartbeat` every 5 seconds containing its PID and a Unix timestamp.
2. Worker reads the heartbeat file every 10 seconds.
3. If heartbeat is >20 seconds stale (2 missed intervals), master is presumed dead.
4. On detecting master death, worker finishes its current table (if export/import is mid-table), writes final checkpoint, updates `jobs.status = 'interrupted'`, and exits cleanly.

### Rationale

- Heartbeat file is cross-platform: works on Windows, Linux, and macOS without OS-specific `/proc` parsing.
- Writing a small file every 5 seconds is negligible I/O.
- The 20-second grace period prevents false positives from temporary I/O stalls.
- Worker finishes current table rather than aborting mid-row — prevents partially written data.

---

## ADR-014: Serve SQLite Read Access

**Status**: Accepted  
**Date**: 2026-07-26  

### Decision

Serve process opens `owl-migrate.db` in read-only mode for querying job data directly. It does not write to SQLite. All writes go through Master (job lifecycle) or Worker (progress events, checkpoints).

Serve queries:
```sql
SELECT * FROM jobs ORDER BY created_at DESC LIMIT 100;
SELECT * FROM job_checkpoints WHERE job_id = ? ORDER BY schema, table_name;
SELECT id, seq, event_type, schema, table_name, rows, message FROM progress_events
    WHERE job_id = ? AND seq > ? ORDER BY seq;
```

### Rationale

- Read-only access means no lock contention with Master or Worker writers.
- Direct SQLite reads avoid IPC overhead for job list and progress queries.
- Simpler than proxying all reads through Master — Serve is already a Go process, `database/sql` import is trivial.

---

## ADR-015: SQLite Lock Debugging

**Status**: Accepted  
**Date**: 2026-07-26  

### Decision

SQLite connection configuration:
```
file:owl-migrate.db?_busy_timeout=5000&_journal_mode=WAL
```

A `/debug/db-stats` endpoint on Serve exposes:
```json
{
  "wal_size_bytes": 4096,
  "open_connections": 3,
  "running_jobs": 2,
  "last_write_latency_ms": 12
}
```

Application-level instrumentation: SQLite write operations wrapped with `context.Context` timeout (10s). If a write exceeds 100ms, log a warning with the operation details. If a write times out, return `context.DeadlineExceeded` rather than blocking indefinitely.

### Rationale

- WAL mode is the primary mitigation — readers never block writers.
- `_busy_timeout=5000` gives writers 5 seconds to acquire the lock before returning `SQLITE_BUSY`.
- `/debug/db-stats` provides operational visibility without requiring `sqlite3` CLI access on the server.
- Per-operation context timeouts prevent hung writes from blocking the entire process.

---

## ADR-016: Service Layer Refactoring Scope

**Status**: Accepted  
**Date**: 2026-07-26  

### Decision

The following currently-private functions in `internal/cmd/` are moved to `internal/service/` and exported:

| Function | New location | Purpose |
|---|---|---|
| `loadSchemaModel` | `MetadataService.Load` | Load metadata from config |
| `openDB` | `ConfigService.OpenDB` | Open sql.DB from DBConfig |
| `buildPKMap` | `MetadataService.BuildPKMap` | Build primary key map for cursor pagination |
| `filterTables` | `MetadataService.FilterTables` | Filter tables by include list |
| `toBuildOptions` | `ConfigService.ToBuildOptions` | Convert Config → dialect.BuildOptions |
| `connectTimeout` | `ConfigService.ConnectTimeout` | Parse connect timeout from DBConfig |
| `newLogger` | `ConfigService.NewLogger` | Create zap.Logger from config |

All other internal packages (`config`, `metadata`, `dialect`, `registry`, `generator`, `transfer/exporter`, `transfer/importer`) are used directly — no changes needed.

The `migrate_cmd.go` RunE is partially refactored: the pipeline orchestration (steps 1-7) moves to `MigrateService.Run()`, but the CLI's `fmt.Printf` formatting and report printing remain in the CLI layer. The service returns structured results.

### Rationale

- Only the dispatch functions need exporting — the heavy logic lives in already-public internal packages.
- Exporter and Importer already have clean constructors and config structs — no changes needed for the web layer to use them.
- The migrate pipeline extraction is the biggest change but is scoped to what the CLI layer currently does with `fmt.Printf` vs what the web layer needs (structured JSON progress).

---

## ADR-017: Module Layout

**Status**: Accepted  
**Date**: 2026-07-26  

### Decision

New directory structure:

```
cmd/migrate/main.go
internal/
  cmd/
    serve.go              # Cobra command "serve" (master process entry)
  server/
    master/
      master.go           # Master process: SQLite init, IPC HTTP, worker spawn, PID monitor
      worker_monitor.go   # PID heartbeat monitoring
    serve/
      server.go           # Serve process: user-facing HTTP, templates, static files
      handlers.go         # API handlers
      middleware.go        # CORS, logging, recovery
      websocket.go        # WebSocket hub and connection management
      sse.go              # [DEPRECATED - replaced by WebSocket]
  service/
    migrate.go            # MigrateService
    config.go             # ConfigService
    metadata.go           # MetadataService
    job.go                # JobManager (SQLite operations)
templates/                # Go html/template files
static/                   # CSS, JS files
```

### Rationale

- `internal/server/master/` and `internal/server/serve/` are separate packages because they run in different processes with different responsibilities.
- `internal/server/serve/` handles the web API. `internal/server/master/` handles process management.
- `internal/service/` is shared between master and serve processes — it knows nothing about HTTP or processes.

---

## ADR-018: Config Distribution to Workers

**Status**: Accepted  
**Date**: 2026-07-26  

### Decision

Master receives a config JSON from Serve via IPC. Master serializes it to YAML at `/tmp/owl-migrate-jobs/<job_id>/config.yaml` and passes `--config <path>` to the worker. The worker calls `config.Load(path)` — zero changes to config loading.

Worker flags passed by master:
```
owl-migrate migrate \
  --config /tmp/owl-migrate-jobs/job-abc123/config.yaml \
  --progress-db /path/to/owl-migrate.db \
  --job-id job-abc123 \
  --parent-pid <master_pid> \
  --temp-dir /tmp/owl-migrate-jobs/job-abc123/
```

### Rationale

- Workers reuse the exact same `config.Load()` path as the CLI — no new config parsing code.
- Temp directory per job keeps export artifacts isolated.
- Config file on disk is inspectable for debugging.
