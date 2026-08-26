/* ============================================================
   owl-migrate · console front-end runtime
   ============================================================ */

/* ── api: fetch wrappers ─────────────────────────────────── */
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
    get(path) { return fetch(path, { headers: this._headers() }).then(r => this._handle(r)); },
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

/* ── util ────────────────────────────────────────────────── */
function humanSize(bytes) {
    if (bytes === null || bytes === undefined) return '-';
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
    if (bytes < 1024 * 1024 * 1024) return (bytes / 1024 / 1024).toFixed(1) + ' MB';
    return (bytes / 1024 / 1024 / 1024).toFixed(2) + ' GB';
}

function escapeHtml(s) {
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

/* ── theme ───────────────────────────────────────────────── */
const theme = {
    current() { return document.documentElement.getAttribute('data-theme') || 'dark'; },
    set(t) {
        document.documentElement.setAttribute('data-theme', t);
        try { localStorage.setItem('owl-theme', t); } catch (e) {}
    },
    toggle() { this.set(this.current() === 'dark' ? 'light' : 'dark'); }
};

/* ── toast ───────────────────────────────────────────────── */
const toast = {
    show(title, msg, type) {
        type = type || 'info';
        const root = document.getElementById('toast-root');
        if (!root) { if (type === 'err') alert(title + (msg ? ': ' + msg : '')); return; }
        const el = document.createElement('div');
        el.className = 'toast ' + (type === 'ok' ? 'ok' : type === 'err' ? 'err' : type === 'warn' ? 'warn' : '');
        el.innerHTML = '<div><div class="toast-title">' + escapeHtml(title) + '</div>' +
            (msg ? '<div>' + escapeHtml(msg) + '</div>' : '') + '</div>';
        root.appendChild(el);
        setTimeout(() => {
            el.classList.add('out');
            setTimeout(() => el.remove(), 260);
        }, type === 'err' ? 6000 : 3200);
    },
    ok(title, msg) { this.show(title, msg, 'ok'); },
    err(title, msg) { this.show(title, msg, 'err'); },
    warn(title, msg) { this.show(title, msg, 'warn'); }
};

/* ── SQL / YAML lightweight highlighting ─────────────────── */
const highlightSQL = (function () {
    const KW = new Set(('SELECT FROM WHERE INSERT INTO VALUES CREATE TABLE ALTER DROP INDEX VIEW ' +
        'PRIMARY KEY NOT NULL DEFAULT AND OR ORDER BY GROUP HAVING LIMIT OFFSET FETCH NEXT ROWS ONLY ' +
        'TRUNCATE CONSTRAINT FOREIGN REFERENCES UNIQUE SEQUENCE TRIGGER FUNCTION PROCEDURE PACKAGE ' +
        'SYNONYM MATERIALIZED COMMENT ON AS IS BEGIN END CASCADE SET WITH CURSOR OPEN CLOSE LOOP EXIT ' +
        'WHEN THEN ELSE IF DECLARE RETURN RETURNS NUMBER VARCHAR2 VARCHAR CHAR DATE TIMESTAMP CLOB BLOB ' +
        'INTEGER INT BIGINT SMALLINT DECIMAL NUMERIC TEXT BOOLEAN FLOAT DOUBLE PRECISION NVARCHAR NCHAR ' +
        'GRANT REVOKE TO IDENTIFIED TABLESPACE STORAGE PCTFREE INITRANS LOGGING NOLOGGING PARALLEL ' +
        'USING BTREE ASC DESC IN EXISTS BETWEEN LIKE ISNULL COALESCE CAST INTERVAL DAY MONTH YEAR HOUR ' +
        'MINUTE SECOND ZONE LOCAL GLOBAL TEMPORARY PRESERVE COMMIT ROLLBACK SAVEPOINT UPDATE DELETE').split(' '));
    const RE = /(--[^\n]*)|('(?:[^']|'')*')|("(?:[^"]|"")*")|(\b\d+(?:\.\d+)?\b)|([A-Za-z_][A-Za-z0-9_$#]*)/g;
    return function (text) {
        return escapeHtml(text).replace(RE, function (m, com, str, qi, num, word) {
            if (com) return '<span class="tk-com">' + m + '</span>';
            if (str) return '<span class="tk-str">' + m + '</span>';
            if (qi) return '<span class="tk-qi">' + m + '</span>';
            if (num) return '<span class="tk-num">' + m + '</span>';
            if (word && KW.has(word.toUpperCase())) return '<span class="tk-kw">' + m + '</span>';
            return m;
        });
    };
})();

const highlightYAML = (function () {
    return function (text) {
        return escapeHtml(text).split('\n').map(function (line) {
            if (/^\s*#/.test(line)) return '<span class="tk-com">' + line + '</span>';
            return line.replace(/^(\s*)([\w.\-\/]+)(:)(\s*)(.*)$/, function (m, ind, key, colon, sp, val) {
                let v = val;
                if (/^(true|false|null|~)$/i.test(val)) v = '<span class="tk-bool">' + val + '</span>';
                else if (/^-?\d+(\.\d+)?$/.test(val)) v = '<span class="tk-num">' + val + '</span>';
                else if (val) v = '<span class="tk-str">' + val + '</span>';
                return ind + '<span class="tk-key">' + key + '</span>' + colon + sp + v;
            });
        }).join('\n');
    };
})();

/* ── global config status bar (topbar chips) ─────────────── */
(async function renderConfigBar() {
    const bar = document.getElementById('config-bar');
    if (!bar) return;
    try {
        const st = await api.get('/api/v1/config/status');
        const chips = [];
        chips.push('<span class="cfg-chip" title="' + escapeHtml(st.path || '') + '"><span class="dot ' +
            (st.metadata_loaded ? '' : 'off') + '"></span>' +
            (st.target_dialect ? '<b>' + escapeHtml(st.target_dialect) + '</b>' : '未配置') + '</span>');
        if (st.metadata_loaded) {
            chips.push('<span class="cfg-chip hide-sm">' + st.table_count + ' 张表</span>');
        }
        if (st.source_type) {
            chips.push('<span class="cfg-chip hide-sm">源 <b>' + escapeHtml(st.source_type) + '</b></span>');
        }
        chips.push('<a class="cfg-bar-link" href="/config">编辑配置</a>');
        bar.innerHTML = chips.join('');
    } catch (e) { /* best-effort */ }
})();

/* ── sidebar collapse + theme toggle wiring ──────────────── */
(function () {
    const btn = document.getElementById('collapse-btn');
    if (document.documentElement.classList.contains('pre-collapsed')) {
        document.body.classList.add('nav-collapsed');
        document.documentElement.classList.remove('pre-collapsed');
    }
    if (btn) {
        btn.addEventListener('click', () => {
            document.body.classList.toggle('nav-collapsed');
            try {
                localStorage.setItem('owl-nav', document.body.classList.contains('nav-collapsed') ? 'collapsed' : 'expanded');
            } catch (e) {}
        });
    }
    const tbtn = document.getElementById('theme-btn');
    if (tbtn) tbtn.addEventListener('click', () => theme.toggle());
})();

/* ── auth: prompt when backend rejects the token ──────────── */
window.addEventListener('owl-auth-required', () => {
    if (window.authPrompt) window.authPrompt.show();
});

/* ── jobUI: start / stream / cancel a job ────────────────── */
const jobUI = {
    jobId: null,
    ws: null,
    logEl: null,
    onComplete: null,

    bind(logSelector) { this.logEl = document.querySelector(logSelector); },

    async start(endpoint, body) {
        const resp = await api.post(endpoint, body || {});
        this.jobId = resp.job_id;
        const badge = document.getElementById('job-id-badge');
        if (badge) badge.textContent = resp.job_id;
        const panel = document.getElementById('progress-panel');
        if (panel) panel.style.display = 'block';
        const start = document.getElementById('btn-start');
        const cancel = document.getElementById('btn-cancel');
        if (start) start.style.display = 'none';
        if (cancel) cancel.style.display = 'inline-flex';
        this.logLine('info', '任务已启动', resp.job_id);
        this.connect(resp.job_id);
        return resp;
    },

    async cancel() {
        if (!this.jobId) return;
        await api.del('/api/v1/jobs/' + this.jobId);
        this.logLine('warn', '已发送取消请求', '');
    },

    connect(jobId) {
        this.ws = new WebSocket(api.wsURL('/api/v1/jobs/' + jobId + '/ws'));
        this.ws.onmessage = (e) => {
            const m = JSON.parse(e.data);
            if (m.type === 'progress') {
                const tbl = ((m.schema || '') + (m.table ? '.' + m.table : '')).trim();
                this.logLine('info', m.event + (tbl ? '  ' + tbl : ''), (m.rows !== undefined && m.rows !== null) ? m.rows + ' rows' : '');
            } else if (m.type === 'complete') {
                this.logLine('ok', '任务完成', m.status || '');
                toast.ok('任务完成', m.status || '');
                this.finish();
                if (this.onComplete) this.onComplete(this.jobId);
            } else if (m.type === 'cancelled') {
                this.logLine('warn', '任务已取消', '');
                toast.warn('任务已取消', '');
                this.finish();
            } else if (m.type === 'error') {
                this.logLine('err', m.error || '未知错误', '');
                toast.err('任务失败', m.error || '');
                this.finish();
            }
        };
        this.ws.onclose = () => this.logLine('dim', '连接关闭', '');
    },

    finish() {
        const start = document.getElementById('btn-start');
        const cancel = document.getElementById('btn-cancel');
        if (start) start.style.display = 'inline-flex';
        if (cancel) cancel.style.display = 'none';
        if (this.ws) this.ws.close();
    },

    /* structured terminal line: [tag] message  detail */
    logLine(kind, msg, detail) {
        if (!this.logEl) return;
        const line = document.createElement('span');
        line.className = 'term-line';
        const time = new Date().toTimeString().slice(0, 8);
        line.innerHTML = '<span class="ln-dim">' + time + '</span>  ' +
            '<span class="ln-' + kind + '">' + escapeHtml(msg) + '</span>' +
            (detail ? '  <span class="ln-dim">' + escapeHtml(String(detail)) + '</span>' : '');
        this.logEl.appendChild(line);
        this.logEl.scrollTop = this.logEl.scrollHeight;
    },

    log(text) { this.logLine('info', text, ''); }
};

/* ── renderGenFiles: file tabs + highlighted SQL preview ─── */
function renderGenFiles(files, listEl, previewEl) {
    listEl.innerHTML = '';
    if (!files || files.length === 0) {
        previewEl.innerHTML = '<span class="ln-dim">（无输出文件）</span>';
        return;
    }
    files.forEach((f, i) => {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'file-tab' + (i === 0 ? ' active' : '');
        btn.textContent = f.name;
        btn.addEventListener('click', () => {
            listEl.querySelectorAll('.file-tab').forEach(b => b.classList.remove('active'));
            btn.classList.add('active');
            previewEl.innerHTML = highlightSQL(f.content || '');
        });
        listEl.appendChild(btn);
    });
    previewEl.innerHTML = highlightSQL(files[0].content || '');
}

/* expose kernel globals to window so strict ES modules can use them */
window.api = api;
window.theme = theme;
window.toast = toast;
window.jobUI = jobUI;
window.highlightSQL = highlightSQL;
window.highlightYAML = highlightYAML;
window.humanSize = humanSize;
window.escapeHtml = escapeHtml;
window.renderGenFiles = renderGenFiles;
