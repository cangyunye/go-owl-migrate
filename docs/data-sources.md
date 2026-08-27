# Data Sources (数据源)

Status: available in `owl-migrate serve` web mode. **Web-only** — a data source is
a named, reusable database connection profile that the config page can fill in
with one click instead of retyping a DSN. It has no effect on the CLI or the
migration engine.

## What it solves

Every config scenario (`migrate`, `export`, `import`, `gen-select`, …) declares
its own `source_dsn`/`target_dsn` fields, so switching scenarios means retyping
the same connection string. A data source is a finer-grained unit than the
[config library](config.md): instead of a whole scenario config, it captures just
`{type, schema, dsn}` and can be referenced by any scenario.

Two levels of re-use coexist:

| Unit | Granularity | Reused across | Still needed |
|---|---|---|---|
| Config library (`configs/`) | one whole scenario YAML | a specific job | full config round-trip, offline CLI |
| Data source (`datasources/`) | one connection profile | all scenarios | retyping DSNs |

## User guide (Web UI)

1. Open the **数据源** menu (sidebar → Prepare → 数据源) and click **新建数据源**.
2. Fill `名称`, `类型`, `Schema`, `DSN`, optional `备注`, then **保存**.
   The DSN is **encrypted at rest**; the list and detail never show it.
   A per-type DSN format example appears under the DSN field and updates as
   you change `类型` (same hints as the config page, e.g.
   `格式示例：oracle://user:pass@host:1521/service_name`).
3. Go to the **配置** page. Next to any 源/目标 DSN field click **从数据源选择**.
4. In the modal pick a data source and click **应用**. The form fills the
   `类型` + `Schema` and places a `datasource:<name>` reference into the DSN
   field.
5. Save or preview as usual — the server resolves the reference, and the live
   preview shows a **masked** DSN (`u:******@…`) so the password never hits the
   browser. The saved config on disk holds the **real** DSN, so it remains
   offline-usable with the CLI.

> Notes
> - Picking a source is a **snapshot**: it copies the connection into the form.
>   Editing or deleting a later data source does not change an already-built
>   config.
> - `名称` is the reference key. Rename = create a new profile and delete the old.
> - Editing a profile with an empty DSN keeps the previously stored secret.
> - On the config page you can also save a filled connection directly as a data
>   source: click **存为数据源** next to a DSN field, type a name, and confirm.
>   A DSN that already came from a data source (a `datasource:<name>` reference)
>   is skipped to avoid saving a literal reference.

## Developer guide

### Storage layout

```
~/.owl/migrate/
  .ds_key                       # AES-256 key (0600), auto-generated; or OWL_MIGRATE_DS_KEY
  datasources/
    <name>.yaml                 # one profile per file
```

A profile file looks like:

```yaml
name: prod-oracle
type: oracle
schema: SCOTT
dsn: enc:v1:<base64(nonce||ciphertext)>
remark: production
created: "2026-08-27T00:00:00+08:00"
updated: "2026-08-27T00:00:00+08:00"
```

The `dsn` field is AES-256-GCM ciphertext with the `enc:v1:` prefix. Read and
write paths are:

- `List` — reads every `*.yaml`, returns `Info{name,type,schema,remark,updated}`,
  **no DSN**, sorted newest-first.
- `Put` — encrypts a plaintext DSN, or stores it verbatim if it already carries
  the `enc:v1:` prefix (never double-encrypts); an empty DSN on update keeps the
  existing ciphertext.
- `Resolve` — decrypts the DSN and returns `{type, schema, dsn}`.
- `Delete` — removes the file (no error if absent).

### Encryption (`internal/dscrypto`)

The key is resolved in this priority:

1. `OWL_MIGRATE_DS_KEY` env var (hex or base64, 32 bytes).
2. `~/.owl/migrate/.ds_key` (32 raw bytes), generated with a CSPRNG on first use,
   written `0600`.

Key loading and the vault are lazy (created on the first data-source request),
so a server that never touches data sources stays hermetic and never creates the
key file.

### Backend pieces

| File | Responsibility |
|---|---|
| `internal/dscrypto/vault.go` | AES-GCM `Encrypt`/`Decrypt`, key bootstrap + env override |
| `internal/datasource/datasource.go` | `Record`/`Info`/`Store` — CRUD, at-rest encryption, ref helpers |
| `internal/server/serve/datasources.go` | HTTP handlers |
| `internal/server/serve/scenarios.go` | resolves refs + masks the preview in `handleBuildScenarioConfig` |
| `internal/server/serve/server.go` | `DataSourcesDir` config, lazy `dsStore()`, route registration |

### API

See [api-contract.md](api-contract.md). Summary:

| Method & path | Purpose |
|---|---|
| `GET /api/v1/datasources` | list (no DSN) |
| `POST /api/v1/datasources` | create / replace |
| `PUT /api/v1/datasources/{name}` | update |
| `DELETE /api/v1/datasources/{name}` | delete |
| `POST /api/v1/datasources/{name}/pick` | type + schema + `ref`, never DSN |
| `POST /api/v1/scenarios/{name}/build` | resolves `datasource:<name>` refs, masks preview |

### Ref resolution & masking flow

The config form never sees the stored DSN. Instead it writes a reference:

- On pick, the frontend calls `POST /api/v1/datasources/{name}/pick`, gets
  `{type, schema, ref:"datasource:<name>"}`, fills type/schema, and sets the DSN
  field to `ref`.
- `handleBuildScenarioConfig` sees a DSN value with the `datasource:` prefix
  (`datasource.IsRef`), calls `store.Resolve(name)`, and substitutes the real
  DSN into `cfg.Source.DSN` / `cfg.Target.DSN`.
- The resolved fields are tracked, and the returned preview (`config` map and
  `yaml`) has those DSNs masked via `config.MaskDSN`.
- `save: true` persists the **unmasked** config to disk, so the CLI gets a real
  DSN. The browser only ever sees masked/reference forms.

### Security model

- At-rest: DSN is encrypted; the key is server-side only, never exposed.
- On the wire: list/pick never return the DSN or ciphertext.
- In the browser: the picker stores a reference; the preview is masked.
- Boundary: this protects against *passive* disk/`/api` reads. Because the key
  file lives on the server, a process with filesystem access can still read it —
  the same trust boundary as any server-side key.

### Tests

```
go test ./internal/dscrypto/...    # round-trip, nonce, key file, env override
go test ./internal/datasource/...  # at-rest encryption, resolve round-trip, list hides DSN
go test ./internal/server/serve/... -run 'DataSource'  # CRUD, pick, ref build + masking, stale ref 400
```

### Dev loop (docs / release)

- `docs/data-sources.md` is the canonical page. It is linked from
  `docs/index.md` and registered in `docs-site/index.html` (`DOCS` + `web`
  category).
- In dev, `/docs` serves `docs-site/` live (symlinked `docs/`), so edits appear
  without rebuilding.
- For a release binary, re-run `make web/docsite` to re-stage `docs/*.md` into
  `web/docsite/` (embedded). `git restore web/docsite/` before committing the
  generated copies.
