# Phase 3: SPA Cutover — Remove the SSR UI, Serve the SPA at `/`

**Goal:** Finish the dual-track migration by removing the legacy server-side-rendered
(SSR) UI and serving the SPA as the only frontend. This is the Phase-2 residual
recorded in `2026-08-25-phase2-spa-page-migration.md` (Task 9, Step 3).

**Status:** Plan (review before executing).

---

## 1. Prerequisite: feature parity (Phase-2 residual closure)

Before cutover the SPA must do everything the SSR UI did. The parity audit found
one real gap and it is now closed:

| Ability | SSR | SPA | Status |
|---|---|---|---|
| Config page structured-DSN editor modal | `openDSNModal` | ported to `views/config.js` | ✅ done |
| Config page connection test (+ schema dropdown) | `testConn` / `enableSchemaSelect` | ported to `views/config.js` | ✅ done |
| Config page data-source picker | `static/js/config.js` | `views/config.js` | ✅ done |
| Job detail: checkpoints / resume / events / WebSocket | `job_detail.html` | `views/jobDetail.js` | ✅ present |
| metadata / export / import / migrate / generators | templates+JS | `views/*.js` | ✅ endpoints present |

**Remaining audit checklist** (verify each during cutover, browser smoke):
- Home flow-board links (`views/home.js`) all hash routes resolve.
- `#/jobs/:id` deep links + `#/config?scenario=` still work.
- DSN mask-safe prefill on loaded configs (SPA `applyFormValues` skips `******`).
- Token prompt works on the SPA (Phase-1 shell already has it; app.js shared).

---

## 2. Cutover tasks

### Task A — Rewrite `internal/server/serve/pages.go` to serve the SPA everywhere
- Remove the `pages` route table, `PageData`, `loadPage`, and the `html/template`
  + `web` (for templates) imports used only by it.
- Keep `GET /static/` (file server — still needed for app.js/css).
- Serve the SPA index `web.FS.ReadFile("static/ui/index.html")` at:
  `GET /` (`GET /{$}`), `GET /ui`, and `GET /ui/{$}`.
- `/docs` and `/api/v1/*` are untouched (docs.go, server.go).

### Task B — Delete the SSR-only assets
- `web/templates/*.html` — all 14 files (base.html included; the SPA shell
  `web/static/ui/index.html` replaces the whole layout).
- `web/static/js/config.js`, `web/static/js/datasources.js` — the two SSR
  per-page IIFEs. **Keep `web/static/js/app.js`** — it is the shared kernel
  loaded by the SPA shell (`window.api/toast/jobUI/highlight*`).

### Task C — Update tests
- `internal/server/serve/*_test.go` may assert SSR HTML (`/config` returns
  `scenario-pills` etc.). Change those to assert the SPA shell (`/ui`, `/`
  returns `static/ui/index.html` markers like `id="view"` / `#spa-nav`).
- Keep `/api/v1/*` contract tests unchanged.

### Task D — Docs + metadata
- `docs/api-contract.md` — non-API routes table: drop the SSR page rows, note
  `/` and `/ui` both serve the SPA.
- `CLAUDE.md` project overview / `docs/index.md` web mentions — update to "SPA".

---

## 3. Verification

```bash
make web/docsite          # re-stage docs if docs change (else gitignore)
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./internal/server/serve/ -count=1
node --check web/static/ui/views/*.js   # if node present
make build && ./build/$(go env GOOS)-$(go env GOARCH)/owl-migrate serve
```

Browser smoke (serve with token, SPA at `/`):
- `/` 200 → SPA home renders; navigate all 12 nav routes.
- `/ui` 200 → same shell (alias).
- `/config, /datasources, /metadata, /jobs/:id` all render; no "coming soon".
- DSN editor modal + test-connect + data-source picker all work on `/config`.
- `#/docs` → still `/docs` portal.

## 4. Rollback

Single-commit feature → `git revert` the cutover commit restores the SSR UI
and templates. Keep `git restore web/docsite/` discipline (staged copies not
committed).
