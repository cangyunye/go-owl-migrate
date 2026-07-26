# PRD: owl-migrate Web Service

## Problem Statement

As a DBA or data engineer using owl-migrate, I currently interact with the tool exclusively through the CLI. For complex multi-step migrations spanning dozens of tables across different database dialects, I need a visual interface that lets me compose configs, inspect metadata, preview generated DDL, trigger exports/imports, and monitor long-running migration jobs without hunting through terminal output. I also want to deploy a single self-contained binary with no external runtime dependencies — no separate web server or frontend build step required.

## Solution

A three-process architecture embedded in the `owl-migrate` binary. `owl-migrate serve` starts a **Master** process that manages a shared SQLite database and spawns child processes: a **Serve** process (user-facing web server) and **Worker** processes (one per migration/export/import job). The web UI is multi-page HTML with Go `html/template` for server-side rendering and vanilla JS for dynamic content — no frontend framework, no Node.js build step. All frontend assets are embedded via `//go:embed`.

There is no authentication layer — the service is intended for local or trusted-network use. Every CLI operation (validate, generate DDL, generate SELECT, export data, import data, full migrate pipeline) has a corresponding web endpoint and UI page.

The binary gains a new subcommand:

```
owl-migrate serve [--port 8080] [--host 127.0.0.1] [--master-ipc-port 25430]
```

Port configuration priority: CLI flags > `.env` file > `OWL_MIGRATE_*` environment variables > defaults.

> **Grill session findings**: See `docs/plans/adr-web-service.md` for 18 architecture decision records and `docs/plans/glossary-web-service.md` for the full terminology glossary produced during design review.

## User Stories

### Configuration

1. As a user, I want to create a new migration configuration through a form in the browser, so that I don't need to hand-write YAML.
2. As a user, I want to upload an existing `migrate.yaml` file via drag-and-drop, so that I can quickly resume work from a saved config.
3. As a user, I want to visually edit every section of the config (source, target, DDL, export, import, select_gen, metadata) with form validation, so that I catch misconfigurations before running a job.
4. As a user, I want to save my current config as a downloadable YAML file, so that I can version-control it or share it with colleagues.
5. As a user, I want the web UI to auto-detect available dialects from the registry and present them as dropdown choices, so that I don't need to memorize dialect names.
6. As a user, I want to toggle the `type_overrides` mapping entries with an add/remove UI, so that I can configure cross-dialect type conversions without YAML syntax errors.
7. As a user, I want to upload an external type-mapping YAML file and have the web UI surface the loaded mappings, so that I can reuse mappings across projects.

### Metadata Inspection

8. As a user, I want to load metadata from CSV files, an XLSX metadata workbook, or a live database, and see the resulting tables, columns, indexes, foreign keys, views, triggers, functions, sequences, and synonyms in the browser, so that I can verify the metadata before generating DDL or running a migration.
9. As a user, I want the metadata viewer to show column-level details (type, nullable, default, identity, comment), so that I can audit the source schema.
10. As a user, I want to see validation results (warnings and errors) rendered in the browser with severity highlighting, so that I can fix metadata issues before proceeding.
11. As a user, I want to filter and search the table list in the metadata viewer, so that I can focus on a subset of a large schema.
12. As a user, I want to see the primary-key map used for cursor pagination displayed per table, so that I understand which columns drive the export batching strategy.

### DDL Operations

13. As a user, I want to generate DDL for all object types (tables, indexes, views, sequences, triggers, functions, packages, synonyms, materialized views) and preview each generated file inline in the browser, with syntax highlighting for SQL, so that I can review DDL before downloading.
14. As a user, I want to download generated DDL as a ZIP archive of `.sql` files, so that I can apply them manually or store them in version control.
15. As a user, I want to select a target dialect from the available registry and regenerate DDL without restarting, so that I can compare what Oracle→PostgreSQL vs Oracle→MySQL DDL looks like.
16. As a user, I want to toggle DDL build options (include comments, IF NOT EXISTS, drop-if-exists, identity-to-serial, add ROWID column, split by object) as checkboxes and see the preview update, so that I can iterate on DDL settings quickly.
17. As a user, I want to view the schema mapping transform from the config applied to the DDL output, so that I can verify that `SCOTT`→`public` substitution is working.

### SELECT Generation

18. As a user, I want to generate paginated SELECT queries for all tables and preview them in the browser, so that I can verify the cursor or offset pagination logic.
19. As a user, I want to choose between cursor-based and offset-based pagination and see the generated SQL change, so that I can pick the appropriate strategy for my source database.
20. As a user, I want to adjust the page size and see the SELECT queries regenerate, so that I can tune batch sizes.
21. As a user, I want to download the generated SELECT files as a ZIP archive.

### Data Export

22. As a user, I want to trigger a live database export from the web UI with a single click, using the same `Exporter` pipeline the CLI uses, so that I don't need to switch to a terminal.
23. As a user, I want to see per-table export progress (rows exported, elapsed time, errors) updated in real time via Server-Sent Events (SSE), so that I can monitor a long-running export.
24. As a user, I want to cancel a running export job from the web UI, so that I can stop it if I realize the config is wrong.
25. As a user, I want to download exported CSV/SQL/XLSX files from the web UI after an export completes, so that I can inspect or reuse the output.
26. As a user, I want to trigger an offline export from uploaded CSV or XLSX source data, choosing the output format (CSV, SQL, XLSX), so that I can reformat data without a database connection.

### Data Import

27. As a user, I want to upload CSV files along with a table definition and trigger an import to a target database, so that I can load data through the UI.
28. As a user, I want to see per-table import progress (rows written, commit batches, errors) via SSE, so that I can monitor a large import job.
29. As a user, I want to configure import data transforms (datetime format, trim strings, null_if lists) through form fields, so that I can handle source data quirks without hand-editing YAML.

### Full Migration Pipeline

30. As a user, I want to trigger a complete end-to-end migration (metadata load → DDL create → export → import → report) from the web UI, so that I can run a full migration without touching the terminal.
31. As a user, I want to see the migration pipeline progress step-by-step (metadata loaded, tables created, exporting table X of Y, importing table X of Y) as a visual timeline, so that I understand what's happening at each stage.
32. As a user, I want to resume a failed migration from the web UI, loading the `migrate_progress.json` checkpoint, so that I don't need to re-export tables that already succeeded.
33. As a user, I want the migration report to display in the browser as a styled summary table (colored PASS/FAIL/PARTIAL status, per-table row counts, error messages), so that I can assess results at a glance.
34. As a user, I want to download the migration report as JSON.
35. As a user, I want to choose between direct mode (export→target DB) and SQL-output mode (export→INSERT SQL files) via a toggle in the UI.

### Job Management

36. As a user, I want to see a list of past jobs (migration, export, import) with their status, start time, duration, and a link to the report, so that I have an audit trail of operations performed through this instance.
37. As a user, I want to cancel a running job and have the underlying context cancelled, gracefully stopping the pipeline.
38. As a user, I want the web UI to persist job history and current job state across server restarts, so that a browser refresh or server crash doesn't lose track of running migrations.

### Cross-cutting

39. As a user, I want the web service to be a single static binary with zero install steps beyond downloading the binary, so that I can run it on air-gapped servers.
40. As a user, I want the web UI to work on modern browsers (Chrome/Firefox/Edge/Safari latest two versions), so that I can use my preferred browser.
41. As a user, I want to limit the listen address to `127.0.0.1` for local-only access, so that I can prevent network access to the no-auth service.
42. As a user, I want to specify the port on the command line, so that I can avoid port conflicts.
43. As a user, I want the serve command to accept the same `-c/--config` flag as other commands, so that I can preload a config when starting the web UI.
44. As a user, I want the web UI to expose structured log output in a collapsible log panel, so that I can inspect what the internal pipeline is logging without watching stdout.

## Implementation Decisions

### Process Architecture

`owl-migrate serve` starts a three-process tree. Full rationale in `docs/plans/adr-web-service.md` (ADR-006).

```
owl-migrate serve
  ├── Master process — SQLite init, IPC HTTP, worker spawn, PID monitoring
  ├── Serve process  — user-facing HTTP, templates, WebSocket hub
  └── Worker process — one per migrate/export/import job (child of Master)
```

- **Master** owns the SQLite database, binds an IPC HTTP server on `127.0.0.1` (auto-selected port, ADR-007), and spawns child processes. It is the only process that creates workers.
- **Serve** binds the browser-facing HTTP port, serves templates and static files from `//go:embed`, reads SQLite read-only for job data, and relays job start/cancel commands to Master via IPC HTTP. It manages a global WebSocket hub for progress fan-out.
- **Worker** is a child of Master. It runs the existing `owl-migrate` CLI with new flags (`--progress-db`, `--job-id`, `--parent-pid`), writes progress events and checkpoints to the shared SQLite, and detects Master death via heartbeat file.

Crash isolation: Serve crash does not affect running workers. Master crash is detected by workers via heartbeat; workers finish their current table and exit. Serve restart reattaches to running workers via SQLite state.

### Operations that become child processes

| Operation | Child Process? | Rationale |
|---|---|---|
| `migrate` | Yes | Long-running (10min-10hr), multi-step, checkpoint support |
| `export data` | Yes | Moderate duration (10s-10min), crash isolation |
| `import data` | Yes | Moderate duration, crash isolation |
| `validate` | No (in-process) | <1s |
| `export ddl` | No (in-process) | <1s |
| `gen-select` | No (in-process) | <1s |
| `export insert` | No (in-process) | <1s |

### Service Layer Refactoring

The following currently-private functions in `internal/cmd/` are moved to `internal/service/` and exported (ADR-016):

- `loadSchemaModel` → `MetadataService.Load`
- `openDB` → `ConfigService.OpenDB`
- `buildPKMap` → `MetadataService.BuildPKMap`
- `filterTables` → `MetadataService.FilterTables`
- `toBuildOptions` → `ConfigService.ToBuildOptions`
- `connectTimeout` → `ConfigService.ConnectTimeout`
- `newLogger` → `ConfigService.NewLogger`

All other internal packages (`config`, `metadata`, `dialect`, `registry`, `generator`, `transfer`) are used directly — no changes needed. The migrate pipeline extraction from `migrate_cmd.go` RunE into `MigrateService.Run()` is the largest change.

### State Store: SQLite3

Single global file `owl-migrate.db` in the working directory. WAL mode, `_busy_timeout=5000`. Three tables (ADR-001, ADR-002):

- **`jobs`** — one row per job. Fields: `job_id`, `type` (migrate/export/import), `status` (running/completed/interrupted/failed/cancelling/cancelled), `config` (JSON), `pid`, timestamps.
- **`job_checkpoints`** — one row per `(job_id, schema, table)`. Fields: `exported`, `exported_rows`, `imported`, `imported_rows`, `status`, `error`. Replaces `migrate_progress.json`.
- **`progress_events`** — append-only event log. Fields: `id`, `job_id`, `seq`, `event_type`, `schema`, `table_name`, `rows`, `message`, `created_at`. Per-table granularity.

Server startup: all `jobs WHERE status = 'running'` are bulk-updated to `'interrupted'`. Resume creates a new job reusing checkpoint data.

### Worker Flags Added to CLI

Three new flags added to `migrate_cmd.go`, `export_data.go`, and `import.go` (ADR-012):

```
--progress-db <path>     Path to shared SQLite database
--job-id <id>            Job identifier
--parent-pid <pid>       Master PID for orphan detection
```

When set, the worker writes to the shared SQLite in transactions. When absent, behavior is identical to current CLI. Zero breaking changes.

### Worker Progress Logging

Per-table granularity with goroutine naming (ADR-011). Export: start + batch-level (only if page_size ≥ 10000) + complete with row count and duration. Import: start + per-batch progress (every commit_interval rows) + complete with final tallies. Errors logged immediately with row context; repeated identical errors deduplicated (max 10 per table). Final summary printed as terminal table with Unicode box-drawing characters. Full spec in ADR-011.

### Frontend

Multi-page HTML with Go `html/template` for server-side skeleton rendering, vanilla JS for dynamic content. No frontend framework, no Node.js build step. All templates and static files embedded via `//go:embed`. Routing uses Go 1.22+ `net/http` enhanced ServeMux (ADR-005).

Template inventory:
```
templates/base.html        — shared layout (sidebar nav, header, footer)
templates/index.html       — scenario entry page (card-based navigation)
templates/config.html      — config form (JS-populated per scenario)
templates/migrate.html     — migration launcher with WebSocket progress
templates/export.html      — export launcher
templates/import.html      — import launcher
templates/jobs.html        — job history table
templates/job-detail.html  — per-job detail: checkpoints + events
templates/ddl.html         — DDL generator with preview
static/css/style.css       — shared stylesheet
static/js/lib.js           — shared JS: API client, WebSocket helper
```

The homepage presents scenario entry cards (ADR-010), each mapping to an `init --scenario` equivalent: Migrate, Export DDL, Generate SELECT, Export Data, Import Data, Generate INSERT, Validate, Full Config.

### REST API Design (Serve → Browser)

All endpoints under `/api/v1/`. JSON request/response. Long-running operations return a job ID and stream progress via WebSocket at `ws://host/api/v1/jobs/{id}/ws`.

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/api/v1/config/load` | Load config from uploaded YAML |
| `GET` | `/api/v1/config` | Get current config as JSON (full struct) |
| `PUT` | `/api/v1/config` | Save/update config from JSON body |
| `GET` | `/api/v1/config/download` | Download config as YAML file |
| `POST` | `/api/v1/metadata/load` | Load metadata and return SchemaModel as JSON |
| `GET` | `/api/v1/metadata/tables` | List tables with column summaries |
| `GET` | `/api/v1/metadata/tables/{schema}/{table}` | Full table definition |
| `GET` | `/api/v1/metadata/validate` | Return validation errors |
| `POST` | `/api/v1/ddl/generate` | Generate DDL; returns file list + content |
| `GET` | `/api/v1/ddl/download` | Download generated DDL as ZIP |
| `POST` | `/api/v1/select/generate` | Generate paginated SELECT |
| `GET` | `/api/v1/select/download` | Download SELECT files as ZIP |
| `POST` | `/api/v1/export` | Start an export job (returns job_id) |
| `POST` | `/api/v1/import` | Start an import job (returns job_id) |
| `POST` | `/api/v1/insert/generate` | Generate INSERT SQL; returns preview |
| `GET` | `/api/v1/insert/download` | Download INSERT files as ZIP |
| `POST` | `/api/v1/migrate` | Start a migration job (returns job_id) |
| `GET` | `/api/v1/jobs` | List all jobs |
| `GET` | `/api/v1/jobs/{id}` | Get job status and report |
| `DELETE` | `/api/v1/jobs/{id}` | Cancel a running job (relayed to Master) |
| `GET` | `/api/v1/dialects` | List registered dialects with features |
| `POST` | `/api/v1/mapping/load` | Load external type-mapping file |
| `GET` | `/api/v1/files/{jobId}/{filename}` | Download output file |
| `GET` | `/debug/db-stats` | SQLite diagnostic info (ADR-015) |

### IPC API Design (Serve → Master)

HTTP over `127.0.0.1` on auto-selected port. JSON payloads (ADR-008):

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/api/v1/jobs` | Start a new job (returns job_id + pid) |
| `DELETE` | `/api/v1/jobs/{id}` | Cancel job (sends SIGTERM to worker) |
| `GET` | `/health` | Master liveness check |

### WebSocket Progress Protocol

`coder/websocket` library. Single connection per job viewer. Server-to-client only. JSON messages (ADR-004):

```
← {"type":"progress","seq":1,"event":"export_start","schema":"SCOTT","table":"EMP"}
← {"type":"progress","seq":2,"event":"export_complete","schema":"SCOTT","table":"EMP","rows":5000}
← {"type":"checkpoint","schema":"SCOTT","table":"EMP","exported":true,"exported_rows":5000}
← {"type":"complete","status":"SUCCESS","report":{...}}
← {"type":"cancelled","status":"CANCELLED"}
```

On connect, server replays all existing events for that job (catch-up), then streams new ones. Client deduplicates by `seq`. Three consecutive send failures evict the connection from the subscriber set.

### WebSocket Hub

Global `Hub` with `map[jobID]*SubscriberSet` (ADR-009). Background goroutine polls SQLite `progress_events` for new rows (500ms interval per job with active subscribers) and fans out to all connections. Multiple browser tabs watching the same job each get their own WebSocket connection; all receive the same event stream.

### Cancel Mechanism

REST-based cancellation (ADR-003): `DELETE /api/v1/jobs/{id}` on Serve → relayed to Master via IPC → Master sends `SIGTERM` to Worker PID → Worker catches signal, finishes current table's batch, writes final checkpoint, updates `jobs.status = 'cancelled'`, exits. No mid-operation pause — only cancel + resume via new job with checkpoint reuse.

### Port Configuration

Priority chain (ADR-007):

**Serve port** (browser-facing): CLI `--port`/`--host` > `.env` `OWL_MIGRATE_SERVE_PORT`/`OWL_MIGRATE_SERVE_HOST` > environment variables > default `127.0.0.1:8080`

**Master IPC port** (internal): CLI `--master-ipc-port` > `.env` `OWL_MIGRATE_MASTER_IPC_PORT` > environment variable > auto-select 25430-25439 → 25400-25499 → 25000-25999 → 26000+ increment → error prompt

### Parent-Death Detection

Cross-platform heartbeat file at `/tmp/owl-migrate-master.heartbeat` (ADR-013). Master writes PID + timestamp every 5s. Worker reads every 10s. >20s staleness = master dead. Worker finishes current table and exits.

### Config Distribution

Master receives config JSON from Serve, serializes to YAML at `/tmp/owl-migrate-jobs/<job_id>/config.yaml`, passes `--config <path>` to worker. Worker calls `config.Load()` — zero changes to config loading (ADR-018).

### Module Layout

```
cmd/migrate/main.go
internal/
  cmd/
    serve.go                    # Cobra command "serve" (master entry point)
  server/
    master/
      master.go                 # Master: SQLite init, IPC HTTP, worker spawn
      worker_monitor.go         # PID heartbeat monitoring
    serve/
      server.go                 # Serve: user-facing HTTP, templates, static
      handlers.go               # API handlers
      middleware.go              # CORS, logging, recovery
      websocket.go              # WebSocket hub and connection management
  service/
    migrate.go                  # MigrateService
    config.go                   # ConfigService
    metadata.go                 # MetadataService
    job.go                      # SQLite operations (used by both master and serve)
templates/                      # Go html/template files
static/                         # CSS, JS files
```

### Non-functional Decisions

- **No router dependency**: Go 1.22+ `net/http` enhanced ServeMux with method+path patterns.
- **Middleware**: request logging (zap), panic recovery, CORS (allow all origins — no auth), request size limits for file uploads.
- **File uploads**: multipart form upload for CSV metadata, XLSX workbooks, source CSV data. Written to session-scoped temp directory.
- **Graceful shutdown**: SIGINT/SIGTERM → Master forwards to Serve → Workers. Master waits up to 30 seconds.
- **Environment variables**: `OWL_MIGRATE_` prefix for all env vars to avoid collisions with `go-owl` and `go-owl-metrics`.

## Testing Decisions

### What makes a good test

Tests verify external behavior through the HTTP API and IPC layer — send a request, assert the response status and body shape. No tests reach into the frontend. Service layer is tested through the API handlers. Internal package tests already cover core logic.

### Modules to test

| Module | Test Type | Prior Art |
|--------|-----------|-----------|
| `internal/server/serve/handlers.go` | Unit tests with `net/http/httptest` | Net-new |
| `internal/server/serve/websocket.go` | Unit test for hub fan-out + catch-up replay | Net-new |
| `internal/server/master/master.go` | Integration test: spawn serve, submit job via IPC, verify worker runs | Net-new |
| `internal/service/migrate.go` | Integration test with `go-sqlmock` | Similar to `migrate_cmd.go` logic |
| `internal/service/config.go` | Unit test for Load/MergePatch/ToYAML | Similar to `config/config_test.go` |
| `internal/service/job.go` | Unit test for concurrent create/read/cleanup with SQLite | Net-new |
| Worker `--progress-db` flag | Integration test: run migrate in worker mode, verify SQLite rows | Net-new |

### Key test scenarios

- `POST /api/v1/migrate` returns `201` with job ID; WebSocket streams progress events until `complete`.
- `DELETE /api/v1/jobs/{id}` over IPC cancels a running worker; worker writes `status = 'cancelled'`.
- WebSocket reconnect replays all existing events from `seq=1`; client handles deduplication.
- Three consecutive WebSocket send failures evict connection from subscriber set.
- Server startup: all `jobs WHERE status = 'running'` become `'interrupted'`.
- Worker heartbeat detection: removing heartbeat file causes worker to exit after 20s.
- Port auto-selection falls through ranges correctly; fails gracefully with user prompt when exhausted.

## Out of Scope

- **Authentication/authorization**: No login, no user accounts, no RBAC. The service is open to anyone who can reach the port.
- **HTTPS/TLS**: The server listens on plain HTTP only. Tunneling/SSL termination is the user's responsibility via a reverse proxy or SSH tunnel.
- **Multi-tenancy**: Single configuration and single job queue per process instance.
- **Distributed execution**: No worker nodes, no message queues, no coordinator. Single-machine only.
- **Database connection pooling UI**: Connection pool settings are configured via config YAML; there's no runtime pool inspection in the UI.
- **Scheduling/cron**: No ability to schedule recurring migrations. Jobs are triggered manually.
- **Diff/compare views**: No side-by-side diff of source vs target schemas or DDL before/after.
- **Edit metadata in UI**: Metadata is read-only in the web viewer. To modify metadata, user must edit the CSV/Excel source and reload.
- **Mobile/responsive design**: UI is designed for desktop browsers with typical 1200px+ viewport. Mobile is not a target.
- **Mid-operation pause**: Only cancel + resume are supported. No runtime pause of an in-flight table export/import.

## Further Notes

- The existing `--sql-out` migrate flag maps naturally to a toggle in the migrate page UI: "Direct Import" vs "Generate SQL Files".
- The `migrate_cmd.go` extraction into `MigrateService` is the most impactful refactoring. Extracting it makes both the CLI and web handler thin.
- Worker flags (`--progress-db`, `--job-id`, `--parent-pid`) are additive — CLI experience is unchanged when flags are absent.
- For dialect-specific methods, the existing `registry` and `dialect` interfaces already handle variation. No web-specific dialect abstractions needed.
- The `.env` file and `OWL_MIGRATE_*` environment variables are documented in ADR-007.
- Full architecture decision records: `docs/plans/adr-web-service.md` (18 ADRs).
- Full domain glossary: `docs/plans/glossary-web-service.md`.
