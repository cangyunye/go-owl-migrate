# E2E Test Design — owl-migrate Web Service

## Architecture

All E2E tests use `//go:build e2e` build tags and require Docker Compose to provide real databases. The pattern follows existing E2E tests in `internal/cmd/e2e_conn_test.go` and `internal/transfer/importer/importer_e2e_test.go`.

## Prerequisites

```bash
# Start test databases
docker compose -f testdata/db/docker-compose.yaml up -d

# Run E2E tests
make test/e2e

# Or directly:
go test -tags e2e -v -count=1 ./internal/server/... ./internal/cmd/...
```

## Test Databases from Docker Compose

| Database | Host | Port | User/Pass | Database |
|----------|------|------|-----------|----------|
| PostgreSQL | 127.0.0.1 | 5432 | postgres/secret | testdb |
| MySQL | 127.0.0.1 | 3306 | root/secret | testdb |

DSN constants at top of E2E test files:
```go
const (
    pgDSN    = "host=127.0.0.1 port=5432 user=postgres password=secret dbname=testdb sslmode=disable"
    mysqlDSN = "root:secret@tcp(127.0.0.1:3306)/testdb?charset=utf8mb4&parseTime=true"
)
```

## E2E Test Cases

### Suite 1: Serve HTTP API (`internal/server/serve/e2e_test.go`)

**Setup per test**: start a full `owl-migrate serve` process on a random port. Seed `testdata/csv/` metadata. SQLite database in `t.TempDir()`.

```
func TestE2E_ServeStartup(t *testing.T) {
    t.SkipIfNoDocker(t)
    // 1. Start owl-migrate serve --port <random> --host 127.0.0.1
    // 2. GET http://127.0.0.1:<port>/api/v1/health → 200
    // 3. GET http://127.0.0.1:<port>/ → HTML page with <title>
    // 4. GET http://127.0.0.1:<port>/static/css/style.css → 200
    // 5. Send SIGTERM → process exits cleanly
}
```

```
func TestE2E_ConfigLoadAndValidate(t *testing.T) {
    // 1. Start serve
    // 2. POST /api/v1/config/load with valid migrate YAML → 200, returns config JSON
    // 3. POST /api/v1/config/load with invalid YAML (bad target_dialect) → 400, error message
    // 4. GET /api/v1/config → returns the loaded config
}
```

```
func TestE2E_MetadataLoad(t *testing.T) {
    // 1. Start serve with csv metadata config preset
    // 2. POST /api/v1/metadata/load → 200, returns metadata JSON
    // 3. GET /api/v1/metadata/tables → 200, lists SCOTT.EMP, SCOTT.DEPT, SCOTT.BONUS
    // 4. GET /api/v1/metadata/tables/SCOTT/EMP → 200, full table with columns, PKs, indexes
    // 5. GET /api/v1/metadata/validate → 200, list of validation errors (empty or populated)
}
```

```
func TestE2E_DDLGenerate(t *testing.T) {
    // 1. Load metadata + config with target_dialect=postgres
    // 2. POST /api/v1/ddl/generate → 200, returns DDL SQL content
    // 3. GET /api/v1/ddl/download → 200, ZIP download with correct Content-Type
    // 4. Verify ZIP contains expected file count and naming
}
```

```
func TestE2E_SelectGenerate(t *testing.T) {
    // 1. Load metadata
    // 2. POST /api/v1/select/generate with batch method=cursor, page_size=100 → 200
    // 3. Verify generated SQL contains cursor pagination clause
}
```

```
func TestE2E_JobAPI(t *testing.T) {
    // 1. Start serve
    // 2. POST /api/v1/migrate with SQL-output config → 202, returns job_id
    // 3. GET /api/v1/jobs → 200, list contains new job
    // 4. GET /api/v1/jobs/{job_id} → 200, job status eventually transitions to completed
    // 5. GET /api/v1/jobs/{job_id}/checkpoints → 200, list of per-table checkpoints
}
```

```
func TestE2E_CancelJob(t *testing.T) {
    // 1. Start a long-running export job
    // 2. DELETE /api/v1/jobs/{job_id} → 200
    // 3. GET /api/v1/jobs/{job_id} → status = "cancelled"
}
```

```
func TestE2E_WebSocketProgress(t *testing.T) {
    // 1. Start a migration job
    // 2. Open ws://127.0.0.1:<port>/api/v1/jobs/{job_id}/ws
    // 3. Verify messages: progress events in order, then complete event
    // 4. Verify event schema matches: {type, seq, event, schema, table, rows}
}
```

```
func TestE2E_WebSocketReconnect(t *testing.T) {
    // 1. Start job, connect WebSocket, receive 2 events, disconnect
    // 2. Reconnect WebSocket
    // 3. Verify catch-up: receives seq 1-2 again + remaining events
}
```

### Suite 2: Full Migration Pipeline (`internal/server/e2e_migrate_test.go`)

```
func TestE2E_Migration_PGtoPG(t *testing.T) {
    // 1. Create source table in PostgreSQL with test data
    // 2. Start serve with config: source=PG, target=PG, tables=[test_table]
    // 3. POST /api/v1/migrate → returns job_id
    // 4. Wait for job completion (poll GET /api/v1/jobs/{id})
    // 5. Verify target table has same row count as source
    // 6. Verify migration report.json has correct totals
}
```

```
func TestE2E_Migration_MySQLtoPG(t *testing.T) {
    // Same as above, source=MySQL, target=PostgreSQL
}
```

```
func TestE2E_Migration_Resume(t *testing.T) {
    // 1. Run partial migration (interrupt via cancel)
    // 2. Verify checkpoint saved for completed tables
    // 3. POST /api/v1/migrate with resume_from=interrupted_job_id
    // 4. Verify only uncompleted tables are processed
    // 5. Verify final report shows both old + new table results
}
```

```
func TestE2E_Migration_SQLOutput(t *testing.T) {
    // 1. Migration in SQL output mode
    // 2. Verify INSERT SQL files generated
    // 3. Verify SQL files contain correct data
    // 4. Verify no target DB was touched
}
```

### Suite 3: Worker Crash Recovery (`internal/server/e2e_worker_test.go`)

```
func TestE2E_WorkerCrash_Reattach(t *testing.T) {
    // 1. Start a long migration (large table, slow export)
    // 2. Kill the serve process (kill -9)
    // 3. Verify worker continues running (check PID still exists)
    // 4. Start new serve instance
    // 5. GET /api/v1/jobs → sees the still-running job from SQLite
    // 6. WebSocket connection to job receives current progress events
}
```

```
func TestE2E_WorkerCrash_MasterDeath(t *testing.T) {
    // 1. Start migration
    // 2. Kill master process (kill -9)
    // 3. Verify heartbeat file staleness
    // 4. Verify worker writes status='interrupted' after heartbeat timeout
    // 5. Verify worker exits cleanly
}
```

```
func TestE2E_InterruptedJob_Resume(t *testing.T) {
    // 1. Start migration, interrupt (kill worker)
    // 2. Verify status='interrupted' in SQLite
    // 3. Resume with checkpoint reuse
    // 4. Verify final report includes both old (completed tables) + new results
}
```

### Suite 4: Port Selection and Config (`internal/server/e2e_config_test.go`)

```
func TestE2E_PortPriority_FlagOverEnv(t *testing.T) {
    // 1. Set OWL_MIGRATE_SERVE_PORT=9090 in env
    // 2. Start serve with --port 8080
    // 3. Verify serve binds to 8080 (flag wins)
}
```

```
func TestE2E_PortPriority_EnvOverDefault(t *testing.T) {
    // 1. Set OWL_MIGRATE_SERVE_PORT=9090 in env
    // 2. Start serve without --port
    // 3. Verify serve binds to 9090
}
```

```
func TestE2E_IPCPortAutoSelect(t *testing.T) {
    // 1. Start serve without --master-ipc-port
    // 2. Verify master IPC bound to port in valid range
    // 3. Verify serve can reach master at that port
}
```

```
func TestE2E_IPCHealth(t *testing.T) {
    // 1. Start serve
    // 2. GET http://127.0.0.1:<master_ipc_port>/health → 200 + {"status":"ok"}
}
```

### Suite 5: Multi-Client WebSocket (`internal/server/serve/websocket_e2e_test.go`)

```
func TestE2E_WebSocket_MultiClient(t *testing.T) {
    // 1. Start job
    // 2. Open 3 WebSocket connections to same job
    // 3. Verify all 3 receive the same events in order
    // 4. Close 2 connections, verify 3rd still receives events
}
```

```
func TestE2E_WebSocket_EvictionOnFailure(t *testing.T) {
    // 1. Open connection, simulate 3 send failures
    // 2. Verify connection removed from subscriber set
    // 3. Write new event → only remaining connections receive it
}
```

---

## Makefile Targets

```makefile
test/e2e:
    # Start test databases
    docker compose -f testdata/db/docker-compose.yaml up -d
    sleep 5  # wait for databases to be ready
    # Run E2E tests
    $(GO) test -tags e2e -v -count=1 ./internal/server/...
    # Cleanup
    docker compose -f testdata/db/docker-compose.yaml down

test/e2e-quick:
    $(GO) test -tags e2e -v -count=1 ./internal/server/serve/  # Serve API only

test/e2e-full:
    $(GO) test -tags e2e -v -count=1 ./...  # All E2E tests in all packages
```

## Test Data

- Source test tables created programmatically in each test via `CREATE TABLE ...` + `INSERT`.
- Table names include test name + random suffix to avoid collisions.
- Tables dropped in `t.Cleanup()`.
- SCOTT schema available as a fixture for metadata tests.

## Total E2E Test Functions

| Suite | Package | Count |
|-------|---------|-------|
| Serve HTTP API | `internal/server/serve/e2e_test.go` | 9 |
| Full Migration | `internal/server/e2e_migrate_test.go` | 4 |
| Worker Crash Recovery | `internal/server/e2e_worker_test.go` | 3 |
| Port/Config | `internal/server/e2e_config_test.go` | 4 |
| WebSocket Multi-Client | `internal/server/serve/websocket_e2e_test.go` | 2 |
| **Total** | | **22** |
