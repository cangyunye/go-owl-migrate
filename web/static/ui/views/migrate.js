/* owl-migrate SPA · migrate view (ported from web/templates/migrate.html) */
import { modalFocus } from '../util.js';
/* ============================================================
   Ports: mode toggle (direct / sql-out), pipeline stage board,
   prefill from /api/v1/config/status, startMigrate -> jobUI.start,
   jobUI.logLine/finish/onComplete overrides, and the SQL-output
   download panel (sql-out mode only).

   jobUI is a singleton shared across views; its override methods
   are reset to the kernel originals at the top of every render so
   stale overrides never leak across navigation.
   ============================================================ */

/* Kernel originals — captured once at module load (app.js runs first). */
const ORIG_LOG_LINE = window.jobUI.logLine;
const ORIG_FINISH = window.jobUI.finish;
const ORIG_ON_COMPLETE = window.jobUI.onComplete;
const ORIG_ON_EVENT = window.jobUI.onEvent;

/* Module-level mode keeps the toggle across re-renders (user pref).
   'direct' | 'sql-out'. Defaults to 'direct' per SSR initial state. */
let mode = 'direct';

/* Escape handler for the pre-start confirmation, removed on each re-render. */
let confirmEscHandler = null;

/* In-flight row-count stream (module scope so a re-render aborts it instead of
   leaving the server counting into a discarded view). */
let rowCountAbort = null;

function setStage(root, id, cls) {
    const el = root.querySelector('#' + id);
    if (!el) return;
    el.classList.remove('active', 'done', 'failed', 'cancelled');
    if (cls) el.classList.add(cls);
}

function flow(root, id, on) {
    const el = root.querySelector('#' + id);
    if (el) el.classList.toggle('flowing', !!on);
}

export function render(root /*Element*/, params) {
    /* ── reset overrides: no cross-view leakage ────────────────── */
    window.jobUI.logLine = ORIG_LOG_LINE;
    window.jobUI.finish = ORIG_FINISH;
    window.jobUI.onComplete = ORIG_ON_COMPLETE;
    window.jobUI.onEvent = ORIG_ON_EVENT;
    /* the confirm overlay lives in this view; drop any stale scroll lock */
    document.body.classList.remove('modal-open');
    /* abort a count stream left running by the previous render */
    if (rowCountAbort) { rowCountAbort.abort(); rowCountAbort = null; }

    root.innerHTML = ''
        + '<div class="page-head reveal" style="--i:0">'
        +   '<div>'
        +     '<div class="overline">执行 · 迁移</div>'
        +     '<h1>数据迁移</h1>'
        +     '<p class="subtitle">端到端迁移：源库 → 导出 → 目标库（或生成 INSERT SQL）</p>'
        +   '</div>'
        + '</div>'

        + '<nav class="tabs reveal" style="--i:1">'
        +   '<button type="button" class="tab" data-mode="direct" aria-pressed="false">直接迁移</button>'
        +   '<button type="button" class="tab" data-mode="sql" aria-pressed="false">SQL 输出</button>'
        + '</nav>'
        + '<p class="mode-desc reveal" style="--i:1" id="mode-desc"></p>'

        + '<div class="panel reveal" style="--i:2">'
        +   '<div class="panel-head"><span class="panel-title">迁移管道</span></div>'
        +   '<div class="pipeline" id="pipeline">'
        +     '<div class="pipe-node" id="pn-source">'
        +       '<div class="pn-name"><span class="status-dot" style="color:var(--cyan)"></span>源库</div>'
        +       '<div class="pn-val" id="pv-source">—</div>'
        +       '<div class="pn-sub" id="ps-source">读取元数据与数据</div>'
        +     '</div>'
        +     '<div class="pipe-link" id="pl-1"></div>'
        +     '<div class="pipe-node" id="pn-export">'
        +       '<div class="pn-name"><span class="status-dot" style="color:var(--amber)"></span>导出</div>'
        +       '<div class="pn-val" id="pv-export">—</div>'
        +       '<div class="pn-sub" id="ps-export">CSV 中转 / 游标分页</div>'
        +     '</div>'
        +     '<div class="pipe-link" id="pl-2"></div>'
        +     '<div class="pipe-node" id="pn-target">'
        +       '<div class="pn-name"><span class="status-dot" style="color:var(--green)"></span>目标库</div>'
        +       '<div class="pn-val" id="pv-target">—</div>'
        +       '<div class="pn-sub" id="ps-target">批量写入 / INSERT SQL</div>'
        +     '</div>'
        +   '</div>'

        +   '<div class="tbl-section">'
        +     '<div class="tbl-head">'
        +       '<span class="tbl-title">表选择 <span class="badge badge-accent" id="tbl-count">—</span></span>'
        +       '<input id="tbl-filter" type="text" placeholder="过滤表名…" spellcheck="false" autocomplete="off">'
        +       '<button type="button" class="btn-ghost btn-sm" id="tbl-all">全选</button>'
        +       '<button type="button" class="btn-ghost btn-sm" id="tbl-none">清空</button>'
        +       '<button type="button" class="btn-ghost btn-sm" id="tbl-reload" title="按当前配置（源库 / Schema）重新抽取表列表">重新加载</button>'
        +       '<button type="button" class="btn-ghost btn-sm" id="tbl-count-all" title="从上到下依次 COUNT(*) 统计每张表的实际行数">全量统计</button>'
        +     '</div>'
        +     '<div id="tbl-status" class="field-help" role="status">加载表列表…</div>'
        +     '<div id="tbl-list" class="tbl-list" style="display:none"></div>'
        +   '</div>'

        +   '<div class="field">'
        +     '<label class="check"><input type="checkbox" id="opt-skip-ddl"> 跳过建表 <code>--skip-ddl</code>（仅导数据）</label>'
        +   '</div>'
        +   '<div class="field">'
        +     '<label class="check"><input type="checkbox" id="opt-continue-on-error"> 部分表失败不中断 <code>--continue-on-error</code></label>'
        +   '</div>'
        +   '<div class="form-actions">'
        +     '<button type="button" class="btn-ghost" id="btn-preflight">预检</button>'
        +     '<button class="btn-primary" id="btn-start" type="button">'
        +       '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M8 5v14l11-7z"/></svg>'
        +       '开始迁移'
        +     '</button>'
        +     '<button class="btn-danger" id="btn-cancel" type="button" style="display:none">取消</button>'
        +   '</div>'
        + '</div>'

        + '<div id="preflight-panel" class="panel reveal" style="--i:3;display:none">'
        +   '<div class="panel-head"><span class="panel-title">预检结果 <span class="badge badge-accent" id="pf-badge"></span></span></div>'
        +   '<div id="preflight-list"></div>'
        + '</div>'

        + '<div id="progress-panel" class="panel reveal" style="--i:3;display:none">'
        +   '<div class="panel-head">'
        +     '<span class="panel-title">实时进度 <span class="badge badge-accent" id="job-id-badge"></span></span>'
        +     '<span class="live-dot"></span>'
        +   '</div>'
        +   '<div id="progress-log" class="term" role="log" aria-live="polite"></div>'
        + '</div>'

        + '<div id="download-panel" class="panel reveal" style="--i:4;display:none">'
        +   '<div class="panel-head"><span class="panel-title">SQL 输出结果</span></div>'
        +   '<p id="output-summary" class="field-help" style="margin-bottom:14px"></p>'
        +   '<div class="form-row">'
        +     '<label>下载格式</label>'
        +     '<select id="dl-format">'
        +       '<option value="tar.gz">tar.gz（压缩，推荐大文件/多文件）</option>'
        +       '<option value="zip">zip</option>'
        +       '<option value="raw">原始 .sql（仅单文件）</option>'
        +     '</select>'
        +     '<button class="btn-primary" type="button" id="btn-download">下载</button>'
        +   '</div>'
        + '</div>'

        + '<div class="dsn-modal-overlay" id="mig-confirm">'
        +   '<div class="dsn-modal" role="dialog" aria-modal="true" aria-labelledby="mig-confirm-title">'
        +     '<div class="dsn-modal-head"><h3 id="mig-confirm-title">确认迁移</h3>'
        +       '<button type="button" class="btn-ghost dsn-modal-x" id="mig-confirm-x" aria-label="关闭">×</button></div>'
        +     '<div class="dsn-modal-body" id="mig-confirm-body"></div>'
        +     '<div class="dsn-modal-actions">'
        +       '<button type="button" class="btn-ghost" id="mig-confirm-cancel">取消</button>'
        +       '<button type="button" class="btn-primary" id="mig-confirm-go">确认开始迁移</button>'
        +     '</div>'
        +   '</div>'
        + '</div>';

    /* local per-render job state (reset every mount) */
    let completedJobId = null;
    let outputFileCount = 0;
    /* Endpoint identity from /api/v1/config/status: type plus the resolved
       machine/database/schema/user, so two endpoints sharing a dialect stay
       distinguishable. */
    let sourceInfo = null;
    let targetInfo = null;
    const confirmFocus = modalFocus(root.querySelector('#mig-confirm'));

    const modeDesc = root.querySelector('#mode-desc');
    const pvSource = root.querySelector('#pv-source');
    const psSource = root.querySelector('#ps-source');
    const pvTarget = root.querySelector('#pv-target');
    const psTarget = root.querySelector('#ps-target');

    /* reflect the current mode on tabs, description, and target label */
    function applyMode() {
        root.querySelectorAll('.tab').forEach(t => {
            const active = t.dataset.mode === (mode === 'sql-out' ? 'sql' : 'direct');
            t.classList.toggle('active', active);
            t.setAttribute('aria-pressed', active ? 'true' : 'false');
        });
        modeDesc.textContent = (mode === 'sql-out')
            ? 'SQL 输出模式：导出后生成 INSERT SQL 文件，不连接目标库。'
            : '直接模式：导出 CSV 后直接导入目标数据库。';
        if (mode === 'sql-out') {
            pvTarget.textContent = 'INSERT SQL';
            pvTarget.title = '迁移产物是 INSERT SQL 文件，不连接目标库';
            psTarget.textContent = '生成 SQL 文件，不连接目标库';
            psTarget.title = pvTarget.title;
        } else {
            fillEndpoint(pvTarget, psTarget, targetInfo, '批量写入 / INSERT SQL', '待配置');
        }
    }

    /* endpointTitle is the hover text: the full identity, including parts the
       compact line drops. */
    function endpointTitle(info) {
        if (!info) return '';
        const parts = [];
        if (info.type) parts.push('类型 ' + info.type);
        if (info.ref) parts.push('数据源档案 ' + info.ref);
        if (info.user) parts.push('用户 ' + info.user);
        if (info.host) parts.push('主机 ' + info.host + (info.port ? ':' + info.port : ''));
        if (info.database) parts.push('库 ' + info.database);
        if (info.schema) parts.push('Schema ' + info.schema);
        return parts.join(' · ');
    }

    /* fillEndpoint renders an endpoint: the connection identity on the value
       line, dialect/schema plus the caller's hint underneath. */
    function fillEndpoint(valEl, subEl, info, tail, fallbackSub) {
        const label = (info && info.label) || '';
        valEl.textContent = label || (info && info.type) || '—';
        valEl.title = endpointTitle(info);
        const parts = [];
        if (label && info.type) parts.push(info.type);
        /* MySQL-family schemas repeat the database name; do not echo them. */
        if (info && info.schema && info.schema !== info.database) parts.push('schema ' + info.schema);
        if (tail) parts.push(tail);
        subEl.textContent = parts.length ? parts.join(' · ') : (fallbackSub || '');
        subEl.title = endpointTitle(info);
    }

    root.querySelectorAll('.tab').forEach(t => t.addEventListener('click', () => {
        mode = (t.dataset.mode === 'sql') ? 'sql-out' : 'direct';
        applyMode();
    }));

    /* refreshEndpoints re-reads the active config and redraws the pipeline
       nodes, so a reload after a config change also updates which endpoint the
       run will actually use. */
    async function refreshEndpoints() {
        try {
            const st = await window.api.get('/api/v1/config/status');
            sourceInfo = st.source || null;
            targetInfo = st.target || null;
            fillEndpoint(pvSource, psSource, sourceInfo,
                st.metadata_loaded ? st.table_count + ' 张表待迁移' : '', '读取元数据与数据');
            applyMode();
        } catch (e) { /* best-effort */ }
    }

    /* prefill pipeline endpoints from the active config */
    refreshEndpoints();

    /* ── table picker: choose which tables this run covers ───── */
    let tblRows = [];
    let tblApplyTimer = null;
    /* key(lowercase) -> {badge, schema, name} for the exact row-count refresh.
       The badge shows the planner estimate (≈N 行) until COUNT(*) replaces it. */
    let tblCountEls = new Map();

    function selectedKeys() {
        return Array.from(root.querySelectorAll('#tbl-list input[type="checkbox"]:checked'))
            .map(i => i.dataset.key);
    }

    function updateTblCount() {
        const el = root.querySelector('#tbl-count');
        if (!el) return;
        const total = tblRows.length;
        const sel = selectedKeys().length;
        el.textContent = !total ? '—' : (sel >= total ? '全部 ' + total + ' 张' : sel + ' / ' + total);
    }

    function scheduleTblApply() {
        if (tblApplyTimer) clearTimeout(tblApplyTimer);
        tblApplyTimer = setTimeout(() => { tblApplyTimer = null; if (root.isConnected) applyTableSelection(); }, 700);
    }

    async function applyTableSelection() {
        const statusEl = root.querySelector('#tbl-status');
        if (!statusEl || !tblRows.length) return;
        const sel = selectedKeys();
        /* Nothing selected must not silently mean "everything". */
        if (!sel.length) {
            statusEl.textContent = '未选择任何表（配置保持不变）';
            return;
        }
        const tablesValue = sel.length >= tblRows.length ? '*' : sel.join(',');
        try {
            const cur = await window.api.get('/api/v1/config/current');
            const values = cur.values || {};
            values.tables = tablesValue;
            await window.api.post('/api/v1/scenarios/' + cur.scenario + '/build', { values: values, save: true });
            statusEl.textContent = tablesValue === '*'
                ? '全部 ' + tblRows.length + ' 张表，已同步到配置'
                : '已选 ' + sel.length + ' / ' + tblRows.length + ' 张表，已同步到配置';
            if (window.refreshConfigBar) window.refreshConfigBar();
        } catch (e) {
            statusEl.textContent = '✗ 同步配置失败：' + ((e && e.message) || e);
        }
    }

    function renderTablePicker() {
        const statusEl = root.querySelector('#tbl-status');
        const listEl = root.querySelector('#tbl-list');
        if (!tblRows.length) {
            statusEl.textContent = '源库中没有表';
            updateTblCount();
            return;
        }
        listEl.innerHTML = '';
        tblCountEls = new Map();
        (async () => {
            let tablesValue = '*';
            try {
                const cur = await window.api.get('/api/v1/config/current');
                tablesValue = (cur.values && cur.values.tables) || '*';
            } catch (e) { /* default to all */ }
            const all = tablesValue.trim() === '' || tablesValue.trim() === '*';
            const wanted = new Set(tablesValue.split(',').map(s => s.trim().toLowerCase()).filter(Boolean));
            const cap = 500;
            tblRows.slice(0, cap).forEach(r => {
                const key = (r.schema ? r.schema + '.' : '') + r.name;
                const label = document.createElement('label');
                label.className = 'check tbl-item';
                label.dataset.key = key.toLowerCase();
                const cb = document.createElement('input');
                cb.type = 'checkbox';
                cb.dataset.key = key;
                cb.checked = all || wanted.has(key.toLowerCase()) || wanted.has((r.name || '').toLowerCase());
                cb.addEventListener('change', () => { updateTblCount(); scheduleTblApply(); });
                label.appendChild(cb);
                const name = document.createElement('span');
                name.className = 'tbl-name';
                name.textContent = key;
                name.title = '点击统计实际行数（COUNT(*)）';
                label.appendChild(name);
                const badge = document.createElement('button');
                badge.type = 'button';
                badge.className = 'tbl-rowcount';
                badge.title = '点击统计实际行数（COUNT(*)）';
                label.appendChild(badge);
                tblCountEls.set(key.toLowerCase(), { badge, schema: r.schema || '', name: r.name });
                /* Clicking the table (name or count) counts it; the checkbox
                   keeps toggling selection, so the default label activation is
                   suppressed here. */
                const countThis = ev => { ev.preventDefault(); ev.stopPropagation(); countRows([{ schema: r.schema || '', name: r.name }]); };
                name.addEventListener('click', countThis);
                badge.addEventListener('click', countThis);
                setRowCountBadge(r, badge);
                listEl.appendChild(label);
            });
            if (tblRows.length > cap) {
                const note = document.createElement('div');
                note.className = 'field-help';
                note.textContent = '仅显示前 ' + cap + ' 张，请用过滤条件缩小范围';
                listEl.appendChild(note);
            }
            listEl.style.display = 'block';
            statusEl.textContent = '';
            updateTblCount();
        })();
    }

    /* setRowCountBadge renders one table's row count: the metadata estimate is
       marked with ≈, an exact COUNT(*) plainly (and remembered on the row so a
       re-render keeps it). */
    function setRowCountBadge(r, badge) {
        if (!badge) return;
        const exact = r.rows_exact;
        const n = exact ? r.row_count : (r.row_count || 0);
        badge.classList.toggle('is-exact', !!exact);
        badge.classList.remove('is-busy', 'is-error');
        badge.textContent = (exact ? '' : '≈') + Number(n || 0).toLocaleString() + ' 行';
        badge.title = exact
            ? '精确统计（COUNT(*)）'
            : '规划器估算值（ANALYZE/VACUUM 后刷新）· 点击统计实际行数';
    }

    function setBadgeState(key, text, cls, title) {
        const entry = tblCountEls.get(String(key).toLowerCase());
        if (!entry || !entry.badge.isConnected) return;
        entry.badge.classList.remove('is-busy', 'is-error', 'is-exact');
        if (cls) entry.badge.classList.add(cls);
        entry.badge.textContent = text;
        if (title) entry.badge.title = title;
    }

    /* countRows streams exact COUNT(*) results for the given tables, one at a
       time from the server, and refreshes each row as its line arrives. */
    async function countRows(tables) {
        const statusEl = root.querySelector('#tbl-status');
        const allBtn = root.querySelector('#tbl-count-all');
        if (!tables.length) return;
        if (rowCountAbort) {
            setTblStatusText(statusEl, '正在统计中，请稍候…');
            return;
        }

        const ctrl = new AbortController();
        rowCountAbort = ctrl;
        if (allBtn) allBtn.textContent = '停止';
        setTblStatusText(statusEl, '统计中… 0/' + tables.length);
        /* A single click gets feedback on its own row; a full run only shows
           progress in the status line, so the estimates stay readable. */
        if (tables.length === 1) {
            const key = ((tables[0].schema ? tables[0].schema + '.' : '') + tables[0].name).toLowerCase();
            setBadgeState(key, '统计中…', 'is-busy', '正在执行 COUNT(*)');
        }

        let done = 0, failed = 0, cancelled = false;
        try {
            const resp = await fetch('/api/v1/metadata/row-count', {
                method: 'POST',
                headers: window.api._headers({ 'Content-Type': 'application/json' }),
                body: JSON.stringify({ tables: tables }),
                signal: ctrl.signal
            });
            if (resp.status === 401) {
                window.dispatchEvent(new CustomEvent('owl-auth-required'));
                throw new Error('unauthorized');
            }
            if (!resp.ok) {
                const err = await resp.json().catch(() => ({}));
                throw new Error(err.error || (resp.status + ' ' + resp.statusText));
            }
            /* NDJSON: one record per table, flushed as each count lands. */
            const reader = resp.body.getReader();
            const decoder = new TextDecoder();
            let buf = '';
            for (;;) {
                const chunk = await reader.read();
                if (chunk.done) break;
                buf += decoder.decode(chunk.value, { stream: true });
                let nl;
                while ((nl = buf.indexOf('\n')) >= 0) {
                    const line = buf.slice(0, nl).trim();
                    buf = buf.slice(nl + 1);
                    if (!line) continue;
                    let rec;
                    try { rec = JSON.parse(line); } catch (e) { continue; }
                    if (rec.done) continue;
                    if (rec.error) { failed++; } else { done++; }
                    applyRowCountRecord(rec, tables);
                    setTblStatusText(statusEl, '统计中… ' + (done + failed) + '/' + tables.length);
                }
            }
        } catch (e) {
            if (e && e.name === 'AbortError') { cancelled = true; }
            else { statusEl.textContent = '✗ 统计失败：' + ((e && e.message) || e); }
        } finally {
            rowCountAbort = null;
            if (allBtn) allBtn.textContent = '全量统计';
        }
        if (cancelled) {
            setTblStatusText(statusEl, '统计已停止（已完成 ' + (done + failed) + ' / ' + tables.length + '）');
            return;
        }
        setTblStatusText(statusEl, failed
            ? '统计完成：成功 ' + done + ' 张，失败 ' + failed + ' 张'
            : '统计完成：' + done + ' 张表');
    }

    /* applyRowCountLine folds one NDJSON record into its row. */
    function applyRowCountRecord(rec, tables) {
        const key = ((rec.schema ? rec.schema + '.' : '') + rec.name).toLowerCase();
        if (rec.error) {
            setBadgeState(key, '统计失败', 'is-error', rec.error);
            return;
        }
        const entry = tblCountEls.get(key);
        if (entry) setRowCountBadge({ row_count: rec.rows, rows_exact: true }, entry.badge);
        for (const r of [tables, tblRows]) {
            const row = r.find(t => (t.schema || '') === (rec.schema || '') && t.name === rec.name);
            if (row) { row.row_count = rec.rows; row.rows_exact = true; }
        }
    }

    function setTblStatusText(el, text) {
        /* Keep the "已同步到配置" feedback from applyTableSelection readable:
           only replace the status when the picker owns it. */
        if (el && el.isConnected) el.textContent = text;
    }

    async function loadTables() {
        const statusEl = root.querySelector('#tbl-status');
        statusEl.textContent = '正在从源库抽取元数据…';
        try {
            /* Empty body: the server loads from the active config, which keeps
               the real DSN out of the browser. */
            await window.api.post('/api/v1/metadata/load', {});
            tblRows = await window.api.get('/api/v1/metadata/tables') || [];
            renderTablePicker();
            /* The config that produced this list is the current one: refresh
               the endpoint nodes and the topbar to match. */
            await refreshEndpoints();
            if (window.refreshConfigBar) window.refreshConfigBar();
        } catch (e) {
            statusEl.textContent = '✗ 加载失败：' + ((e && e.message) || e);
        }
    }

    (async function initTables() {
        const statusEl = root.querySelector('#tbl-status');
        /* The server reports whether the loaded tables still match the active
           config (schema/DSN/source changed → metadata_stale). Stale tables
           must never be shown as if they were the new source's. */
        let stale = false;
        try {
            const st = await window.api.get('/api/v1/config/status');
            stale = !!st.metadata_stale;
        } catch (e) { /* best-effort: fall through and show what is loaded */ }

        try {
            tblRows = await window.api.get('/api/v1/metadata/tables') || [];
        } catch (e) {
            statusEl.textContent = '元数据尚未加载。';
            const btn = document.createElement('button');
            btn.type = 'button';
            btn.className = 'btn-ghost btn-sm';
            btn.textContent = '从源库加载表列表';
            btn.addEventListener('click', loadTables);
            statusEl.appendChild(btn);
            updateTblCount();
            return;
        }
        if (stale) {
            statusEl.textContent = '配置已变更（源库 / Schema），正在按新配置重新抽取表列表…';
            await loadTables();
            return;
        }
        renderTablePicker();
    })();

    root.querySelector('#tbl-filter').addEventListener('input', () => {
        const q = (root.querySelector('#tbl-filter').value || '').trim().toLowerCase();
        root.querySelectorAll('#tbl-list .tbl-item').forEach(item => {
            item.style.display = (!q || item.dataset.key.indexOf(q) >= 0) ? '' : 'none';
        });
    });
    root.querySelector('#tbl-all').addEventListener('click', () => {
        root.querySelectorAll('#tbl-list .tbl-item').forEach(item => {
            if (item.style.display !== 'none') item.querySelector('input').checked = true;
        });
        updateTblCount();
        scheduleTblApply();
    });
    root.querySelector('#tbl-none').addEventListener('click', () => {
        root.querySelectorAll('#tbl-list input[type="checkbox"]').forEach(cb => { cb.checked = false; });
        updateTblCount();
        scheduleTblApply();
    });
    /* 重新加载: re-extract the table list from the active config, whatever
       changed behind it (schema, DSN, source). */
    root.querySelector('#tbl-reload').addEventListener('click', () => {
        if (rowCountAbort) rowCountAbort.abort();
        loadTables();
    });
    /* 全量统计: count the visible tables in top-to-bottom order; a second click
       stops the run (results already streamed stay). Matches 全选, which also
       only covers what the filter left visible. */
    root.querySelector('#tbl-count-all').addEventListener('click', () => {
        if (rowCountAbort) { rowCountAbort.abort(); return; }
        const list = [];
        root.querySelectorAll('#tbl-list .tbl-item').forEach(item => {
            if (item.style.display === 'none') return;
            const entry = tblCountEls.get(item.dataset.key);
            if (entry) list.push({ schema: entry.schema, name: entry.name });
        });
        countRows(list);
    });

    /* wire jobUI overrides for this view (after reset above) */
    jobUI.bind('#progress-log');

    /* Precise pipeline attribution from worker stage events. */
    jobUI.onEvent = function (m) {
        if (!m || m.event !== 'stage') return;
        switch (m.message) {
            case 'load_metadata':
            case 'connect_source':
                setStage(root, 'pn-source', 'active');
                break;
            case 'export':
                setStage(root, 'pn-source', 'done');
                setStage(root, 'pn-export', 'active');
                flow(root, 'pl-1', true);
                break;
            case 'connect_target':
            case 'create_tables':
            case 'import':
            case 'generate_sql':
                setStage(root, 'pn-source', 'done');
                setStage(root, 'pn-export', 'done');
                setStage(root, 'pn-target', 'active');
                flow(root, 'pl-1', false);
                flow(root, 'pl-2', true);
                break;
        }
    };

    const origFinish = jobUI.finish.bind(jobUI);
    jobUI.finish = function (status) {
        origFinish(status);
        flow(root, 'pl-1', false); flow(root, 'pl-2', false);
        const nodes = ['pn-source', 'pn-export', 'pn-target'].map(id => root.querySelector('#' + id));
        const mark = (n, cls) => {
            if (!n) return;
            n.classList.remove('active', 'done', 'failed', 'cancelled');
            n.classList.add(cls);
        };
        if (status === 'completed_with_errors') {
            /* Stages all ran, but some tables failed — keep them green-but-warn. */
            nodes.forEach(n => {
                if (!n) return;
                n.classList.remove('active', 'failed', 'cancelled');
                n.classList.add('done', 'warned');
            });
            return;
        }
        if (status === 'failed' || status === 'interrupted' || status === 'cancelled') {
            const cls = status === 'cancelled' ? 'cancelled' : 'failed';
            const active = root.querySelector('.pipe-node.active');
            const anyDone = nodes.some(n => n && n.classList.contains('done'));
            /* A job-level failure before any stage completed carries no stage
               info, so mark the whole pipeline rather than mis-blaming one node. */
            if (!anyDone) nodes.forEach(n => mark(n, cls));
            else if (active) mark(active, cls);
            else nodes.forEach(n => { if (n && !n.classList.contains('done')) mark(n, cls); });
            return;
        }
        nodes.forEach(n => mark(n, 'done'));
    };

    jobUI.onComplete = async function (jobId) {
        if (!root.isConnected || mode !== 'sql-out') return;
        completedJobId = jobId;
        try {
            const info = await window.api.get('/api/v1/jobs/' + jobId + '/output');
            if (!info.has_sql) return;
            outputFileCount = info.file_count;
            root.querySelector('#output-summary').textContent =
                '输出目录：' + info.dir + ' ｜ ' + info.file_count + ' 个文件，共 ' + window.humanSize(info.total_size);
            const fmt = root.querySelector('#dl-format');
            if (info.file_count === 1) fmt.value = 'raw';
            else if (info.total_size > 10 * 1024 * 1024 || info.file_count > 5) fmt.value = 'tar.gz';
            root.querySelector('#download-panel').style.display = 'block';
        } catch (e) { /* best-effort */ }
    };

    function summaryRow(label, value, title) {
        const row = document.createElement('div');
        row.className = 'confirm-row';
        const l = document.createElement('span');
        l.className = 'confirm-label';
        l.textContent = label;
        const v = document.createElement('span');
        v.className = 'confirm-value';
        v.textContent = value;
        if (title) v.title = title;
        row.appendChild(l);
        row.appendChild(v);
        return row;
    }

    function note(text) {
        const el = document.createElement('div');
        el.className = 'field-note';
        el.textContent = text;
        return el;
    }

    /* Show what will actually run — and warn about destructive settings —
       before a click can start writing to the target. Endpoints are named by
       their connection identity, not just the dialect: "确认" must not be the
       step where you find out the target was another machine. */
    function openConfirm(cfg, st) {
        const body = root.querySelector('#mig-confirm-body');
        body.innerHTML = '';
        const isSQL = mode === 'sql-out';
        const tables = (cfg.export && cfg.export.tables && cfg.export.tables.include) || [];
        const src = (st && st.source) || null;
        const tgt = (st && st.target) || null;
        const describe = (info, type, schema, schemaFallback) => {
            const label = (info && (info.label || info.type)) || type || '—';
            const sch = (info && info.schema) || schema;
            return { label, schema: sch || schemaFallback };
        };
        body.appendChild(summaryRow('模式', isSQL ? 'SQL 输出（不连接目标库）' : '直接迁移'));
        const s = describe(src, cfg.source && cfg.source.type, cfg.source && cfg.source.schema, '（未指定）');
        body.appendChild(summaryRow('源数据库', s.label, endpointTitle(src)));
        body.appendChild(summaryRow('源 Schema', s.schema));
        if (!isSQL) {
            const t = describe(tgt, cfg.target && cfg.target.type, cfg.target && cfg.target.schema, '（与源相同）');
            body.appendChild(summaryRow('目标数据库', t.label, endpointTitle(tgt)));
            body.appendChild(summaryRow('目标 Schema', t.schema));
        }
        body.appendChild(summaryRow('迁移的表', tables.length ? tables.join(', ') : '*'));
        if (!isSQL && !(cfg.target && cfg.target.type)) {
            body.appendChild(note('⚠ 未配置目标数据库，直接迁移无法执行。'));
        }
        const truncate = !!(cfg.import && cfg.import.target && cfg.import.target.truncate_before);
        if (truncate && !isSQL) {
            body.appendChild(note('⚠ 目标表将在导入前执行 TRUNCATE，现有数据会被清空，且无法从界面撤销。'));
        }
        root.querySelector('#mig-confirm').classList.add('open');
        document.body.classList.add('modal-open');
        confirmFocus.open();
        root.querySelector('#mig-confirm-go').focus();
    }

    function closeConfirm() {
        const overlay = root.querySelector('#mig-confirm');
        if (overlay) overlay.classList.remove('open');
        document.body.classList.remove('modal-open');
        confirmFocus.close();
    }

    async function startMigrate() {
        let cfg, st = null;
        try { cfg = await window.api.get('/api/v1/config'); }
        catch (e) { window.toast.err('读取配置失败', e && e.message || ''); return; }
        if (!cfg || !cfg.metadata || !cfg.metadata.type) {
            window.toast.warn('尚未配置', '请先在「配置」页选择场景并保存配置');
            return;
        }
        /* Endpoint identities come from the status endpoint (the config payload
           only carries masked DSNs), so the confirmation names the machines. */
        try { st = await window.api.get('/api/v1/config/status'); } catch (e) { /* optional */ }
        openConfirm(cfg, st);
    }

    let starting = false;
    async function doStart() {
        if (starting) return;
        starting = true;
        const btnStart = root.querySelector('#btn-start');
        if (btnStart) btnStart.disabled = true;
        closeConfirm();
        setStage(root, 'pn-source', 'active'); flow(root, 'pl-1', false); flow(root, 'pl-2', false);
        setStage(root, 'pn-export', ''); setStage(root, 'pn-target', '');
        try {
            await jobUI.start('/api/v1/migrate', {
                mode: mode,
                skip_ddl: root.querySelector('#opt-skip-ddl').checked,
                continue_on_error: root.querySelector('#opt-continue-on-error').checked,
            });
        } catch (e) {
            window.toast.err('启动失败', e && e.message || '');
        } finally {
            starting = false;
            if (btnStart && btnStart.style.display !== 'none') btnStart.disabled = false;
        }
    }

    function downloadSQL() {
        const fmt = root.querySelector('#dl-format').value;
        if (fmt === 'raw' && outputFileCount !== 1) {
            window.toast.warn('格式不适用', '原始文件下载仅适用于单个文件，多文件请选 tar.gz 或 zip');
            return;
        }
        window.location = window.api.downloadURL('/api/v1/jobs/' + completedJobId + '/output/download?format=' + encodeURIComponent(fmt));
    }

    /* Dry-run: cheap read-only checks (config, metadata, table list, target). */
    async function runPreflight() {
        const panel = root.querySelector('#preflight-panel');
        const list = root.querySelector('#preflight-list');
        const badge = root.querySelector('#pf-badge');
        const btn = root.querySelector('#btn-preflight');
        btn.disabled = true;
        panel.style.display = 'block';
        list.textContent = '正在检查…';
        try {
            const res = await window.api.post('/api/v1/migrate/preflight?mode=' + encodeURIComponent(mode), {});
            list.innerHTML = '';
            (res.checks || []).forEach(c => {
                const row = document.createElement('div');
                row.className = 'pf-row';
                const mark = document.createElement('span');
                mark.className = c.ok ? 'st-ok' : 'st-fail';
                mark.textContent = c.ok ? '✓' : '✗';
                const name = document.createElement('b');
                name.textContent = c.name;
                const detail = document.createElement('span');
                detail.className = 'pf-detail';
                detail.textContent = c.detail || '';
                row.appendChild(mark);
                row.appendChild(name);
                row.appendChild(detail);
                list.appendChild(row);
            });
            (res.warnings || []).forEach(w => {
                const note = document.createElement('div');
                note.className = 'field-note';
                note.textContent = '⚠ ' + w;
                list.appendChild(note);
            });
            badge.textContent = res.ok ? '通过' : '未通过';
        } catch (e) {
            badge.textContent = '失败';
            list.innerHTML = '';
            const err = document.createElement('div');
            err.className = 'field-error';
            err.textContent = '✗ 预检请求失败：' + ((e && e.message) || e);
            list.appendChild(err);
        } finally {
            btn.disabled = false;
        }
    }

    root.querySelector('#btn-start').addEventListener('click', startMigrate);
    root.querySelector('#btn-preflight').addEventListener('click', runPreflight);
    root.querySelector('#btn-cancel').addEventListener('click', () => jobUI.cancel());
    root.querySelector('#btn-download').addEventListener('click', downloadSQL);

    const confirmOverlay = root.querySelector('#mig-confirm');
    root.querySelector('#mig-confirm-go').addEventListener('click', doStart);
    root.querySelector('#mig-confirm-cancel').addEventListener('click', closeConfirm);
    root.querySelector('#mig-confirm-x').addEventListener('click', closeConfirm);
    confirmOverlay.addEventListener('click', e => { if (e.target === confirmOverlay) closeConfirm(); });
    if (confirmEscHandler) document.removeEventListener('keydown', confirmEscHandler);
    confirmEscHandler = e => { if (e.key === 'Escape' && confirmOverlay.classList.contains('open')) closeConfirm(); };
    document.addEventListener('keydown', confirmEscHandler);

    applyMode();
}
