# owl-migrate Web API Contract (v1)

Status: frozen for the 1.x line. Additive changes only (new optional fields,
new endpoints). Breaking changes ship as `/api/v2` in 2.0.

## Conventions

- Base path: `/api/v1`
- JSON requests/responses unless noted. Errors: `{"error": "<message>"}`
  with an appropriate 4xx/5xx status.
- Request bodies are limited: 2 MiB for config uploads, 1 MiB elsewhere.
  Oversized bodies receive 400 with a `request body too large` message.
  The two config-upload endpoints that allow 2 MiB are
  `POST /api/v1/config/upload` and `POST /api/v1/configs`; every other
  endpoint that reads a body is capped at 1 MiB.
- Same-origin only: no CORS headers are emitted; the Web UI is served from
  the same host.
- Authentication is delivered with Phase 1 (token middleware + SPA token
  prompt). Record it here when that phase lands; the contract reserves
  `Authorization: Bearer <token>` and `?token=` for WebSocket upgrades for
  that purpose. As of this phase there is **no authentication** — the server
  is intended for local/test-network use and is unauthenticated.

## Security notes

- `GET /api/v1/config` masks DSN passwords (`config.MaskDSN`), for both the
  `source` and `target` DSN values. The active-config download
  (`GET /api/v1/config/download`) and the saved-config library operations
  (`GET /api/v1/configs/{name}` download, `POST /api/v1/configs/{name}/load`)
  are all unmasked. The saved configs are returned byte-for-byte verbatim
  because they are file-equivalent operations and masking would break save
  round-trips; the active-config download instead re-serializes the in-memory
  config. The legacy `POST /api/v1/config/upload` echoes the submitted `yaml`
  verbatim in its response as well.
- Every generation run (DDL/SELECT/INSERT/metadata/export/offline export) is
  recorded in the job database; the `*/download` endpoints resolve the newest
  recorded output for their kind and survive server restarts. Ten outputs per
  kind are retained (`genOutputKeep`); older output directories are pruned
  from disk.
- Data sources are a **web-only** feature (reusable connection profiles). A
  stored DSN is **encrypted at rest** with AES-256-GCM. The key comes from the
  `OWL_MIGRATE_DS_KEY` environment variable, else a generated
  `~/.owl/migrate/.ds_key` file (`0600`). The key never leaves the server and
  is never returned by any endpoint. `GET /api/v1/datasources` and
  `POST /api/v1/datasources/{name}/pick` never include the DSN — nor its
  ciphertext. The config form stores a `datasource:<name>` reference in the
  DSN field instead; `POST /api/v1/scenarios/{name}/build` resolves that
  reference server-side and **masks** the resolved DSN in the returned
  preview (`config` map and `yaml`), while the persisted config keeps the
  real DSN so it stays offline-usable.

## Endpoints

| Method & path | Purpose |
|---|---|
| GET /api/v1/health | Liveness. Returns `{"status":"ok"}`. No auth. |
| GET /api/v1/jobs | Lists the most recent 100 jobs. |
| GET /api/v1/jobs/{id} | Returns one job descriptor. 404 if unknown. |
| GET /api/v1/jobs/{id}/events | Returns job progress events; optional `?after_seq=N` filters to newer events. |
| GET /api/v1/jobs/{id}/checkpoints | Returns the job's savepoint/checkpoint list. |
| GET /api/v1/jobs/{id}/output | Reports a sql-out job's INSERT SQL output directory, file list, and sizes. |
| GET /api/v1/jobs/{id}/output/download | Streams a completed job's SQL output as `tar.gz` (default), `zip`, or `raw` (`?format=`). 409 unless the job is `completed`. |
| GET /api/v1/dialects | Returns the sorted list of valid dialect names. |
| GET /api/v1/config | Returns the active config as JSON map; DSN passwords of `source`/`target` are masked. |
| PUT /api/v1/config | Replaces the active config from a JSON object and persists it. |
| GET /api/v1/config/download | Downloads the active config as `migrate.yaml` (re-serialized YAML, unmasked). |
| GET /api/v1/config/status | Reports the config path, on-disk status, dialect/type, and metadata-loaded state. |
| POST /api/v1/config/upload | Legacy config upload: parses submitted YAML, makes it active, persists verbatim, echoes scenario + form values. |
| GET /api/v1/configs | Lists the saved config library (name, size, modified, scenario, source/target types). |
| POST /api/v1/configs | Saves an uploaded config to the library, makes it active, returns name/scenario/values/yaml. |
| GET /api/v1/configs/{name} | Downloads a saved config's raw YAML (unmasked, file-equivalent). |
| POST /api/v1/configs/{name}/load | Makes a saved config active and returns its scenario + form values (YAML verbatim). |
| DELETE /api/v1/configs/{name} | Removes a saved config from the library. |
| POST /api/v1/metadata/load | Extracts metadata from the given `metadata`/`source` config and makes it active; returns table summaries. |
| GET /api/v1/metadata/tables | Returns all loaded tables with their columns, PKs, and row counts. |
| GET /api/v1/jobs/{id}/ws | WebSocket: streams job progress events, then a terminal message (`complete`/`cancelled`/`error`). |
| POST /api/v1/migrate | Starts a migration job (relayed to the master IPC server). |
| POST /api/v1/export | Starts a data-export job (relayed to the master IPC server). |
| POST /api/v1/import | Starts a data-import job (relayed to the master IPC server). |
| DELETE /api/v1/jobs/{id} | Cancels a running job (relayed to the master IPC server). |
| GET /api/v1/scenarios | Returns the available scenarios and DSN examples. |
| GET /api/v1/scenarios/{name} | Returns the schema for one scenario. 404 if unknown. |
| POST /api/v1/scenarios/{name}/build | Builds a config from submitted form values; optional `save` makes it the active config. Resolves `datasource:<name>` references server-side. |
| GET /api/v1/datasources | Lists reusable data sources (name, type, schema, remark, updated). Never returns the DSN. |
| GET /api/v1/datasources/{name} | Returns one profile plus its decomposed `fields` (username, host, port, database, extra) and `password_set`. The password value itself is never returned. |
| POST /api/v1/datasources | Creates/replaces a data source; the DSN is encrypted at rest. Accepts either `dsn` (raw) or `fields` (structured). |
| PUT /api/v1/datasources/{name} | Updates a data source; an empty `dsn` or a `fields.password` blank keeps the stored secret. |
| DELETE /api/v1/datasources/{name} | Removes a data source. |
| POST /api/v1/datasources/{name}/pick | Returns a data source's type + schema and its `datasource:<name>` ref for filling a config form. Never returns the DSN. |
| POST /api/v1/ddl/generate | Generates DDL files from the loaded metadata; optional `no_quote_identifiers`. Returns files + output dir. |
| GET /api/v1/ddl/download | Downloads the newest recorded DDL output as a zip. |
| POST /api/v1/select/generate | Generates paginated SELECT files; optional `batch_method`, `page_size`, `no_quote_identifiers`. |
| GET /api/v1/select/download | Downloads the newest recorded SELECT output as a zip. |
| POST /api/v1/insert/generate | Generates INSERT SQL from CSV data; optional `batch_size`, `truncate`, `no_quote_identifiers`. |
| GET /api/v1/insert/download | Downloads the newest recorded INSERT output as a zip. |
| GET /api/v1/metadata/validate | Validates loaded metadata and returns JSON `{"count": N, "errors": [{"severity": ..., "message": ...}]}`. |
| GET /api/v1/metadata/tables/{schema}/{table} | Returns one table's columns, PKs, and indexes. 404 if unknown. |
| POST /api/v1/metadata/export | Extracts live metadata from `source` to `csv`/`xlsx`/`sql` files with an optional `scope` filter. |
| GET /api/v1/metadata/export/download | Downloads the newest recorded metadata export as a zip. |
| POST /api/v1/export/offline | Converts local CSV (`data_dir`) or XLSX (`xlsx_path`) data into the target format. |
| GET /api/v1/export/offline/download | Downloads the newest recorded offline export as a zip. |
| GET /api/v1/show-query | Returns metadata extraction SQL for a dialect; `?dialect=` required, optional `?object_type=`. |

### Non-API routes (served for the Web UI, not part of the JSON contract)

| Method & path | Purpose |
|---|---|
| GET / | The SPA shell (since the Phase-3 cutover; the SSR page set was removed). |
| GET /ui | Same SPA shell (alias). |
| GET /static/ | Static assets (app.js kernel, style.css, SPA views). |
| GET /docs | Redirects to `/docs/`. |
| GET /docs/ | The docs portal (from `docs-site/` or embedded). |

## Path parameters

`{id}`, `{name}`, `{schema}`, and `{table}` are path segments. `{name}` is
sanitized before use (path components and YAML/JSON extensions stripped) and
resolved strictly inside the config library directory; attempts outside it
return 400 `invalid config name`. For data sources the same `{name}` rule
applies, resolved strictly inside `~/.owl/migrate/datasources/`.

## Job lifecycle

Jobs run in the background through the master IPC server. Progress is
observable via `GET /api/v1/jobs/{id}/events`,
`GET /api/v1/jobs/{id}/checkpoints`, and the `/api/v1/jobs/{id}/ws` WebSocket.
Cancellation is `DELETE /api/v1/jobs/{id}`. Sql-out migration SQL is available
from `GET /api/v1/jobs/{id}/output` and
`GET /api/v1/jobs/{id}/output/download` once the job reaches `completed`.
