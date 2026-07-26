# Glossary — owl-migrate Web Service

Terms introduced by the web service feature. All existing CLI/project domain terms (SchemaModel, TableDef, LogicalType, Dialect, etc.) remain unchanged — see `docs/development.md` for those.

## Processes

### Master Process
The root process created by `owl-migrate serve`. Responsibilities: initialize the shared SQLite database, bind the IPC HTTP server, spawn the **Serve** child process, accept job requests from Serve, spawn **Worker** child processes, monitor Worker PIDs via heartbeat file, coordinate graceful shutdown. The Master is the only process that creates child processes.

### Serve Process
The child process of Master that runs the user-facing web server. Responsibilities: bind the HTTP port for browser access, serve embedded templates and static files, expose the REST API, read SQLite (read-only) for job data, relay job start/cancel commands to Master, manage **WebSocket Hub** for progress streaming. The Serve process is stateless beyond WebSocket connections — all durable state is in SQLite.

### Worker Process
A child process of Master that executes a single migration, export, or import job. It runs the same `owl-migrate` CLI binary with additional flags (`--progress-db`, `--job-id`, `--parent-pid`). Responsibilities: execute the full pipeline for its job type, write **Progress Events** and **Job Checkpoints** to the shared SQLite, detect Master death via **Heartbeat File**, and exit cleanly on completion or signal.

## Data Stores

### owl-migrate.db
A single SQLite3 database file in the working directory (or configured path). Three tables: `jobs`, `job_checkpoints`, `progress_events`. Opened in WAL mode with `_busy_timeout=5000`. Written to by Master and Worker processes; read by Serve process. This is the central state store for the entire web service.

### Job
A record in the `jobs` table representing a single migration, export, or import operation. Fields: `job_id`, `type`, `status`, `config` (JSON), `pid`, `created_at`, `finished_at`. Status values: `running`, `completed`, `interrupted`, `failed`, `cancelling`, `cancelled`. A Job may transition through multiple states over its lifetime.

### Job Checkpoint
A record in the `job_checkpoints` table representing the per-table state of a job. One row per `(job_id, schema, table_name)` tuple. Fields: `exported`, `exported_rows`, `imported`, `imported_rows`, `status`, `error`. Analogous to the CLI's `migrate_progress.json` table entries. Used for **Resume** operations — a new job reuses checkpoint data from a previous interrupted job.

### Progress Event
A record in the `progress_events` table representing a single progress notification. Fields: `id`, `job_id`, `seq` (monotonic sequence number), `event_type` (e.g., `export_start`, `export_complete`, `import_start`, `import_complete`, `checkpoint`), `schema`, `table_name`, `rows`, `message`, `created_at`. Emitted by Worker processes at table granularity (one event per table completed). Streamed to WebSocket clients by Serve.

### Heartbeat File
A file at `/tmp/owl-migrate-master.heartbeat` written by the Master process every 5 seconds containing its PID and current Unix timestamp. Worker processes read this file every 10 seconds. If the heartbeat is >20 seconds stale, the Worker assumes the Master has died and begins graceful self-termination.

## Networking

### IPC Port
The TCP port on `127.0.0.1` that the Master process binds for internal communication with the Serve process. Auto-selected from range 25430-25439 → 25400-25499 → 25000-25999 → 26000+, or configured via `--master-ipc-port`, `.env`, or `OWL_MIGRATE_MASTER_IPC_PORT`. Not exposed to the network.

### IPC HTTP
The REST protocol over localhost between Serve and Master. Three endpoints: `POST /api/v1/jobs` (start job), `DELETE /api/v1/jobs/{id}` (cancel job), `GET /health` (liveness). All payloads are JSON.

### Serve Port
The TCP port the Serve process binds for browser access. Configured via `--port`/`--host` flags, `.env` file, or `OWL_MIGRATE_SERVE_HOST`/`OWL_MIGRATE_SERVE_PORT` environment variables. Default: `127.0.0.1:8080`.

## WebSocket

### WebSocket Hub
A singleton in the Serve process managing all active WebSocket connections. Contains a `map[jobID]*SubscriberSet]`. A background goroutine polls SQLite `progress_events` for new rows and fans out to all subscribers of that job. Handles connection lifecycle: accept, three-failure eviction, clean close.

### WebSocket Subscriber
A single browser tab's WebSocket connection watching a specific job's progress. Receives JSON messages: `progress`, `checkpoint`, `complete`, `error`, `cancelled`. On connect, receives a catch-up replay of all existing events for that job (deduplicated by `seq`). Clients never send messages over WebSocket — it is receive-only.

### Catch-up Replay
When a WebSocket client connects (or reconnects after disconnect), the server sends all existing `progress_events` rows for that job in order, followed by any new events as they arrive. This handles browser refresh, tab close/reopen, and Serve process restart without losing progress visibility. The client deduplicates by `seq` field.

## Operations

### Scenario
A pre-defined configuration profile matching a CLI `init` scenario: `migrate`, `export-ddl`, `gen-select`, `export`, `import`, `export-insert`, `validate`, `full`. Each scenario populates specific sections of the `Config` struct with sensible defaults. The web homepage presents scenarios as entry-point cards.

### Direct Mode
Migration mode where data flows directly from source DB → CSV (export) → target DB (import). The Worker process performs both export and import. Contrast with **SQL Output Mode**.

### SQL Output Mode
Migration mode where data is exported from source DB → CSV → INSERT SQL files. No target DB connection needed. The Worker process only performs export and SQL generation. Enabled by a toggle in the migrate page UI.

### Resume
Starting a new job that reuses checkpoint data from a previous interrupted job. The new Worker reads `job_checkpoints` from the old job ID, finds tables marked `status = 'SUCCESS'`, and skips them. Only uncompleted tables are processed. Enabled via "Resume" button on a job detail page.

### Cancel
Stopping a running job before completion. Flow: user clicks Cancel → `DELETE /api/v1/jobs/{id}` on Serve → Serve relays to Master → Master sends `SIGTERM` to Worker PID → Worker catches signal, finishes current table's batch, writes final checkpoint, sets `jobs.status = 'cancelled'`, exits. No mid-batch abort.

### Interrupted
Status assigned to a job when the Worker process dies unexpectedly (crash, OOM kill, or Master death detected by heartbeat). On Serve startup, all `jobs WHERE status = 'running'` are bulk-updated to `'interrupted'`. An interrupted job's checkpoints and events are preserved for inspection and potential resume.
