# Documentation Management — owl-migrate Web Service

## Documentation Directory Structure

```
docs/
├── index.md                       # Documentation hub (existing)
├── getting-started.md             # Installation + quick start (existing)
├── cli-commands.md                # CLI command reference (existing)
├── config.md                      # Configuration reference (existing)
├── csv-format.md                  # CSV metadata format (existing)
├── migration-pipeline.md          # E2E pipeline details (existing)
├── dialect-mapping.md             # Dialect + type mapping (existing)
├── development.md                 # Developer guide (existing)
├── database-metadata/             # Metadata query SQL ref (existing)
│   └── index.md
├── plans/                         # PRDs + ADRs + test designs (new)
│   ├── prd-web-service.md
│   ├── adr-web-service.md
│   ├── glossary-web-service.md
│   ├── unit-test-design.md
│   └── e2e-test-design.md
└── web/                           # User guide for web service (new)
    ├── index.md                   # Web service docs hub
    ├── installation.md            # Download + launch + port config
    ├── config-wizard.md           # How to use the scenario config builder
    ├── metadata-viewer.md         # Loading + inspecting metadata in browser
    ├── ddl-generator.md           # DDL preview + download workflow
    ├── data-export.md             # Export job workflow
    ├── data-import.md             # Import job workflow
    ├── migration-pipeline.md      # Full migration via web UI
    ├── job-management.md          # Job history, resume, cancel
    └── troubleshooting.md         # Common issues + debug endpoint
```

## Document Audiences

| Audience | Documents |
|---|---|
| **End users** (DBAs, data engineers) | `getting-started.md`, `cli-commands.md`, `config.md`, `csv-format.md`, `dialect-mapping.md`, `web/*` |
| **Operators** (deployment, troubleshooting) | `web/installation.md`, `web/troubleshooting.md`, `config.md` |
| **Developers** (contributors) | `development.md`, `plans/*`, `database-metadata/*` |

## Web Service User Guide (`docs/web/`)

Eight documents covering the web UI workflow end-to-end. Each follows the same template:

### Template

```markdown
# <Title>

## Overview
One-paragraph description of the feature from the user's perspective.

## Prerequisites
What config/setup is needed before using this page.

## Step-by-Step
Numbered steps with screenshots or terminal output.

## Configuration Reference
Relevant config YAML section with annotations.

## Troubleshooting
Common errors and their solutions.
```

### Document Outlines

#### `web/index.md` — Web Service Documentation

- Link table to all eight docs
- Quick architecture overview (Master/Serve/Worker, no auth)
- Screenshot of homepage with scenario cards labeled

#### `web/installation.md` — Installation & Launch

- Download binary from GitHub Releases
- `owl-migrate serve --help`
- Port configuration priority (flag > .env > env var > default)
- `.env` file format with all `OWL_MIGRATE_*` variables
- Accessing from browser
- Stopping (SIGTERM/Ctrl-C)

#### `web/config-wizard.md` — Configuration Builder

- Homepage scenario cards explained (Migrate, Export DDL, etc.)
- Each scenario's form fields and defaults
- How to upload an existing YAML vs building from scratch
- Downloading config as YAML file
- Schema mapping, type overrides, table filter configuration

#### `web/metadata-viewer.md` — Metadata Viewer

- Loading from CSV directory, XLSX file, or live database
- Table list with search/filter
- Column detail view
- Validation results page (warnings vs errors)
- PK map display for cursor pagination

#### `web/ddl-generator.md` — DDL Generator

- Selecting target dialect from registry
- Toggling build options (IF NOT EXISTS, comments, split by object)
- Preview with SQL syntax highlighting
- Downloading as ZIP
- Regenerating after changing dialect

#### `web/data-export.md` — Data Export

- Online mode: select tables, set batch/page size, parallelism
- Offline mode: upload CSV/XLSX source
- Progress monitoring with WebSocket
- Downloading exported files
- Canceling a running export

#### `web/data-import.md` — Data Import

- Uploading CSV source files
- Configuring transforms (datetime format, trim strings, null_if)
- Setting commit interval + error policy
- Progress monitoring
- Post-import validation

#### `web/migration-pipeline.md` — Full Migration

- Direct mode vs SQL output mode
- Step-by-step timeline: metadata → DDL → export → import → report
- Progress monitoring with WebSocket
- Migration report viewer (styled summary table)
- Downloading report as JSON

#### `web/job-management.md` — Job Management

- Job history table (status, duration, row counts)
- Job detail page: checkpoints, events, download links
- Resume: reusing checkpoint from interrupted job
- Cancel: how it works, what happens to the worker
- Interrupted jobs vs failed jobs

#### `web/troubleshooting.md` — Troubleshooting

- Port already in use (`--port` or kill existing process)
- IPC connection failure (check master is running)
- SQLite lock errors (`/debug/db-stats` to inspect WAL)
- Worker not writing progress (check `--progress-db` flag)
- WebSocket connection refused (check WebSocket endpoint path)
- Browser shows blank page (check static file embedding)
- Heartbeat file not found (check `/tmp/` permissions)
- Job stuck in "running" after crash (manual resume or mark interrupted)

## In-App Documentation

The web service embeds a lightweight help system accessible via the sidebar or `/docs` route. Implementation:

### Template: `templates/docs.html`

- Renders Markdown files embedded via `//go:embed docs/web/*`
- Uses a minimal Markdown-to-HTML renderer (stdlib `html/template` with pre-rendered HTML, or a lightweight Go markdown library)
- Sidebar navigation tree generated from document list

### Decision: Static pre-rendered HTML vs dynamic Markdown rendering

**Choice: Static pre-rendered HTML from embedded `.html` files** (not live Markdown rendering).

Rationale:
- No markdown library dependency — the binary stays lean.
- `docs/web/*.md` files are the source of truth; a `make docs` target converts them to `docs/web/*.html` before `go build`.
- The embedded HTML files are served directly via `//go:embed docs/web/*.html`.
- When docs are updated, the contributor runs `make docs` to regenerate HTML and commits both `.md` and `.html` files.

### Makefile target

```makefile
docs:
    # Convert all Markdown docs to HTML for embedding
    # Uses a Go tool or a simple script
    go run ./cmd/owl-migrate/ docs/build.go -in docs/web/ -out internal/server/serve/embeds/docs/
    # This generates .html files that get embedded via //go:embed

docs/check:
    # CI check: ensure generated HTML is in sync with markdown
    diff <(go run ./cmd/owl-migrate/ docs/build.go -in docs/web/ -out /tmp/docs-check/) internal/server/serve/embeds/docs/
```

### Route registration

```go
// In internal/server/serve/server.go
mux.Handle("GET /docs/", docsHandler)           // Serves embedded HTML docs
mux.Handle("GET /docs", http.RedirectHandler("/docs/", 301))
```

### Sidebar integration

The `templates/base.html` sidebar includes a "Documentation" link that opens `/docs/` in a new browser tab (not an in-app overlay — full page docs). The sidebar also links to the existing GitHub docs for CLI reference.

## Developer Documentation (`docs/plans/`)

These are reference documents for maintainers, not embedded in the binary:

| Document | Purpose |
|---|---|
| `prd-web-service.md` | Feature specification (updated after grill) |
| `adr-web-service.md` | 18 architecture decision records |
| `glossary-web-service.md` | Domain terminology for web service |
| `unit-test-design.md` | 60 unit test functions across 10 files |
| `e2e-test-design.md` | 22 E2E test cases across 5 suites |

## Documentation Maintenance Rules

1. **User-facing docs are source of truth**: `docs/web/*.md` must be updated when behavior changes.
2. **Pre-rendered HTML must be regenerated**: Run `make docs` before committing changes to `docs/web/*.md`.
3. **PR checklist includes docs**: Any PR that changes web service behavior must include the corresponding doc updates.
4. **New scenarios = new doc page**: Each scenario card on the homepage must have a corresponding doc page in `docs/web/`.
5. **Developer docs are optional**: `docs/plans/*` are design artifacts — they don't need to be kept in sync with code after implementation.
