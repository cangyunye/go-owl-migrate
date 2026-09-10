/* owl-migrate SPA · jobs list view (ported from web/templates/jobs.html) */
import { statusBadge, escapeHtml } from '../util.js';

let allJobs = [];
let currentFilter = 'all';
let timer = null;

function matches(j) {
    if (currentFilter === 'all') return true;
    if (currentFilter === 'running') return j.status === 'running' || j.status === 'cancelling';
    if (currentFilter === 'completed') return j.status === 'completed' || j.status === 'completed_with_errors';
    if (currentFilter === 'failed') return j.status === 'failed' || j.status === 'interrupted' || j.status === 'cancelled';
    return true;
}

function renderTable() {
    const tbody = document.getElementById('jobs-body');
    if (!tbody) return;
    const rows = allJobs.filter(matches);
    if (!rows.length) {
        tbody.innerHTML = '<tr><td colspan="6" style="text-align:center;color:var(--text-3);padding:20px">无匹配任务</td></tr>';
        return;
    }
    tbody.innerHTML = rows.map(j =>
        '<tr><td class="mono"><a href="#/jobs/' + encodeURIComponent(j.job_id) + '">' + escapeHtml(j.job_id) + '</a></td>' +
        '<td class="mono">' + escapeHtml(j.type) + '</td>' +
        '<td>' + statusBadge(j.status) + '</td>' +
        '<td class="mono" style="color:var(--text-2)">' + escapeHtml(j.pid || '—') + '</td>' +
        '<td class="mono" style="color:var(--text-2)">' + escapeHtml(j.created_at || '') + '</td>' +
        '<td class="mono" style="color:var(--text-2)">' + escapeHtml(j.finished_at || '—') + '</td></tr>'
    ).join('');
}

function setFilter(f) {
    currentFilter = f;
    document.querySelectorAll('#filter-tabs .tab').forEach(t => {
        const active = t.dataset.filter === f;
        t.classList.toggle('active', active);
        if (active) t.setAttribute('aria-current', 'true');
        else t.removeAttribute('aria-current');
    });
    renderTable();
}

async function loadJobs() {
    try {
        allJobs = await window.api.get('/api/v1/jobs') || [];
        renderTable();
        const updated = document.getElementById('jobs-updated');
        if (updated) updated.textContent = '更新于 ' + new Date().toTimeString().slice(0, 8);
    } catch (e) { /* best-effort */ }
}

export function render(root /*Element*/, params) {
    if (timer) { clearInterval(timer); timer = null; }
    currentFilter = 'all';
    allJobs = [];
    root.innerHTML = ''
        + '<div class="page-head">'
        +   '<div>'
        +     '<div class="overline">monitor · jobs</div>'
        +     '<h1>任务历史</h1>'
        +     '<p class="subtitle">迁移 / 导出 / 导入任务的执行记录 — 支持检查点恢复</p>'
        +   '</div>'
        +   '<div class="panel-actions">'
        +     '<span class="live-dot" title="每 5 秒自动刷新"></span>'
        +     '<span class="status-msg" id="jobs-updated"></span>'
        +     '<button class="btn-ghost btn-sm" id="refresh-jobs" type="button">刷新</button>'
        +   '</div>'
        + '</div>'
        + '<nav class="tabs" id="filter-tabs">'
        +   '<a href="#/jobs" class="tab active" data-filter="all" aria-current="true">全部</a>'
        +   '<a href="#/jobs" class="tab" data-filter="running">运行中</a>'
        +   '<a href="#/jobs" class="tab" data-filter="completed">已完成</a>'
        +   '<a href="#/jobs" class="tab" data-filter="failed">失败 / 中断</a>'
        + '</nav>'
        + '<div>'
        +   '<table class="data-table">'
        +     '<thead><tr><th scope="col">ID</th><th scope="col">类型</th><th scope="col">状态</th><th scope="col">PID</th><th scope="col">创建时间</th><th scope="col">完成时间</th></tr></thead>'
        +     '<tbody id="jobs-body"><tr><td colspan="6" style="text-align:center;color:var(--text-3);padding:20px">加载中…</td></tr></tbody>'
        +   '</table>'
        + '</div>';

    root.querySelectorAll('#filter-tabs .tab').forEach(tab => {
        tab.addEventListener('click', (e) => {
            e.preventDefault();
            setFilter(tab.dataset.filter);
        });
    });
    const refresh = root.querySelector('#refresh-jobs');
    if (refresh) refresh.addEventListener('click', loadJobs);

    loadJobs();
    timer = setInterval(loadJobs, 5000);
}
