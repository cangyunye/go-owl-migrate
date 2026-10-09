/* owl-migrate SPA · 产物库视图
   ============================================================
   任务产物独立成库：每一行是一个任务的产物包（关联任务号），
   可单任务打包下载（tar.gz / zip）。没有任务归属的历史文件放在
   「未归属任务」区，按文件单独下载。 */

import { statusBadge, escapeHtml } from '../util.js';

let timer = null;

function fmtTime(s) {
    if (!s) return '—';
    return s.replace('T', ' ').slice(0, 19);
}

function human(size) {
    return window.humanSize ? window.humanSize(size) : (size + ' B');
}

async function loadArtifacts() {
    const bodyEl = document.getElementById('artifacts-body');
    if (!bodyEl) return;
    let data;
    try {
        data = await window.api.get('/api/v1/artifacts') || {};
    } catch (e) {
        bodyEl.innerHTML = '<tr><td colspan="7" style="text-align:center;color:var(--red);padding:20px">'
            + '加载失败：' + escapeHtml(e && e.message || String(e)) + '</td></tr>';
        return;
    }
    const jobs = data.jobs || [];
    const standalone = (data.standalone && data.standalone.files) || [];

    const countEl = document.getElementById('artifacts-count');
    if (countEl) countEl.textContent = jobs.length + ' 个任务产物包';

    if (!jobs.length) {
        bodyEl.innerHTML = '<tr><td colspan="7" style="text-align:center;color:var(--text-3);padding:20px">暂无任务产物</td></tr>';
    } else {
        bodyEl.innerHTML = jobs.map(j => {
            const base = window.api.downloadURL('/api/v1/jobs/' + encodeURIComponent(j.job_id) + '/artifacts/download?format=');
            return '<tr>'
                + '<td class="mono"><a href="#/jobs/' + encodeURIComponent(j.job_id) + '">' + escapeHtml(j.job_id) + '</a></td>'
                + '<td class="mono">' + escapeHtml(j.type) + '</td>'
                + '<td>' + statusBadge(j.status) + '</td>'
                + '<td class="mono">' + escapeHtml(String(j.file_count)) + (j.has_sql ? ' <span class="badge">含 SQL</span>' : '') + '</td>'
                + '<td class="mono">' + escapeHtml(human(j.total_size)) + '</td>'
                + '<td class="mono">' + escapeHtml(fmtTime(j.created_at)) + '</td>'
                + '<td>'
                +   '<a class="btn-ghost btn-sm" href="' + base + 'tar.gz">tar.gz</a> '
                +   '<a class="btn-ghost btn-sm" href="' + base + 'zip">zip</a>'
                + '</td>'
                + '</tr>';
        }).join('');
    }

    const sDir = document.getElementById('standalone-dir');
    if (sDir) sDir.textContent = (data.standalone && data.standalone.dir) || '';
    const sBody = document.getElementById('standalone-body');
    if (sBody) {
        if (!standalone.length) {
            sBody.innerHTML = '<tr><td colspan="4" style="text-align:center;color:var(--text-3);padding:16px">无未归属产物</td></tr>';
        } else {
            sBody.innerHTML = standalone.map(f =>
                '<tr><td class="mono">' + escapeHtml(f.name) + '</td>'
                + '<td class="mono">' + escapeHtml(human(f.size)) + '</td>'
                + '<td class="mono">' + escapeHtml(fmtTime(f.modified)) + '</td>'
                + '<td><a class="btn-ghost btn-sm" href="'
                + window.api.downloadURL('/api/v1/artifacts/download?name=' + encodeURIComponent(f.name))
                + '">下载</a></td></tr>'
            ).join('');
        }
    }
}

export function render(root /*Element*/) {
    if (timer) { clearInterval(timer); timer = null; }

    root.innerHTML = ''
        + '<div class="page-head reveal" style="--i:0">'
        +   '<div>'
        +     '<div class="overline">监控 · 产物</div>'
        +     '<h1>产物库</h1>'
        +     '<p class="subtitle">每个任务的产物独立成包（关联任务号）；无任务归属的历史文件单列</p>'
        +   '</div>'
        +   '<div class="panel-actions">'
        +     '<span class="live-dot" title="每 10 秒自动刷新"></span>'
        +     '<span class="status-msg" id="artifacts-updated"></span>'
        +     '<button class="btn-ghost btn-sm" id="refresh-artifacts" type="button">刷新</button>'
        +   '</div>'
        + '</div>'
        + '<div class="panel reveal" style="--i:1">'
        +   '<div class="panel-head">'
        +     '<span class="panel-title">任务产物包<span class="badge badge-accent" id="artifacts-count"></span></span>'
        +   '</div>'
        +   '<table class="data-table">'
        +     '<thead><tr><th scope="col">任务号</th><th scope="col">类型</th><th scope="col">状态</th><th scope="col">文件数</th><th scope="col">大小</th><th scope="col">创建时间</th><th scope="col">打包下载</th></tr></thead>'
        +     '<tbody id="artifacts-body"><tr><td colspan="7" style="text-align:center;color:var(--text-3);padding:20px">加载中…</td></tr></tbody>'
        +   '</table>'
        + '</div>'
        + '<div class="panel reveal" style="--i:2">'
        +   '<div class="panel-head">'
        +     '<span class="panel-title">未归属任务的历史产物</span>'
        +     '<span class="field-help mono" id="standalone-dir" style="margin:0"></span>'
        +   '</div>'
        +   '<table class="data-table">'
        +     '<thead><tr><th scope="col">文件</th><th scope="col">大小</th><th scope="col">修改时间</th><th scope="col">操作</th></tr></thead>'
        +     '<tbody id="standalone-body"><tr><td colspan="4" style="text-align:center;color:var(--text-3);padding:16px">加载中…</td></tr></tbody>'
        +   '</table>'
        + '</div>';

    const refreshBtn = root.querySelector('#refresh-artifacts');
    if (refreshBtn) refreshBtn.addEventListener('click', loadArtifacts);

    loadArtifacts();
    timer = setInterval(loadArtifacts, 10000);
}
