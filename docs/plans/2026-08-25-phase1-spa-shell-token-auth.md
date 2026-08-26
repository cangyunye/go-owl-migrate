# Phase 1: SPA Shell + Token Auth — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add token authentication to the serve server and stand up a build-less static SPA shell (hash router + shared kernel + token prompt) mounted at `/ui` with the home and jobs pages migrated from SSR templates, coexisting with the legacy SSR pages (dual-track).

**Architecture:** No change to the serve/master/worker skeleton. Two additions:
1. Backend token-auth middleware over `/api/v1/*` (except health), with a safe default (no token → refuse non-loopback bind), plus WS token via `?token=`.
2. A static SPA at `/ui`: native ES modules, hash routing, reusing the existing `web/static/js/app.js` runtime as the kernel, token prompt on 401, with a `home` and `jobs` view. SSR pages remain mounted at their existing paths.

**Tech Stack:** Go 1.25+, stdlib `net/http`, existing `nhooyr.io/websocket`, existing `web/static` JS/CSS assets, no build toolchain, no framework, no CDN.

**Spec:** `docs/plans/2026-08-25-web-service-single-node-iteration.md` (§7 Security, §8 Frontend, §11 Phase Plan).

## Global Constraints

- Module path `github.com/cangyunye/go-owl-migrate`; all builds and tests run with `CGO_ENABLED=0`.
- No new runtime dependencies; no Node/build toolchain; no CDN (all assets from the binary itself). Native ES modules only.
- Existing `/api/v1/*` paths must not change shape. The only new behavior is auth enforcement (401 when a token is configured and missing/invalid) and the `/ui` page.
- Token auth: set via `--token` flag or `OWL_MIGRATE_TOKEN` env. When set, every `/api/v1/*` route except `GET /api/v1/health` requires `Authorization: Bearer <token>`; the WebSocket accepts `?token=<token>` (browsers cannot set WS headers). When NOT set, the server refuses to bind a non-loopback host (startup error).
- Dual-track: SSR pages and the SPA coexist. The shared `web/static/js/app.js` api wrapper attaches the stored token uniformly so both the legacy pages and the SPA work once a user has authenticated; a page without a token degrades quietly (silent catch).
- Repo convention: Go doc comments on exported identifiers; plain `testing` + `httptest` tests, no assertion frameworks.
- Test command pattern: `CGO_ENABLED=0 go test ./... -count=1`.
- Commit messages: lowercase imperative, area prefix (`serve:`, `web:`, `build:`).
- Zero new static assets beyond a single SPA entry + router + views; reuse `web/static/css/style.css` and the `app.js` kernel. Do NOT rewrite the existing theme system.

---

### Task 1: Backend token config + safe-default bind enforcement

**Files:**
- Modify: `internal/cmd/serve.go` (add `--token` flag, enforce loopback-when-no-token)
- Test: `internal/cmd/serve.go` (via a small extracted helper + unit test; the existing `serve_lock_test.go` pattern)

**Interfaces:**
- Produces: `serve.Config.Token string`, and a helper `requireToken(host string, token string) error` (or logic in `serveCmd`) that refuses a non-loopback `host` when `token == ""`.

- [ ] **Step 1: Write the failing test**

Create `internal/cmd/serve_auth_test.go`:

```go
package cmd

import "testing"

func TestRequireBindHost_Policy(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		token   string
		wantErr bool
	}{
		{"loopback no token ok", "127.0.0.1", "", false},
		{"loopback with token ok", "127.0.0.1", "s3cret", false},
		{"localhost no token ok", "localhost", "", false},
		{"non-loopback no token refused", "0.0.0.0", "", true},
		{"non-loopback with token ok", "0.0.0.0", "s3cret", false},
		{"private net no token refused", "192.168.1.10", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := requireBindHost(tc.host, tc.token)
			if tc.wantErr && err == nil {
				t.Errorf("requireBindHost(%q,%q) error = nil, want error", tc.host, tc.token)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("requireBindHost(%q,%q) error = %v, want nil", tc.host, tc.token, err)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
CGO_ENABLED=0 go test ./internal/cmd/ -run TestRequireBindHost_Policy -count=1
```

Expected: FAIL — `requireBindHost undefined`.

- [ ] **Step 3: Implement `requireBindHost`**

Create `internal/cmd/serve_auth.go`:

```go
package cmd

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/server/serve"
)

// requireBindHost refuses to bind a non-loopback address when no token is set,
// so an unauthenticated server can never be exposed on a shared network.
func requireBindHost(host, token string) error {
	if token != "" {
		return nil
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		// "localhost" and DNS names are treated as loopback-safe here; only
		// explicitly non-loopback IPs are refused.
		if host == "localhost" || host == "" {
			return nil
		}
		// Resolve; if it resolves to a loopback IP, allow.
		if addrs, err := net.LookupIP(host); err == nil {
			for _, a := range addrs {
				if a.IsLoopback() {
					return nil
				}
			}
		}
		return fmt.Errorf("refusing to bind %s without an auth token (use --token or OWL_MIGRATE_TOKEN); refusing to expose an unauthenticated server on a non-loopback address", host)
	}
	if ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("refusing to bind %s without an auth token (use --token or OWL_MIGRATE_TOKEN); refusing to expose an unauthenticated server on a non-loopback address", host)
}

// dummy to keep compile if unused imports; removed by goimports as needed
var _ = http.StatusOK
```

(Remove the `dummy` line — it is a placeholder; ensure only `fmt`, `net`, `strings` stay. `http` is not needed here.)

- [ ] **Step 4: Wire `--token` into serveCmd**

In `internal/cmd/serve.go`:
1. Add `token string` to the var block and `cmd.Flags().StringVar(&token, "token", "", "auth token (also OWL_MIGRATE_TOKEN); required to bind non-loopback")`.
2. Resolve env: after flags, `if token == "" { token = os.Getenv("OWL_MIGRATE_TOKEN") }`.
3. Call `requireBindHost(host, token)` before starting the HTTP server; on error, return it.
4. Pass `Token: token` into `serve.Config{}`.

- [ ] **Step 5: Run the tests**

```bash
CGO_ENABLED=0 go test ./internal/cmd/ -count=1
CGO_ENABLED=0 go build ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cmd/serve.go internal/cmd/serve_auth.go internal/cmd/serve_auth_test.go
git commit -m "serve: add token flag and refuse unauthenticated non-loopback bind"
```

---

### Task 2: Auth middleware on the serve handler

**Files:**
- Modify: `internal/server/serve/server.go` (`Config` + `Server` + `Handler` wrap)
- Test: `internal/server/serve/newhandlers_test.go`

**Interfaces:**
- Consumes: `serve.Config.Token` (from Task 1).
- Produces: when `Token != ""`, `Handler()` wraps the mux so every request whose path starts with `/api/v1/` and is not `GET /api/v1/health` requires `Authorization: Bearer <token>`. Unauthorized → 401 with `{"error":"unauthorized"}`. Health, static and page routes are exempt.

- [ ] **Step 1: Write the failing tests**

Append to `internal/server/serve/newhandlers_test.go`:

```go
func TestAuth_RequiresToken(t *testing.T) {
	store, err := service.NewJobStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	srv := NewServer(Config{Store: store, Token: "s3cret"})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	client := ts.Client()
	// No token → 401
	resp, err := client.Get(ts.URL + "/api/v1/jobs")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-token status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// Correct token → 200
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/jobs", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// Wrong token → 401
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/jobs", nil)
	req.Header.Set("Authorization", "Bearer nope")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong-token status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// Health is exempt
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/health", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAuth_DisabledWhenNoToken(t *testing.T) {
	store, err := service.NewJobStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	srv := NewServer(Config{Store: store}) // Token empty
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := ts.Client().Get(ts.URL + "/api/v1/jobs")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("no-token-configured status = %d, want 200", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
CGO_ENABLED=0 go test ./internal/server/serve/ -run TestAuth -count=1
```

Expected: FAIL — `TestAuth_RequiresToken` gets 200 on the no-token request (auth not enforced yet).

- [ ] **Step 3: Implement the middleware**

In `internal/server/serve/server.go`:
1. Add `Token string` to `Config`.
2. Add `token string` field to `Server`; set it in `NewServer`.
3. In `Handler()`, wrap the mux before returning:

```go
	if s.token != "" {
		return withAuth(mux, s.token)
	}
	return mux
```

Add:

```go
// withAuth enforces a Bearer token on every /api/v1/* route except health,
// so the admin UI cannot be hit by unauthenticated clients. Static assets,
// the SPA shell, and docs pages are exempt (they are served for the browser
// that must first present the token).
func withAuth(next http.Handler, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/v1/") && p != "/api/v1/health" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer "+token {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
```

Add `strings` to the imports if missing.

Also update the WS route: `GET /api/v1/jobs/{id}/ws` — a browser WebSocket cannot set an `Authorization` header, so the token comes via `?token=`. In `handleWebSocket`, when auth is enabled, verify `r.URL.Query().Get("token") == s.token`. Add that check inside `handleWebSocket`:

```go
	if s.token != "" && r.URL.Query().Get("token") != s.token {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
```

- [ ] **Step 4: Run tests**

```bash
CGO_ENABLED=0 go test ./internal/server/serve/ -count=1
CGO_ENABLED=0 go build ./...
```

Expected: PASS. Note the WebSocket connection from a test (if any) must pass `?token=`. Check: existing `websocket_test.go` connects without a token — but that server has `Token: ""`, so auth is disabled and the check is skipped. Confirm `newTestWSServer` uses no token (it does: `NewServer(Config{Store: store})`).

- [ ] **Step 5: Commit**

```bash
git add internal/server/serve/server.go internal/server/serve/newhandlers_test.go
git commit -m "serve: enforce bearer token on /api/v1 routes and ws"
```

---

### Task 3: The WebSocket token plumbing (view-side) — kernel helper

**Files:**
- Modify: `web/static/js/app.js` (api wrapper attaches stored token; add `api.token` + `wsURL` helper)
- Modify: `web/static/js/ws.js` (new, optional — or fold into app.js per §8)

Given the dual-track decision (shared app.js carries the token), keep it in `app.js` so legacy pages inherit it.

**Interfaces:**
- Produces: `api._token` get/set; api get/post/put/del attach `Authorization: Bearer <token>` when set; `api.wsURL(path)` returns a WebSocket URL with `?token=` appended when a token is set.

- [ ] **Step 1: Update the api wrapper**

In `web/static/js/app.js`, extend the `api` object (lines ~6-30):

```js
const api = {
    _token: '',
    setToken(t) { this._token = t || ''; try { localStorage.setItem('owl-token', this._token); } catch (e) {} },
    getToken() { try { return localStorage.getItem('owl-token') || ''; } catch (e) { return this._token; } },
    async _handle(resp) {
        if (resp.status === 401) {
            const ev = new CustomEvent('owl-auth-required');
            window.dispatchEvent(ev);
            throw new Error('unauthorized');
        }
        if (!resp.ok) {
            const err = await resp.json().catch(() => ({}));
            throw new Error(err.error || `${resp.status} ${resp.statusText}`);
        }
        return resp.json();
    },
    _headers(extra) {
        const h = Object.assign({}, extra || {});
        const t = this.getToken();
        if (t) h['Authorization'] = 'Bearer ' + t;
        return h;
    },
    get(path) { return fetch(path).then(r => this._handle(r)); },
    post(path, body) {
        return fetch(path, { method: 'POST', headers: this._headers({ 'Content-Type': 'application/json' }), body: JSON.stringify(body || {}) }).then(r => this._handle(r));
    },
    put(path, body) {
        return fetch(path, { method: 'PUT', headers: this._headers({ 'Content-Type': 'application/json' }), body: JSON.stringify(body || {}) }).then(r => this._handle(r));
    },
    del(path) { return fetch(path, { method: 'DELETE', headers: this._headers() }).then(r => this._handle(r)); },
    wsURL(path) {
        const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
        let u = proto + '//' + location.host + path;
        const t = this.getToken();
        if (t) u += (u.indexOf('?') >= 0 ? '&' : '?') + 'token=' + encodeURIComponent(t);
        return u;
    }
};
```

- [ ] **Step 2: Add the auth-prompt listener**

At the end of `app.js` (before the theme wiring IIFE, or after), add:

```js
window.addEventListener('owl-auth-required', () => {
    if (window.authPrompt) window.authPrompt.show();
});
```

- [ ] **Step 3: Run a quick check**

There is no Go test for JS. Verify the file parses by loading it — for now, rely on a later SPA integration task. Provide no automated test here.

- [ ] **Step 4: Commit**

```bash
git add web/static/js/app.js
git commit -m "web: api kernel attaches bearer token and emits auth-required"
```

---

### Task 4: SPA shell — index.html + router + token prompt at /ui

**Files:**
- Create: `web/static/ui/index.html`
- Create: `web/static/ui/router.js`
- Create: `web/static/ui/views/home.js`
- Create: `web/static/ui/views/jobs.js`
- Create: `web/static/ui/views/jobDetail.js`
- Modify: `internal/server/serve/pages.go` (serve /ui/ from a sub-FS; register `GET /ui` and `GET /ui/`)

**Interfaces:**
- Consumes: `window.api` from `web/static/js/app.js` (loaded first), the existing `style.css`, the existing `highlightSQL`/`highlightYAML`, `toast`, `theme`, `jobUI` kernels.
- Produces: a hash-routed SPA at `/ui` with views `#/` (home), `#/jobs`, `#/jobs/:id`.

- [ ] **Step 1: Write the failing test**

Append to `internal/server/serve/newhandlers_test.go`:

```go
func TestSPA_UIShellServed(t *testing.T) {
	srv := NewServer(Config{})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := ts.Client().Get(ts.URL + "/ui")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ui status = %d, want 200", resp.StatusCode)
	}

	// index asset within /ui/static/ or /static/ui/
	r2, err := ts.Client().Get(ts.URL + "/static/ui/router.js")
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("GET /static/ui/router.js status = %d, want 200", r2.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
CGO_ENABLED=0 go test ./internal/server/serve/ -run TestSPA_UIShellServed -count=1
```

Expected: FAIL — 404 on `/ui`.

- [ ] **Step 3: Create the SPA assets**

Using the existing `web/static/css/style.css` classes (the SSR pages use `.sidebar`, `.main`, `.topbar`, `.content`, `.page-head`, `.panel`, `.data-table`, `.term`, `.toast-root`, `.status-dot`, `.st-ok/run/warn/fail`, `.btn-*`, `.tabs`, `.flow-board`, `.sys-strip`). The SPA shell structure mirrors `base.html` (aside sidebar + main content) but is static and routes via `#`.

- [ ] **Step 4: Serve the SPA**

In `internal/server/serve/pages.go` `registerPages`, after the existing `staticFS` serve line, add a sub-FS for `web/static/ui` is NOT where the assets live if we place them under `web/static/ui`. Simpler: put the SPA entry at `web/static/ui/index.html` and static assets under `web/static/ui/`, but they are already served by the `fs.Sub(web.FS, "static")` FileServer at `/static/`. So the SPA HTML can live at `web/static/ui/index.html` and be reachable at `/static/ui/index.html` already. We only need to route `GET /ui` to that file, and let the SPA load `/static/js/app.js`, `/static/css/style.css`.

Add to `registerPages`:

```go
	// SPA shell (Phase 1). /ui serves the static index; the SPA is fully
	// client-side (hash routing), so only this entry is needed.
	mux.HandleFunc("GET /ui", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data, err := web.FS.ReadFile("static/ui/index.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write(data)
	})
	mux.HandleFunc("GET /ui/{$}", func(w http.ResponseWriter, r *http.Request) { ... same ... })
```

Import `github.com/cangyunye/go-owl-migrate/web`.

- [ ] **Step 5: Run tests**

```bash
CGO_ENABLED=0 go test ./internal/server/serve/ -run TestSPA_UIShellServed -count=1
CGO_ENABLED=0 go build ./...
```

Expected: PASS (ensure `/static/ui/router.js` exists in `web/static/ui/` and is embedded — it is, since `static/*` is embedded).

- [ ] **Step 6: Commit**

```bash
git add web/static/ui/ internal/server/serve/pages.go internal/server/serve/newhandlers_test.go
git commit -m "web: add build-less SPA shell at /ui with hash router"
```

---

### Task 5: Home view + jobs list view (SPA)

**Files:**
- Modify: `web/static/ui/views/home.js`, `web/static/ui/views/jobs.js` (functional content migrated from `web/templates/index.html` and `web/templates/jobs.html`)

**Interfaces:**
- Consumes: `api` kernel, `statusBadge` helper (move it into a shared `web/static/ui/util.js` and import in views, or define per view). Prefer a small shared util.

- [ ] **Step 1: Add shared util**

Create `web/static/ui/util.js`:

```js
export function statusBadge(s) {
    const map = {
        running: ['st-run', true], cancelling: ['st-warn', true],
        completed: ['st-ok', false], failed: ['st-fail', false],
        interrupted: ['st-warn', false], cancelled: ['st-warn', false]
    };
    const m = map[s] || ['st-run', false];
    return '<span class="' + m[0] + '"><span class="status-dot' + (m[1] ? ' pulse' : '') + '"></span>' + s + '</span>';
}
```

- [ ] **Step 2: home.js**

Port `index.html`'s `loadRecent` + `loadSys` logic and the flow board markup (a static HTML template string) into `home.js`, using `api.*` and `statusBadge`. Render into a `#view` container.

- [ ] **Step 3: jobs.js**

Port `jobs.html`: filter tabs + `loadJobs()` + `render()` with the shared `statusBadge`.

- [ ] **Step 4: Verify**

There is no Go test for JS in Phase 1. Manual verification is the exit gate for the page. Do not invent a framework test. Confirm the two views are wired in router.js and load without console errors by a drive-by check (optional). Commit after wiring.

- [ ] **Step 5: Commit**

```bash
git add web/static/ui/
git commit -m "web: SPA home and jobs views"
```

---

### Task 6: Job detail view (SPA)

**Files:**
- Create/modify: `web/static/ui/views/jobDetail.js` (port `web/templates/job_detail.html`)

**Interfaces:**
- Consumes: `api.*`, `jobUI` kernel (for the WS streaming) and shared `statusBadge`/`escapeHtml`.

- [ ] **Step 1: Port jobDetail.js**

Port `job_detail.html`: load job, checkpoints, events; live WS when active (use `api.wsURL('/api/v1/jobs/'+id+'/ws')` so the token is appended); cancel/resume buttons.

- [ ] **Step 2: Wire in router**

Add the `#/jobs/:id` route in router.js to render jobDetail.

- [ ] **Step 3: Commit**

```bash
git add web/static/ui/ router.js web/static/ui/router.js
git commit -m "web: SPA job detail view"
```

---

### Task 7: Verify dual-track + auth end to end (smoke)

- [ ] **Step 1: Run full Go tests**

```bash
CGO_ENABLED=0 go test ./... -count=1
CGO_ENABLED=0 go vet ./...
go fmt ./...
```

- [ ] **Step 2: Build + smoke**

```bash
make build
OWL_MIGRATE_HOME=$(mktemp -d) ./build/darwin-arm64/owl-migrate serve --port 18080 --token s3cret &
sleep 1
curl -sf localhost:18080/api/v1/health
curl -s -o /dev/null -w "%{http_code}\n" localhost:18080/api/v1/jobs          # expect 401
curl -s -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer s3cret" localhost:18080/api/v1/jobs   # expect 200
curl -sf -o /dev/null -w "%{http_code}\n" localhost:18080/ui                 # expect 200
curl -sf -o /dev/null -w "%{http_code}\n" localhost:18080/static/ui/router.js # expect 200
kill $!
```

- [ ] **Step 3: No-token bind refusal smoke**

```bash
OWL_MIGRATE_HOME=$(mktemp -d) ./build/darwin-arm64/owl-migrate serve --host 0.0.0.0 --port 18081; echo "exit=$?"   # expect non-zero
```

- [ ] **Step 4: Commit any adjustments**

## Exit Checklist (spec §11 Phase 1)

- [ ] `CGO_ENABLED=0 go test ./...` fully green
- [ ] `/ui` serves the SPA shell; `#/` (home) and `#/jobs`, `#/jobs/:id` render
- [ ] Token auth: 401 without token, 200 with correct token on `/api/v1/*`; WS via `?token=`
- [ ] No-token non-loopback bind refused
- [ ] Legacy SSR pages still function (dual-track) once authenticated
- [ ] No new dependencies; all assets embedded; no CDN
