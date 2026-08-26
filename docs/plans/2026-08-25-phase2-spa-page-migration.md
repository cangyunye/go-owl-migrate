# Phase 2: Migrate Remaining SSR Pages to SPA Views — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port the 9 remaining SSR templates (`config, metadata, ddl, select, insert, migrate, export, export-metadata, import`) into build-less ES-module SPA views, so every nav target renders in the SPA at `/ui`. Dual-track stays: SSR pages remain mounted until Phase 3 cutover.

**Architecture:** No backend change. Each SPA view is an ES module exporting `render(root, params)` in `web/static/ui/views/`, registered in the hash router in `web/static/ui/router.js`. Views reuse the `window` kernel (`api`, `toast`, `theme`, `jobUI`, `highlightSQL`, `highlightYAML`, `humanSize`) and import shared helpers from `../util.js`. A shared generator-view helper consolidates the three near-identical generator pages.

**Tech Stack:** Go 1.25+, existing `web/static` ES modules + `style.css`, no framework, no build, no CDN.

**Spec:** `docs/plans/2026-08-25-web-service-single-node-iteration.md` (§8 Frontend, §11 Phase 2); `docs/plans/2026-08-25-phase1-spa-shell-token-auth.md` (Phase 1 already landed: api/toast/theme/jobUI + escapeHtml on window; util.js with statusBadge/escapeHtml; router at /ui).

## Global Constraints

- Module path `github.com/cangyunye/go-owl-migrate`; all builds/tests run with `CGO_ENABLED=0`.
- No new dependencies; no Node/build toolchain; no CDN; native ES modules only.
- Every view `export function render(root, params)`; registered in `router.js` with `active` + `title` matching the SSR nav label.
- Access kernels via `window.*` (api/toast/theme/jobUI/highlightSQL/highlightYAML/humanSize) — app.js is a classic script, ES modules are strict. Escape ALL server-derived strings via the imported `escapeHtml` from `../util.js`.
- Nav links inside views use hash routes (`#/config`, `#/jobs/<id>`...), never SSR `/paths`.
- Reuse existing CSS classes from `style.css`; do NOT add new CSS unless truly unavoidable.
- XSS safety: every server field (schema, table_name, message, config values, file names, sql content) goes through escapeHtml before innerHTML. Where the SSR template used raw interpolation, improve — do not replicate the XSS gap.
- Repo convention: plain `testing` + `httptest`; no framework.
- Verification per view: `node --check <file>` (if node available) + `CGO_ENABLED=0 go test ./internal/server/serve/ -count=1` (embed + shell route tests) + reason-verify. Commit after each task.

### Shared helper — generator views (ddl/select/insert)

The three generator pages are nearly identical (one endpoint, one options form, one generate(); identical renderGenFiles/file-count/download UX). Fold them into one shared generator view factory to DRY.

Create `web/static/ui/views/generator.js`:

```js
/* owl-migrate SPA · shared generator view factory (ddl/select/insert) */
import { escapeHtml } from '../util.js';

/**
 * buildGeneratorView({ title, overline, subtitle, endpoint, formFields, downloadLabel })
 * returns a view render(root, params) that renders a page-head + a gen form panel
 * (options) + a file-list/sql-preview panel, wires generate() (api.post endpoint),
 * and uses window.renderGenFiles / window.humanSize / window.toast.
 */
export function buildGeneratorView(cfg) {
    const { title, overline, subtitle, endpoint, downloadLabel, formHTML, collectOptions } = cfg;
    return async function render(root /*Element*/, params) {
        root.innerHTML = ''
            + '<div class="page-head"><div>'
            +   '<div class="overline">' + escapeHtml(overline) + '</div>'
            +   '<h1>' + escapeHtml(title) + '</h1>'
            +   '<p class="subtitle">' + escapeHtml(subtitle) + '</p>'
            + '</div></div>'
            + '<div class="panel gen-layout">'
            +   '<div class="panel gen-side">'
            +     '<div class="panel-head"><span class="panel-title">选项</span></div>'
            +     formHTML
            +     '<div class="form-actions">'
            +       '<button class="btn-primary" id="btn-gen" type="button">生成</button>'
            +       '<span class="status-msg" id="gen-status"></span>'
            +       '<span class="badge badge-accent" id="file-count" style="display:none"></span>'
            +       '<button class="btn-ghost btn-sm" id="btn-download" type="button" style="display:none">下载</button>'
            +     '</div>'
            +   '</div>'
            +   '<div class="panel">'
            +     '<div class="panel-head"><span class="panel-title">输出</span></div>'
            +     '<div class="file-tabs" id="file-list"></div>'
            +     '<div class="code-box"><pre class="sql-view" id="sql-preview"></pre></div>'
            +   '</div>'
            + '</div>';

        const status = root.querySelector('#gen-status');
        const btn = root.querySelector('#btn-gen');
        const fc = root.querySelector('#file-count');
        const dlBtn = root.querySelector('#btn-download');
        const listEl = root.querySelector('#file-list');
        const previewEl = root.querySelector('#sql-preview');

        btn.addEventListener('click', async () => {
            status.textContent = '生成中…'; status.className = 'status-msg';
            btn.disabled = true;
            try {
                const opts = collectOptions(root);
                const resp = await window.api.post(endpoint, opts);
                fc.style.display = 'inline-flex';
                fc.textContent = resp.count + ' 个文件';
                dlBtn.style.display = 'inline-flex';
                window.renderGenFiles(resp.files, listEl, previewEl);
                status.textContent = '✓ ' + resp.count + ' 个文件'; status.className = 'status-msg ok';
                if (window.toast) window.toast.ok(downloadLabel + ' 生成完成', resp.count + ' 个文件');
            } catch (e) {
                status.textContent = '✗ ' + (e && e.message || e); status.className = 'status-msg fail';
                if (window.toast) window.toast.err('生成失败', e && e.message || '');
            } finally { btn.disabled = false; }
        });

        dlBtn.addEventListener('click', () => { window.location = endpoint.replace(/\/generate$/, '/download'); });
    };
}
```

Each generator page then becomes a thin view:

`views/ddl.js`:
```js
import { buildGeneratorView } from './generator.js';
export const render = buildGeneratorView({
    overline: 'generate · ddl', title: '生成 DDL', subtitle: '从已加载元数据生成目标库建表语句',
    endpoint: '/api/v1/ddl/generate', downloadLabel: 'DDL',
    formHTML: '<div class="field"><label class="field-label"><input type="checkbox" id="opt-no-quote"> 不加引号标识符</label></div>',
    collectOptions: (root) => ({ no_quote_identifiers: root.querySelector('#opt-no-quote').checked ? true : null }),
});
```

(select.js/insert.js analogous — copy select/insert option fields from their SSR templates: insert has batch_size + truncate + no_quote; select has batch_method + page_size + no_quote. Port the exact field markup and collectOptions from the `{{define "content"}}` of each template.)

---

### Task 0: Expose highlightSQL/highlightYAML (and any const kernel) on window

**Files:**
- Modify: `web/static/js/app.js` (append to the window-exposure block)

**Why:** `highlightSQL` (app.js:90) and `highlightYAML` (app.js:113) are top-level `const` — unlike `humanSize`/`escapeHtml`/`renderGenFiles` (top-level `function`, which auto-bind to `window`), `const` does **not** become a `window` property. Strict ES module views referencing `window.highlightSQL`/`window.highlightYAML` would get `undefined`. The generator factory calls `window.renderGenFiles` (fine, closes over the const), but the config/other views need `window.highlightYAML`. Ensure ALL kernel helpers the SPA uses are on `window`.

- [ ] **Step 1: Extend the window-exposure block** in `web/static/js/app.js` (currently appends `window.api/theme/toast/jobUI`) to also expose the const/IIFE helpers:

```js
window.api = api;
window.theme = theme;
window.toast = toast;
window.jobUI = jobUI;
window.highlightSQL = highlightSQL;
window.highlightYAML = highlightYAML;
```

(`humanSize`/`escapeHtml`/`renderGenFiles` are top-level `function` decls and already on `window` — no need to add, but add them too for explicitness/determinism if you wish; the two `const`s are the required ones.)

- [ ] **Step 2: Verify** — `node --check web/static/js/app.js`; `CGO_ENABLED=0 go test ./internal/server/serve/ -count=1`.
- [ ] **Step 3: Commit** — `git add web/static/js/app.js && git commit -m "web: expose highlight helpers on window for SPA views"`.

---

### Task 1: Shared generator view + ddl/select/insert

**Files:**
- Create: `web/static/ui/views/generator.js`, `web/static/ui/views/ddl.js`, `web/static/ui/views/select.js`, `web/static/ui/views/insert.js`
- Modify: `web/static/ui/router.js` (register 3 routes)

**Interfaces:**
- Consumes: `window.api/renderGenFiles/toast`, `../util.js` escapeHtml.
- Produces: `buildGeneratorView(cfg)` (shared factory) + `render` exports for ddl/select/insert.

- [ ] **Step 1: Port the three generator views** using the shared factory. Read `web/templates/ddl.html`, `select.html`, `insert.html` `{{define "content"}}` to copy exact option field markup (checkbox labels, select options, inputs) into each view's `formHTML`, and `collectOptions` to mirror their generate() body fields. Fields: ddl → no_quote; select → batch_method + page_size + no_quote; insert → batch_size + truncate + no_quote.
- [ ] **Step 2: Register routes in router.js** — add to the `routes` array:
```js
    { re: /^\/ddl$/, render: renderDDL, active: '/ddl', title: 'DDL' },
    { re: /^\/select$/, render: renderSelect, active: '/select', title: 'SELECT' },
    { re: /^\/insert$/, render: renderInsert, active: '/insert', title: 'INSERT' },
```
and `import { render as renderDDL } from './views/ddl.js';` (etc.).
- [ ] **Step 3: Verify** — `node --check` on the 4 new files (or eyeball); `CGO_ENABLED=0 go test ./internal/server/serve/ -count=1`.
- [ ] **Step 4: Commit** — `git add web/static/ui/views/generator.js web/static/ui/views/ddl.js web/static/ui/views/select.js web/static/ui/views/insert.js web/static/ui/router.js && git commit -m "web: SPA generator views (ddl/select/insert)"`.

---

### Task 2: migrate view (SPA)

**Files:**
- Create: `web/static/ui/views/migrate.js`
- Modify: `web/static/ui/router.js`

**Interfaces:**
- Consumes: `window.jobUI` (start/cancel/bind/finish/onComplete/logLine), `window.api`, `window.toast`, `window.humanSize`, `../util.js` escapeHtml.
- Produces: `render` for `/migrate`. Note the SSR page's mode (direct vs sql-out) came from `?mode=sql` query; in the SPA the mode is selected via an in-view toggle (`direct`/`sql`) reading `location.hash` or a router param.

- [ ] **Step 1: Port migrate.js** from `web/templates/migrate.html` scripts. Keep: mode toggle (pick a `direct`/`sql` segmented control), pipeline stage board (`pn-source/export/target` + `pl-1/pl-2` flow), prefill from `/api/v1/config/status`, `startMigrate` → `jobUI.start('/api/v1/migrate',{mode,skip_ddl,continue_on_error})`, the `jobUI.logLine` override that advances stages, `jobUI.onComplete` that loads `/api/v1/jobs/<id>/output` and shows the download panel, `downloadSQL()` that does `window.location = '/api/v1/jobs/'+id+'/output/download?format='+...`. Escape all server-derived text (output dir, humanSize output). Use `#/jobs/<id>` for any in-SPA nav.
- [ ] **Step 2: Register** `{ re: /^\/migrate$/, render: renderMigrate, active: '/migrate', title: '迁移' }`.
- [ ] **Step 3: Verify + commit** — `node --check`, serve tests; `git add web/static/ui/views/migrate.js web/static/ui/router.js && git commit -m "web: SPA migrate view"`.

---

### Task 3: export view (SPA)

**Files:**
- Create: `web/static/ui/views/export.js`
- Modify: `web/static/ui/router.js`

**Interfaces:**
- Consumes: `window.jobUI/api/toast`, `../util.js` escapeHtml.
- Produces: `render` for `/export`. Port from `web/templates/export.html`: online/offline tab toggle (`switchMode`), online `startExport` → `jobUI.start('/api/v1/export',{})`, offline form (`off-mode` csv/xlsx toggle, format/dir fields, `startOffline` → `api.post('/api/v1/export/offline', payload)`, render result file tabs + errors). Escape file names / error text.

- [ ] **Step 1: Port export.js.** 
- [ ] **Step 2: Register** `{ re: /^\/export$/, render: renderExport, active: '/export', title: '导出' }`.
- [ ] **Step 3: Verify + commit** — `node --check`, serve tests; `git commit -m "web: SPA export view"`.

---

### Task 4: import view (SPA)

**Files:**
- Create: `web/static/ui/views/import.js`
- Modify: `web/static/ui/router.js`

**Interfaces:**
- Consumes: `window.jobUI/api/toast`.
- Produces: `render` for `/import`. Port from `web/templates/import.html`: `jobUI.bind('#progress-log')`, `startImport` → `jobUI.start('/api/v1/import', {})`.

- [ ] **Step 1: Port import.js.**
- [ ] **Step 2: Register** `{ re: /^\/import$/, render: renderImport, active: '/import', title: '导入' }`.
- [ ] **Step 3: Verify + commit** — `node --check`, serve tests; `git commit -m "web: SPA import view"`.

---

### Task 5: export-metadata view (SPA)

**Files:**
- Create: `web/static/ui/views/exportMetadata.js`
- Modify: `web/static/ui/router.js`

**Interfaces:**
- Consumes: `window.api/toast`, `../util.js` escapeHtml.
- Produces: `render` for `/export-metadata`. Port from `web/templates/export_metadata.html`: load scenarios (`/api/v1/scenarios`) to fill source-type select + DSN hint (`dsn_examples`), prefill from `GET /api/v1/config`, `doExport` → `api.post('/api/v1/metadata/export', {source:{type,dsn,schema}, format, scope})`, render result file tabs (escape file names). Escape all server text.

- [ ] **Step 1: Port exportMetadata.js.**
- [ ] **Step 2: Register** `{ re: /^\/export-metadata$/, render: renderExportMetadata, active: '/export-metadata', title: '元数据导出' }`.
- [ ] **Step 3: Verify + commit** — `node --check`, serve tests; `git commit -m "web: SPA export-metadata view"`.

---

### Task 6: metadata view (SPA)

**Files:**
- Create: `web/static/ui/views/metadata.js`
- Modify: `web/static/ui/router.js`

**Interfaces:**
- Consumes: `window.api/toast`, `../util.js` escapeHtml.
- Produces: `render` for `/metadata`. Port from `web/templates/metadata.html`: metadata-type select (csv/xlsx/database) + toggle fields, source type/DSN/schema select + hint, `loadMeta` → `api.post('/api/v1/metadata/load', payload)`, `renderTables` → `api.get('/api/v1/metadata/tables')`, `showDetail` → `api.get('/api/v1/metadata/tables/<schema>/<name>')` (escape schema/name/columns — note the SSR built onclick with raw schema/name; the SPA must attach listeners, not inline onclick). Escape all server fields (schema, table_name, type, default, pk).
- Note: `showDetail` in the SPA must render into a panel via DOM or use data-attributes + a delegated listener — do NOT use inline `onclick` with unescaped table names (XSS). Use `data-schema`/`data-table` attributes + a single click handler.

- [ ] **Step 1: Port metadata.js.**
- [ ] **Step 2: Register** `{ re: /^\/metadata$/, render: renderMetadata, active: '/metadata', title: '元数据' }`.
- [ ] **Step 3: Verify + commit** — `node --check`, serve tests; `git commit -m "web: SPA metadata view"`.

---

### Task 7: config view (SPA) — the complex one

**Files:**
- Create: `web/static/ui/views/config.js`
- Modify: `web/static/ui/router.js`

**Interfaces:**
- Consumes: `window.api/toast/theme`, `../util.js` escapeHtml, `window.highlightYAML`.
- Produces: `render` for `/config`. This is the largest port. `web/static/js/config.js` is a classic IIFE that rows on hard-coded element IDs and `history.replaceState('/config?scenario=...')` — it cannot be reused as-is (it binds at load and uses SSR URL style). Port its logic into an ES module:

- [ ] **Step 1: Port config.js → views/config.js**, converting the IIFE into `export function render(root, params)`. Key behaviors to preserve: load `/api/v1/scenarios` → allScenarios + dsnExamples + scenario fields; render scenario pills (`#scenario-pills`); `selectScenario(name)` + `renderForm()` (dynamic fields incl. conditional fields + DSN hints); `collectValues()`; live YAML preview via `api.post('/api/v1/scenarios/<name>/build',{values,save:false})` + `window.highlightYAML`; save via `api.post('/api/v1/scenarios/<name>/build',{values,save:true})`; config library load (`GET /api/v1/configs`)/upload (`POST /api/v1/configs`)/delete/load; file picker (`cfg-file`); `applyConfigResp` for loaded/uploaded config. Differences for SPA: (a) pill clicks update `location.hash` to `#/config?scenario=<name>` (or re-render with a params approach — simplest: params carries scenario, and clicking a pill calls a `selectScenario` directly without hard nav); (b) no `history.replaceState('/config?...')` — set `location.hash` or call selectScenario; (c) DSN fields from scenarios (source_dsn/target_dsn) get DSN hints, masked values from `GET /api/v1/config` are already masked (the backend masks), so prefill carefully — do not let masked `******` overwrite a real entered value.
- [ ] **Step 2: Register** `{ re: /^\/config$/, render: renderConfig, active: '/config', title: '配置' }`. Also update the home.js flow-board link `#/config?scenario=validate` — since the config route is `^\/config$` and the router doesn't parse the query, make the config view read the scenario from `params` OR the hash query when present; simplest: change home.js link to `#/config` and let the config view default to 'validate' only if a `?scenario=validate` params is passed. If that's complex, accept the config view defaults to 'migrate' for now and drop the `?scenario=validate` specificity (record as a Minor follow-up).
- [ ] **Step 3: Verify + commit** — `node --check`, serve tests; `git commit -m "web: SPA config view"`.

---

### Task 8: Route completeness + nav sync check

**Files:**
- Modify: `web/static/ui/router.js` (if any route missing), `web/static/ui/index.html` (nav `data-route` sync)
- Test: append to `internal/server/serve/newhandlers_test.go` an assertion that every nav target resolves.

**Interfaces:**
- Produces: a router where every `#/` nav link has a registered view; no "coming soon" for the 12 top-level nav routes.

- [ ] **Step 1: Verify** — grep router.js routes vs the nav links in index.html (config, metadata, export-metadata, ddl, select, insert, migrate, export, import, jobs, docs, home). `docs` may still be a placeholder (it's an external page in Phase 3) — check; if docs has no SPA view, keep it as a placeholder or leave docs as a legacy redirect if acceptable (record). Every other route must resolve.
- [ ] **Step 2: Add a test** — the test list of `/api/v1` routes unchanged; add a lightweight test that `GET /ui` returns 200 and the router registers all main nav routes (assert the embedded index.html contains the nav links, and that router.js imports each view). Since JS isn't executed by Go tests, keep it to structural assertions or skip — prefer a `node --check` + manual reasoning. Add a Go test only if it adds real value (e.g., embed presence of each view file).
- [ ] **Step 3: Commit** — `git commit -m "web: sync SPA nav routes"`.

---

### Task 9: Full dual-track verification + cleanup note

- [ ] **Step 1: Full suite** — `CGO_ENABLED=0 go test ./... -count=1`, `CGO_ENABLED=0 go vet ./...`, `go build ./...`.
- [ ] **Step 2: Build + smoke** — `make build`; serve `/ui` with token; curl `/ui` (200), `/static/ui/views/config.js` (200), `/static/ui/router.js` (200); confirm no view file 404s (spot-check all 9 view files are embedded).
- [ ] **Step 3: Record Phase 2 residual** — note in the Phase-3 plan: remove web/templates/*.html (except base.html) + pages.go SSR page table + web/static/js/config.js, switch `/` to the SPA, and turn off the `docs` placeholder.

## Exit Checklist (spec §11 Phase 2)

- [ ] All 12 top-level nav routes render in the SPA (`#/config, #/metadata, #/export-metadata, #/ddl, #/select, #/insert, #/migrate, #/export, #/import, #/jobs, #/jobs/:id, #/`).
- [ ] No "coming soon" placeholder for the built views.
- [ ] Every server-derived field escaped (XSS gap from SSR templates not replicated).
- [ ] SSR pages still function (dual-track) — not removed.
- [ ] `CGO_ENABLED=0 go test ./...` green; `node --check` clean on new views; no new deps/CDN.
- [ ] The 3 generator pages are DRY via the shared `buildGeneratorView` factory.
