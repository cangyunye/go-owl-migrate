/* owl-migrate SPA · migrate view (ported from web/templates/migrate.html) */
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

/* Module-level mode keeps the toggle across re-renders (user pref).
   'direct' | 'sql-out'. Defaults to 'direct' per SSR initial state. */
let mode = 'direct';

function setStage(root, id, cls) {
    const el = root.querySelector('#' + id);
    if (!el) return;
    el.classList.remove('active', 'done');
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

    root.innerHTML = ''
        + '<div class="page-head reveal" style="--i:0">'
        +   '<div>'
        +     '<div class="overline">execute · migrate</div>'
        +     '<h1>数据迁移</h1>'
        +     '<p class="subtitle">端到端迁移：源库 → 导出 → 目标库（或生成 INSERT SQL）</p>'
        +   '</div>'
        + '</div>'

        + '<nav class="tabs reveal" style="--i:1">'
        +   '<button type="button" class="tab" data-mode="direct">直接迁移</button>'
        +   '<button type="button" class="tab" data-mode="sql">SQL 输出</button>'
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

        +   '<div class="field">'
        +     '<label class="check"><input type="checkbox" id="opt-skip-ddl"> 跳过建表 <code>--skip-ddl</code>（仅导数据）</label>'
        +   '</div>'
        +   '<div class="field">'
        +     '<label class="check"><input type="checkbox" id="opt-continue-on-error"> 部分表失败不中断 <code>--continue-on-error</code></label>'
        +   '</div>'
        +   '<div class="form-actions">'
        +     '<button class="btn-primary" id="btn-start" type="button">'
        +       '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M8 5v14l11-7z"/></svg>'
        +       '开始迁移'
        +     '</button>'
        +     '<button class="btn-danger" id="btn-cancel" type="button" style="display:none">取消</button>'
        +   '</div>'
        + '</div>'

        + '<div id="progress-panel" class="panel reveal" style="--i:3;display:none">'
        +   '<div class="panel-head">'
        +     '<span class="panel-title">实时进度 <span class="badge badge-accent" id="job-id-badge"></span></span>'
        +     '<span class="live-dot"></span>'
        +   '</div>'
        +   '<div id="progress-log" class="term"></div>'
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
        + '</div>';

    /* local per-render job state (reset every mount) */
    let completedJobId = null;
    let outputFileCount = 0;
    let prefilledTarget = null;

    const modeDesc = root.querySelector('#mode-desc');
    const pvSource = root.querySelector('#pv-source');
    const psSource = root.querySelector('#ps-source');
    const pvTarget = root.querySelector('#pv-target');
    const psTarget = root.querySelector('#ps-target');

    /* reflect the current mode on tabs, description, and target label */
    function applyMode() {
        root.querySelectorAll('.tab').forEach(t =>
            t.classList.toggle('active', t.dataset.mode === (mode === 'sql-out' ? 'sql' : 'direct')));
        modeDesc.textContent = (mode === 'sql-out')
            ? 'SQL 输出模式：导出后生成 INSERT SQL 文件，不连接目标库。'
            : '直接模式：导出 CSV 后直接导入目标数据库。';
        if (mode === 'sql-out') {
            pvTarget.textContent = 'INSERT SQL';
            psTarget.textContent = '生成 SQL 文件，不连接目标库';
        } else {
            pvTarget.textContent = prefilledTarget || '—';
            psTarget.textContent = '批量写入 / INSERT SQL';
        }
    }

    root.querySelectorAll('.tab').forEach(t => t.addEventListener('click', () => {
        mode = (t.dataset.mode === 'sql') ? 'sql-out' : 'direct';
        applyMode();
    }));

    /* prefill pipeline endpoints from the active config */
    (async function prefillPipeline() {
        try {
            const st = await window.api.get('/api/v1/config/status');
            if (st.source_type) pvSource.textContent = st.source_type;
            if (st.target_dialect && mode !== 'sql-out') {
                prefilledTarget = st.target_dialect;
                pvTarget.textContent = st.target_dialect;
            }
            if (st.metadata_loaded) psSource.textContent = st.table_count + ' 张表待迁移';
        } catch (e) { /* best-effort */ }
    })();

    /* wire jobUI overrides for this view (after reset above) */
    jobUI.bind('#progress-log');

    const origLogLine = jobUI.logLine.bind(jobUI);
    jobUI.logLine = function (kind, msg, detail) {
        origLogLine(kind, msg, detail);
        const m = (msg || '').toLowerCase();
        if (m.includes('export') || m.includes('导出')) {
            setStage(root, 'pn-source', 'done'); setStage(root, 'pn-export', 'active'); flow(root, 'pl-1', true);
        }
        if (m.includes('import') || m.includes('导入') || m.includes('insert')) {
            setStage(root, 'pn-export', 'done'); setStage(root, 'pn-target', 'active'); flow(root, 'pl-2', true);
        }
    };

    const origFinish = jobUI.finish.bind(jobUI);
    jobUI.finish = function () {
        origFinish();
        flow(root, 'pl-1', false); flow(root, 'pl-2', false);
        setStage(root, 'pn-source', 'done'); setStage(root, 'pn-export', 'done'); setStage(root, 'pn-target', 'done');
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

    async function startMigrate() {
        setStage(root, 'pn-source', 'active'); flow(root, 'pl-1', false); flow(root, 'pl-2', false);
        setStage(root, 'pn-export', ''); setStage(root, 'pn-target', '');
        try {
            await jobUI.start('/api/v1/migrate', {
                mode: mode,
                skip_ddl: root.querySelector('#opt-skip-ddl').checked,
                continue_on_error: root.querySelector('#opt-continue-on-error').checked,
            });
        } catch (e) { window.toast.err('启动失败', e && e.message || ''); }
    }

    function downloadSQL() {
        const fmt = root.querySelector('#dl-format').value;
        if (fmt === 'raw' && outputFileCount !== 1) {
            window.toast.warn('格式不适用', '原始文件下载仅适用于单个文件，多文件请选 tar.gz 或 zip');
            return;
        }
        window.location = window.api.downloadURL('/api/v1/jobs/' + completedJobId + '/output/download?format=' + encodeURIComponent(fmt));
    }

    root.querySelector('#btn-start').addEventListener('click', startMigrate);
    root.querySelector('#btn-cancel').addEventListener('click', () => jobUI.cancel());
    root.querySelector('#btn-download').addEventListener('click', downloadSQL);

    applyMode();
}
