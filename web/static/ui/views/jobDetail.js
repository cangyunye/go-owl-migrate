/* owl-migrate SPA · job detail view (ported from web/templates/job_detail.html) */
import { statusBadge, escapeHtml } from '../util.js';

let ws = null;
let pollTimer = null;
let lastSeq = 0;
let jobType = 'migrate';

const isActive = s => s === 'running' || s === 'cancelling';

function cpStatus(s) {
    if (s === 'SUCCESS') return '<span class="st-ok">✓ SUCCESS</span>';
    if (s === 'FAIL') return '<span class="st-fail">✗ FAIL</span>';
    if (s) return '<span class="st-run">' + escapeHtml(s) + '</span>';
    return '<span style="color:var(--text-3)">—</span>';
}

function renderInfo(job) {
    const el = document.getElementById('job-info');
    if (!el) return;
    el.innerHTML =
        '<span>类型 <b>' + escapeHtml(job.type) + '</b></span>' +
        '<span>状态 ' + statusBadge(job.status) + '</span>' +
        '<span>PID <b>' + escapeHtml(job.pid || '—') + '</b></span>' +
        '<span>创建 <b>' + escapeHtml(job.created_at || '') + '</b></span>' +
        '<span>完成 <b>' + escapeHtml(job.finished_at || '—') + '</b></span>';
}

function renderButtons(status) {
    const cancel = document.getElementById('btn-cancel');
    const resume = document.getElementById('btn-resume');
    if (cancel) cancel.style.display = (status === 'running') ? 'inline-flex' : 'none';
    if (resume) resume.style.display =
        (['interrupted', 'cancelled', 'failed'].includes(status)) ? 'inline-flex' : 'none';
}

async function loadCheckpoints() {
    const cps = await window.api.get('/api/v1/jobs/' + currentJobId + '/checkpoints') || [];
    const count = document.getElementById('cp-count');
    const tbody = document.getElementById('cp-body');
    if (count) count.textContent = cps.length + ' 张表';
    if (!tbody) return;
    if (!cps.length) {
        tbody.innerHTML = '<tr><td colspan="7" style="text-align:center;color:var(--text-3);padding:16px">暂无检查点</td></tr>';
        return;
    }
    tbody.innerHTML = cps.map(c =>
        '<tr><td class="mono">' + escapeHtml(c.schema) + '</td>' +
        '<td class="mono" style="font-weight:600">' + escapeHtml(c.table_name) + '</td>' +
        '<td>' + (c.exported ? '<span class="st-ok">✓</span>' : '<span style="color:var(--text-3)">—</span>') + '</td>' +
        '<td class="mono">' + escapeHtml(c.exported_rows) + '</td>' +
        '<td>' + (c.imported ? '<span class="st-ok">✓</span>' : '<span style="color:var(--text-3)">—</span>') + '</td>' +
        '<td class="mono">' + escapeHtml(c.imported_rows) + '</td>' +
        '<td>' + cpStatus(c.status) + '</td></tr>'
    ).join('');
}

function appendEvent(m) {
    const log = document.getElementById('progress-log');
    if (!log) return;
    const line = document.createElement('span');
    line.className = 'term-line';
    const tbl = ((m.schema || '') + (m.table ? '.' + m.table : '')).trim();
    line.innerHTML = '<span class="ln-seq">#' + m.seq + '</span>' +
        '<span class="ln-info">' + escapeHtml(m.event || '') + '</span>' +
        (tbl ? '  <span style="color:var(--text)">' + escapeHtml(tbl) + '</span>' : '') +
        (m.rows !== undefined && m.rows !== null ? '  <span class="ln-dim">→ ' + escapeHtml(m.rows) + ' rows</span>' : '') +
        (m.message ? '  <span class="ln-dim">' + escapeHtml(m.message) + '</span>' : '');
    log.appendChild(line);
    log.scrollTop = log.scrollHeight;
}

async function loadEvents() {
    const events = await window.api.get('/api/v1/jobs/' + currentJobId + '/events') || [];
    const log = document.getElementById('progress-log');
    if (!log) return;
    log.innerHTML = '';
    lastSeq = 0;
    if (!events.length) {
        log.innerHTML = '<span class="ln-dim">（暂无事件）</span>';
        return;
    }
    events.forEach(e => appendEvent({
        seq: e.seq, event: e.event_type, schema: e.schema,
        table: e.table_name, rows: e.rows, message: e.message
    }));
}

function connectWS() {
    ws = new WebSocket(window.api.wsURL('/api/v1/jobs/' + currentJobId + '/ws'));
    ws.onmessage = (e) => {
        let m;
        try { m = JSON.parse(e.data); } catch (err) { return; }
        if (m.type === 'progress') {
            if (m.seq > lastSeq) { appendEvent(m); lastSeq = m.seq; }
        } else if (m.type === 'complete' || m.type === 'cancelled' || m.type === 'error') {
            refreshStatus();
        }
    };
}

async function refreshStatus() {
    let job;
    try { job = await window.api.get('/api/v1/jobs/' + currentJobId); } catch (e) { return; }
    renderInfo(job);
    renderButtons(job.status);
    if (!isActive(job.status)) {
        stopLive();
        await loadCheckpoints();
        await loadEvents();
    }
}

function stopLive() {
    const dot = document.getElementById('evt-live');
    if (dot) dot.style.display = 'none';
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
    if (ws) { ws.onclose = null; ws.close(); ws = null; }
}

function startLive() {
    const log = document.getElementById('progress-log');
    if (log) log.innerHTML = '';
    const dot = document.getElementById('evt-live');
    if (dot) dot.style.display = 'inline-block';
    lastSeq = 0;
    connectWS();
    pollTimer = setInterval(refreshStatus, 2000);
}

let currentJobId = '';

async function load() {
    const job = await window.api.get('/api/v1/jobs/' + currentJobId);
    jobType = job.type;
    renderInfo(job);
    renderButtons(job.status);
    await loadCheckpoints();
    if (isActive(job.status)) startLive();
    else await loadEvents();
}

async function cancelJob() {
    const status = document.getElementById('action-status');
    try {
        await window.api.del('/api/v1/jobs/' + currentJobId);
        if (status) status.textContent = '已发送取消请求';
        window.toast.warn('已发送取消请求', currentJobId);
        refreshStatus();
    } catch (e) {
        if (status) status.textContent = '✗ ' + (e && e.message || e);
        window.toast.err('取消失败', e && e.message || '');
    }
}

async function resumeJob() {
    const status = document.getElementById('action-status');
    try {
        const resp = await window.api.post('/api/v1/' + jobType + '?resume_from=' + currentJobId, {});
        window.toast.ok('任务已恢复', resp && resp.job_id || '');
        location.hash = '#/jobs/' + encodeURIComponent(resp.job_id);
    } catch (e) {
        if (status) status.textContent = '✗ ' + (e && e.message || e);
        window.toast.err('恢复失败', e && e.message || '');
    }
}

export function render(root /*Element*/, params) {
    /* clear any live resources from a previous render (no leak across navigation) */
    stopLive();
    lastSeq = 0;

    currentJobId = params && params[0] ? params[0] : '';
    jobType = 'migrate';

    root.innerHTML = ''
        + '<div class="page-head reveal" style="--i:0">'
        +   '<div>'
        +     '<div class="overline">monitor · job detail</div>'
        +     '<h1>任务详情</h1>'
        +     '<p class="subtitle mono" id="job-id-line" style="color:var(--text-3)">' + escapeHtml(currentJobId) + '</p>'
        +   '</div>'
        +   '<div class="panel-actions">'
        +     '<button class="btn-danger btn-sm" id="btn-cancel" type="button" style="display:none">取消任务</button>'
        +     '<button class="btn-primary btn-sm" id="btn-resume" type="button" style="display:none">'
        +       '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/></svg>'
        +       '恢复任务'
        +     '</button>'
        +     '<span id="action-status" class="status-msg"></span>'
        +   '</div>'
        + '</div>'
        + '<div class="panel reveal" style="--i:1">'
        +   '<div class="panel-head"><span class="panel-title">任务信息</span></div>'
        +   '<div class="job-info" id="job-info"></div>'
        + '</div>'
        + '<div class="panel reveal" style="--i:2">'
        +   '<div class="panel-head">'
        +     '<span class="panel-title">检查点（每表状态）<span class="badge badge-accent" id="cp-count"></span></span>'
        +   '</div>'
        +   '<table class="data-table">'
        +     '<thead><tr><th>Schema</th><th>表</th><th>已导出</th><th>导出行数</th><th>已导入</th><th>导入行数</th><th>状态</th></tr></thead>'
        +     '<tbody id="cp-body"><tr><td colspan="7" style="text-align:center;color:var(--text-3);padding:16px">加载中…</td></tr></tbody>'
        +   '</table>'
        + '</div>'
        + '<div class="panel reveal" style="--i:3">'
        +   '<div class="panel-head">'
        +     '<span class="panel-title">进度事件</span>'
        +     '<span class="live-dot" id="evt-live" style="display:none"></span>'
        +   '</div>'
        +   '<div class="term" id="progress-log"></div>'
        + '</div>';

    if (!currentJobId) {
        const info = document.getElementById('job-info');
        if (info) info.innerHTML = '<span style="color:var(--text-3)">缺少任务 ID</span>';
        return;
    }

    const cancel = document.getElementById('btn-cancel');
    const resume = document.getElementById('btn-resume');
    if (cancel) cancel.addEventListener('click', cancelJob);
    if (resume) resume.addEventListener('click', resumeJob);

    load().catch(e => {
        const info = document.getElementById('job-info');
        if (info) info.innerHTML = '<span class="st-fail">✗ 加载失败：' + escapeHtml(e && e.message || String(e)) + '</span>';
        window.toast.err('任务加载失败', e && e.message || '');
    });
}
