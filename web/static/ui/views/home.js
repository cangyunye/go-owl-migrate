/* owl-migrate SPA · home view (ported from web/templates/index.html) */
import { statusBadge, escapeHtml } from '../util.js';

let recentTimer = null;

const SYS_STRIP =
    '<div class="sys-strip" id="sys-strip">'
    +   '<div class="sys-card"><div class="sys-k">目标方言</div><div class="sys-v" id="sys-dialect">—</div></div>'
    +   '<div class="sys-card"><div class="sys-k">元数据</div><div class="sys-v" id="sys-meta">—</div></div>'
    +   '<div class="sys-card"><div class="sys-k">已加载表</div><div class="sys-v" id="sys-tables">—</div></div>'
    +   '<div class="sys-card"><div class="sys-k">最近任务</div><div class="sys-v" id="sys-lastjob">—</div></div>'
    + '</div>';

const FLOW_BOARD =
    '<div class="flow-board">'
    /* 01 准备 */
    + '<div class="flow-stage stage-prepare">'
    +   '<div class="stage-head"><span class="stage-num">01</span><span class="stage-name">准备 · Prepare</span></div>'
    +   '<a href="#/config" class="action-card">'
    +     '<span class="ac-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/></svg></span>'
    +     '<span class="ac-body"><span class="ac-title">配置</span><span class="ac-desc">场景化表单 · 实时生成 YAML</span></span>'
    +     '<span class="ac-arrow"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14"/><path d="m12 5 7 7-7 7"/></svg></span>'
    +   '</a>'
    +   '<a href="#/metadata" class="action-card">'
    +     '<span class="ac-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5V19A9 3 0 0 0 21 19V5"/><path d="M3 12A9 3 0 0 0 21 12"/></svg></span>'
    +     '<span class="ac-body"><span class="ac-title">元数据</span><span class="ac-desc">加载表结构 · 浏览 · 校验</span></span>'
    +     '<span class="ac-arrow"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14"/><path d="m12 5 7 7-7 7"/></svg></span>'
    +   '</a>'
    +   '<a href="#/export-metadata" class="action-card">'
    +     '<span class="ac-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5v14a9 3 0 0 0 9 3"/><path d="M21 5v6"/><path d="m16 19 2 2 4-4"/></svg></span>'
    +     '<span class="ac-body"><span class="ac-title">元数据导出</span><span class="ac-desc">从源库抽取结构 → CSV/SQL</span></span>'
    +     '<span class="ac-arrow"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14"/><path d="m12 5 7 7-7 7"/></svg></span>'
    +   '</a>'
    +   '<a href="#/config" class="action-card">'
    +     '<span class="ac-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"/><path d="m9 12 2 2 4-4"/></svg></span>'
    +     '<span class="ac-body"><span class="ac-title">校验配置</span><span class="ac-desc">元数据与配置正确性检查</span></span>'
    +     '<span class="ac-arrow"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14"/><path d="m12 5 7 7-7 7"/></svg></span>'
    +   '</a>'
    + '</div>'
    /* 02 生成 */
    + '<div class="flow-stage stage-generate">'
    +   '<div class="stage-head"><span class="stage-num">02</span><span class="stage-name">生成 · Generate</span></div>'
    +   '<a href="#/ddl" class="action-card">'
    +     '<span class="ac-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="m10 12-2 2 2 2"/><path d="m14 12 2 2-2 2"/></svg></span>'
    +     '<span class="ac-body"><span class="ac-title">生成 DDL</span><span class="ac-desc">目标库建表语句</span></span>'
    +     '<span class="ac-arrow"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14"/><path d="m12 5 7 7-7 7"/></svg></span>'
    +   '</a>'
    +   '<a href="#/select" class="action-card">'
    +     '<span class="ac-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v18"/><rect width="18" height="18" x="3" y="3" rx="2"/><path d="M3 9h18"/><path d="M3 15h18"/></svg></span>'
    +     '<span class="ac-body"><span class="ac-title">生成 SELECT</span><span class="ac-desc">分页查询 · 手动导出</span></span>'
    +     '<span class="ac-arrow"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14"/><path d="m12 5 7 7-7 7"/></svg></span>'
    +   '</a>'
    +   '<a href="#/insert" class="action-card">'
    +     '<span class="ac-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M11 12H3"/><path d="M16 6H3"/><path d="M16 18H3"/><path d="M18 9v6"/><path d="M21 12h-6"/></svg></span>'
    +     '<span class="ac-body"><span class="ac-title">生成 INSERT</span><span class="ac-desc">从 CSV 生成 INSERT SQL</span></span>'
    +     '<span class="ac-arrow"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14"/><path d="m12 5 7 7-7 7"/></svg></span>'
    +   '</a>'
    + '</div>'
    /* 03 执行 */
    + '<div class="flow-stage stage-execute">'
    +   '<div class="stage-head"><span class="stage-num">03</span><span class="stage-name">执行 · Execute</span></div>'
    +   '<a href="#/migrate" class="action-card">'
    +     '<span class="ac-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m16 3 4 4-4 4"/><path d="M20 7H4"/><path d="m8 21-4-4 4-4"/><path d="M4 17h16"/></svg></span>'
    +     '<span class="ac-body"><span class="ac-title">完整迁移</span><span class="ac-desc">源库 → 导出 → 目标库</span></span>'
    +     '<span class="ac-arrow"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14"/><path d="m12 5 7 7-7 7"/></svg></span>'
    +   '</a>'
    +   '<a href="#/export" class="action-card">'
    +     '<span class="ac-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" x2="12" y1="15" y2="3"/></svg></span>'
    +     '<span class="ac-body"><span class="ac-title">导出数据</span><span class="ac-desc">CSV / SQL / XLSX · 离线转换</span></span>'
    +     '<span class="ac-arrow"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14"/><path d="m12 5 7 7-7 7"/></svg></span>'
    +   '</a>'
    +   '<a href="#/import" class="action-card">'
    +     '<span class="ac-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" x2="12" y1="3" y2="15"/></svg></span>'
    +     '<span class="ac-body"><span class="ac-title">导入数据</span><span class="ac-desc">CSV → 目标库 · 断点续传</span></span>'
    +     '<span class="ac-arrow"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14"/><path d="m12 5 7 7-7 7"/></svg></span>'
    +   '</a>'
    + '</div>'
    /* 04 监控 */
    + '<div class="flow-stage stage-monitor">'
    +   '<div class="stage-head"><span class="stage-num">04</span><span class="stage-name">监控 · Monitor</span></div>'
    +   '<a href="#/jobs" class="action-card">'
    +     '<span class="ac-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 12h-4l-3 9L9 3l-3 9H2"/></svg></span>'
    +     '<span class="ac-body"><span class="ac-title">任务历史</span><span class="ac-desc">进度 · 检查点 · 恢复</span></span>'
    +     '<span class="ac-arrow"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14"/><path d="m12 5 7 7-7 7"/></svg></span>'
    +   '</a>'
    +   '<a href="/docs" class="action-card">'
    +     '<span class="ac-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 7v14"/><path d="M3 18a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h5a4 4 0 0 1 4 4 4 4 0 0 1 4-4h5a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1h-6a3 3 0 0 0-3 3 3 3 0 0 0-3-3z"/></svg></span>'
    +     '<span class="ac-body"><span class="ac-title">文档</span><span class="ac-desc">命令 · 配置 · 排障</span></span>'
    +     '<span class="ac-arrow"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14"/><path d="m12 5 7 7-7 7"/></svg></span>'
    +   '</a>'
    + '</div>'
    + '</div>';

const RECENT_PANEL =
    '<div class="panel">'
    +   '<div class="panel-head">'
    +     '<span class="panel-title">最近任务</span>'
    +     '<div class="panel-actions">'
    +       '<span class="live-dot" id="jobs-live" title="自动刷新"></span>'
    +       '<a class="btn-ghost btn-sm" href="#/jobs">全部任务</a>'
    +     '</div>'
    +   '</div>'
    +   '<table class="data-table">'
    +     '<thead><tr><th>ID</th><th>类型</th><th>状态</th><th>创建时间</th><th>完成时间</th></tr></thead>'
    +     '<tbody id="recent-body"><tr><td colspan="5" class="lib-empty" style="text-align:center;color:var(--text-3);padding:18px">加载中…</td></tr></tbody>'
    +   '</table>'
    + '</div>';

async function loadRecent() {
    try {
        const jobs = await window.api.get('/api/v1/jobs');
        const tbody = document.getElementById('recent-body');
        if (!tbody) return;
        const recent = (jobs || []).slice(0, 5);
        if (!recent.length) {
            tbody.innerHTML = '<tr><td colspan="5" style="text-align:center;color:var(--text-3);padding:18px">暂无任务记录 — 从「执行」阶段启动第一个迁移</td></tr>';
            return;
        }
        tbody.innerHTML = recent.map(j =>
            '<tr><td class="mono"><a href="#/jobs/' + encodeURIComponent(j.job_id) + '">' + escapeHtml(j.job_id) + '</a></td>' +
            '<td class="mono">' + escapeHtml(j.type) + '</td>' +
            '<td>' + statusBadge(j.status) + '</td>' +
            '<td class="mono" style="color:var(--text-2)">' + escapeHtml(j.created_at || '') + '</td>' +
            '<td class="mono" style="color:var(--text-2)">' + escapeHtml(j.finished_at || '—') + '</td></tr>'
        ).join('');
    } catch (e) { /* best-effort */ }
}

async function loadSys() {
    try {
        const st = await window.api.get('/api/v1/config/status');
        const d = document.getElementById('sys-dialect');
        const m = document.getElementById('sys-meta');
        const t = document.getElementById('sys-tables');
        if (d) d.innerHTML = st.target_dialect
            ? '<span class="ok">' + escapeHtml(st.target_dialect) + '</span>'
            : '<span class="warn">未配置</span>';
        if (m) m.innerHTML = st.metadata_loaded
            ? '<span class="ok">' + escapeHtml(st.metadata_type || '已加载') + '</span>'
            : '<span class="warn">未加载</span>';
        if (t) t.textContent = st.metadata_loaded ? (st.table_count + ' 张') : '—';
    } catch (e) { /* best-effort */ }
    try {
        const jobs = await window.api.get('/api/v1/jobs');
        const lj = document.getElementById('sys-lastjob');
        if (lj && jobs && jobs.length) {
            const j = jobs[0];
            lj.innerHTML =
                '<span class="mono" style="font-size:12px">' + escapeHtml(j.type) + '</span>&nbsp;' + statusBadge(j.status);
        }
    } catch (e) { /* best-effort */ }
}

export function render(root /*Element*/, params) {
    if (recentTimer) { clearInterval(recentTimer); recentTimer = null; }
    root.innerHTML = ''
        + '<div class="page-head">'
        +   '<div>'
        +     '<div class="overline">owl migration console</div>'
        +     '<h1>数据库迁移工作台</h1>'
        +     '<p class="subtitle">离线优先 · 多方言 · 端到端管道&nbsp;&nbsp;<span class="mono" style="color:var(--text-3)">Oracle / PostgreSQL / MySQL / GoldenDB / OceanBase</span></p>'
        +   '</div>'
        + '</div>'
        + SYS_STRIP
        + FLOW_BOARD
        + RECENT_PANEL;
    loadSys();
    loadRecent();
    recentTimer = setInterval(loadRecent, 6000);
}
