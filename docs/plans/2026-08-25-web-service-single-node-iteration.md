# Web Service Single-Node Iteration Design

**Date**: 2026-08-25
**Status**: Approved (design discussion)
**Supersedes/extends**: `adr-web-service.md`, `prd-web-service.md` (2026-07-26 baseline)

## 1. Purpose

Iterate on the existing `serve` mode to deliver the lightweight single-node deployment
target for the 1.x line: one self-contained binary embedding all web assets, suitable for
team-shared access on one server. Distributed migration is explicitly deferred to 2.0;
this design only plants the minimal seams (interfaces, schema columns, API stability)
that keep 2.0 from forcing breaking changes.

## 2. Current State (baseline)

Already implemented and retained:

- `owl-migrate serve` runs two HTTP servers: the user-facing serve server
  (`internal/server/serve`) and a loopback master IPC server
  (`internal/server/master`) that spawns CLI subcommands (`migrate`, `export data`,
  `import`) as worker processes.
- Job state lives in SQLite (`jobs`, `job_checkpoints`, `progress_events`); workers
  write progress directly to the shared DB; the serve server streams it to browsers via
  per-job WebSocket with DB polling.
- REST API under `/api/v1/*` covers config, metadata load, DDL/SELECT/INSERT
  generation, job lifecycle, outputs/downloads, scenarios.
- Frontend: 12 Go-template SSR pages + vanilla JS/CSS, embedded via `go:embed`
  (`web/embed.go`).
- Master heartbeat file + worker-side orphan detection for crash recovery.

## 3. Approved Decisions

| Topic | Decision |
|---|---|
| Direction | Iterate existing architecture; keep serve/master/worker skeleton |
| Frontend | Pure static SPA, **no build step**, no framework, no CDN |
| SQLite driver | Replace `mattn/go-sqlite3` (CGO) with `modernc.org/sqlite` (pure Go) |
| Access model | Team-shared server: bind `0.0.0.0`/LAN IP with optional token auth |
| 2.0 prep | Moderate: interface seams + `node_id` column + frozen `/api/v1`; no distributed code |
| Migration path | Phase-wise page replacement; SSR and SPA coexist until cutover |

### Why the driver switch (evidence)

Cross-builds on this machine silently set `CGO_ENABLED=0` (no C cross toolchain),
which makes `mattn/go-sqlite3` compile as its no-op stub (`static_mock.go`). The build
succeeds but `serve` fails at runtime with `go-sqlite3 requires cgo to work`. This was
reproduced locally: `CGO_ENABLED=0 go test ./internal/service/ -run
TestJobStore_CreateJob$` fails with the stub error. In other words, `make build/linux`
artifacts are currently broken for serve mode. `modernc.org/sqlite` removes CGO
entirely: any `GOOS/GOARCH` cross-build works, binaries are static and run on older
Linux systems without glibc version issues.

## 4. Phase 0 — Mandatory Fixes (P0 inventory)

| # | Problem | Location | Fix |
|---|---|---|---|
| 1 | `randSuffix()` is not random (`pid-len(tempdir)`, constant per process) → concurrent/repeat generations overwrite the same output dir | `internal/server/serve/generate.go:333` | Real random suffix (time + random hex) |
| 2 | `genOutputs` is in-memory and single-slot per kind: downloads 404 after restart; concurrent users overwrite each other | `generate.go` (`setGenOutput`/`getGenOutput`) | Persist generation records in SQLite (kind, dir, created_at); downloads read latest per kind |
| 3 | `GET /api/v1/config` and config-library read endpoints return full config including DSN passwords | `server.go:181`, `configs.go` | Mask credentials in read responses; write path unchanged |
| 4 | `Access-Control-Allow-Origin: *`, WebSocket `InsecureSkipVerify` (no Origin check), no request body limits | `server.go:318`, `websocket.go:88`, all JSON handlers | Same-origin CORS only; Origin enforcement on WS; `http.MaxBytesReader` on write endpoints (2 MB config upload, 1 MB others) |
| 5 | No single-instance lock: a second `serve` process marks the first instance's live jobs as interrupted | `internal/cmd/serve.go` (`MarkRunningAsInterrupted`) | Lock file `~/.owl/migrate/serve.lock` with PID; fail fast if held by a live process |
| 6 | `/docs` serves `./docs-site` from the filesystem, not from the embedded binary → 404 on single-binary deploys | `internal/server/serve/docs.go:31` | Makefile build step copies `docs-site/` → `web/docsite/`; `web/embed.go` embeds `docsite/*`; `docs.go` serves the embedded FS (filesystem lookup kept only as dev fallback). `go:embed` cannot reference parent dirs, so the copy step is required |

Additionally in Phase 0:

- Switch job store to `modernc.org/sqlite`. Driver name `sqlite`; DSN pragmas change
  from `_busy_timeout=5000&_journal_mode=WAL` to the modernc
  `_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)` form
  (`internal/service/job.go:49`). Everything above `database/sql` is unaffected.
- Cancel becomes graceful: SIGTERM → grace period → SIGKILL (`master.go:232`).
- Dead code removal: unused `selectPort`/`isPortFree` in `master.go`; unused
  `Hub.broadcast`/`hasSubscribers` in `websocket.go` (connections poll the DB
  directly; either implement central broadcast or delete — delete for now).
- Generation temp dirs: retention policy (keep latest N per kind, prune older).

## 5. Architecture (unchanged skeleton, sharpened seams)

```
browser ──▶ serve HTTP (default 127.0.0.1:8080)
             ├─ static SPA (embedded)
             ├─ /api/v1/* REST + per-job WebSocket
             └─▶ master IPC (127.0.0.1:auto port)
                   └─ Spawner interface
                        ├─ LocalSpawner (today: exec subprocess workers)
                        └─ (2.0) RemoteSpawner
```

- `execSpawner` (`internal/cmd/serve.go:188`) is renamed to `LocalSpawner`. The
  `Spawner` interface is the single replacement point for 2.0; serve server, API, and
  frontend stay untouched when a remote spawner arrives.
- `JobStore` stays interface-shaped for a future shared store; no distributed code now.
- Heartbeat/orphan-recovery behavior is retained as-is.

## 6. Data Layer

- Driver: `modernc.org/sqlite` (see §3).
- Schema: add `node_id TEXT NOT NULL DEFAULT 'local'` to `jobs`,
  `job_checkpoints`, `progress_events`. Zero behavior change in 1.x; 2.0 uses it to
  aggregate multi-node state without another migration.
- New table `generation_outputs` (or equivalent) recording kind, output dir, created
  timestamp, file count/size — backs the Phase 0 fix #2.

## 7. Security

- **Token auth (optional)**: `--token` flag or `OWL_MIGRATE_TOKEN` env. When set,
  `/api/v1/*` (except `health`) requires `Authorization: Bearer <token>`;
  the WebSocket accepts `?token=<token>` (browsers cannot set WS headers). Static
  pages are not gated; the SPA shows a token prompt on 401 and stores the token in
  `localStorage`, attached by the shared `api` wrapper.
- **Safe default**: without a token, the server refuses to bind anything other than
  loopback; an explicit `--host` of a non-loopback address fails startup.
- CORS: same-origin only (wildcard removed).
- Request bodies bounded with `http.MaxBytesReader`.
- Config read endpoints mask passwords embedded in DSNs.
- Master IPC stays on `127.0.0.1` (already the case).

## 8. Frontend — Build-less Static SPA

```
web/
  embed.go            # go:embed unchanged; covers static/* docs/* (templates/* during transition)
  static/
    index.html        # single SPA entry
    css/style.css     # existing ~1200-line theme stylesheet reused as-is
    js/
      app.js          # existing runtime (api/toast/theme/highlight) becomes the kernel
      router.js       # hash router (#/jobs, #/config, ...)
      ws.js           # WebSocket progress + fallback to events?after_seq= polling
      views/          # one ES module per page: home/config/metadata/ddl/select/
                      # insert/migrate/export/import/jobs/jobDetail ...
  docs/               # embedded docs retained
```

Key choices:

- **Zero dependencies**: native ES modules (browser-native `import`), no framework,
  no bundler, no CDN. Deployment targets are often air-gapped intranets; every byte
  must come from the binary itself.
- **Hash routing**: the server serves one HTML entry at `/`; no route config or
  history-mode fallback needed.
- **Data**: everything via existing `/api/v1` endpoints; progress screens combine WS
  streaming with `events?after_seq=` catch-up (both endpoints already exist).
- **Styling**: reuse `style.css` dark/light theme system; no visual rework.
- **XSS hardening**: the current code has 37 `innerHTML` uses against 11 `escapeHtml`
  calls; all views render DB-derived names/messages through a single escape helper.

Transition path (dual-track):

1. SPA shell at `/ui` (index + router + kernel) with home and jobs pages.
2. Migrate one page per commit; each migrated page is decommissioned on the template
   side after verification. Inline template scripts are consolidated into the
   corresponding `views/*.js`.
3. When all 12 pages are migrated: `/` serves the SPA, `templates/` and `pages.go`
   are deleted, and the embed footprint shrinks.

Each step is independently verifiable and reversible.

## 9. API Contract

- `/api/v1/*` paths and semantics are frozen for 1.x; add `docs/api-contract.md`
  documenting them. The existing serve-package e2e suite (`e2e_test.go`) is the
  regression gate.
- Additive changes allowed: new optional fields, new endpoints. Breaking changes go
  to `/api/v2` in 2.0.
- `GET /api/v1/health` returns version/commit/build time (already injected via
  ldflags) for ops troubleshooting.

## 10. Deployment & Ops

- Deliverable: one binary. Typical run:
  `owl-migrate serve --host 0.0.0.0 --port 8080 --token <t>`.
- State under `~/.owl/migrate/` (db, config, logs, serve.lock); project artifacts stay
  CWD-relative — consistent with the existing `paths` design.
- Upgrade = replace binary and restart; job history survives (SQLite persistence).
- Docs gain a systemd unit example.

## 11. Phase Plan

| Phase | Content | Exit criteria |
|---|---|---|
| **0** | The six P0 fixes + modernc switch + graceful cancel + dead-code removal | Full test suite green with `CGO_ENABLED=0`; cross-built `serve` verified working on the target platform (or in a Linux container from macOS) |
| **1** | SPA shell + hash router + api/ws/token kernel; home and jobs pages migrated | `/ui` usable alongside legacy pages |
| **2** | Migrate remaining pages, one commit each, with XSS-safe rendering | All 12 pages migrated |
| **3** | `/` switches to SPA; delete `templates/` + `pages.go`; docs served from embed; embed slimming | Single-binary self-containment verified (bare binary in an empty dir: pages, docs, downloads all work) |

## 12. Verification

- Build guardrail: Makefile targets set `CGO_ENABLED=0` explicitly; `make
  build/linux` and `make build/windows` are mandatory CI checks (they currently
  "succeed" while producing broken binaries — see §3).
- Phase 0 regresses through the existing serve e2e suite (`browser → serve → master →
  worker` chain over `httptest`).
- Phases 1–3: each migrated page checked against the legacy page's feature checklist.
- Phase 3 acceptance: deploy the binary alone into an empty directory; UI, embedded
  docs, and all download endpoints must work with no other files present.

## 13. Out of Scope (2.0+)

- Remote workers / distributed scheduling (only the `Spawner` seam is prepared).
- Multi-user RBAC, audit logging (single shared token in 1.x).
- SPA toolchain/build system (deliberately never, unless requirements change).

## 14. Risks

- `modernc.org/sqlite` behaves differently at the DSN/pragma level; the switch is
  isolated to `internal/service/job.go`, and existing JobStore tests plus serve e2e
  cover the behavior.
- SPA migration spans several commits; dual-track keeps the product usable throughout,
  at the cost of temporarily maintaining two render paths.
- Token is shared (no per-user identity); acceptable for trusted-team deployments,
  documented as a 1.x limitation.
