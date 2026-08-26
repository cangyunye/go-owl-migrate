/* ============================================================
   owl-migrate · SPA router (ES module)
   Hash-router + token prompt overlay.
   - app.js (classic, not a module) provides window.api / toast /
     theme / jobUI / highlightSQL / highlightYAML via <script>.
   - ES modules are strict; access those globals via window.
   ============================================================ */

import { render as renderHome } from './views/home.js';
import { render as renderJobs } from './views/jobs.js';
import { render as renderJobDetail } from './views/jobDetail.js';
import { render as renderDDL } from './views/ddl.js';
import { render as renderSelect } from './views/select.js';
import { render as renderInsert } from './views/insert.js';
import { render as renderMigrate } from './views/migrate.js';
import { render as renderExport } from './views/export.js';
import { render as renderImport } from './views/import.js';
import { render as renderExportMetadata } from './views/exportMetadata.js';
import { render as renderMetadata } from './views/metadata.js';
import { render as renderConfig } from './views/config.js';
import { render as renderDataSources } from './views/datasources.js';

/* ── jobUI singleton originals ────────────────────────────────
   jobUI is created by app.js (classic script, runs before these
   ES modules). Since view modules' overrides are installed at
   render-time (not module-eval), the kernel methods below are still
   the originals at this point. If window.jobUI is somehow absent,
   fall back to undefined so the reset is a no-op rather than a throw. */
const ORIG_LOG_LINE = window.jobUI && window.jobUI.logLine;
const ORIG_FINISH = window.jobUI && window.jobUI.finish;
const ORIG_ON_COMPLETE = window.jobUI && window.jobUI.onComplete;

/* ── nav metadata for active-link + crumb ────────────────── */
const NAV = [
    { route: '/', active: '/', title: '首页' },
    { route: '/config', active: '/config', title: '配置' },
    { route: '/datasources', active: '/datasources', title: '数据源' },
    { route: '/metadata', active: '/metadata', title: '元数据' },
    { route: '/export-metadata', active: '/export-metadata', title: '元数据导出' },
    { route: '/ddl', active: '/ddl', title: 'DDL' },
    { route: '/select', active: '/select', title: 'SELECT' },
    { route: '/insert', active: '/insert', title: 'INSERT' },
    { route: '/migrate', active: '/migrate', title: '迁移' },
    { route: '/export', active: '/export', title: '导出' },
    { route: '/import', active: '/import', title: '导入' },
    { route: '/jobs', active: '/jobs', title: '任务' }
];

/* ── route table: ordered; first match wins ───────────────── */
const routes = [
    { re: /^\/$/, render: renderHome, active: '/', title: '首页' },
    { re: /^\/config$/, render: renderConfig, active: '/config', title: '配置' },
    { re: /^\/datasources$/, render: renderDataSources, active: '/datasources', title: '数据源' },
    { re: /^\/metadata$/, render: renderMetadata, active: '/metadata', title: '元数据' },
    { re: /^\/jobs$/, render: renderJobs, active: '/jobs', title: '任务' },
    { re: /^\/jobs\/([^/]+)$/, render: renderJobDetail, active: '/jobs', title: '任务详情' },
    { re: /^\/ddl$/, render: renderDDL, active: '/ddl', title: 'DDL' },
    { re: /^\/select$/, render: renderSelect, active: '/select', title: 'SELECT' },
    { re: /^\/insert$/, render: renderInsert, active: '/insert', title: 'INSERT' },
    { re: /^\/migrate$/, render: renderMigrate, active: '/migrate', title: '迁移' },
    { re: /^\/export$/, render: renderExport, active: '/export', title: '导出' },
    { re: /^\/import$/, render: renderImport, active: '/import', title: '导入' },
    { re: /^\/export-metadata$/, render: renderExportMetadata, active: '/export-metadata', title: '元数据导出' }
];

function renderComingSoon(root, params, route) {
    const label = params ? params[0] : '';
    let html = '<div class="page-head"><h1>即将推出</h1></div>'
        + '<div class="panel">'
        +   '<p>视图 <code>' + (route || '/') + '</code> 尚未实现。</p>';
    if (label) html += '<p>参数：<code>' + label + '</code></p>';
    html += '</div>';
    root.innerHTML = html;
}

function setActiveNav(active) {
    const links = document.querySelectorAll('#spa-nav .nav-link');
    links.forEach(link => {
        const r = link.getAttribute('data-route');
        link.classList.toggle('active', r === active);
    });
}

function setCrumb(title) {
    const el = document.getElementById('crumb-current');
    if (el) el.textContent = title;
}

function route(hash) {
    // jobUI is a singleton; reset its override hooks to the kernel
    // originals before every render so a prior view's overrides (e.g.
    // migrate's stage advance) never fire against a different view's DOM.
    window.jobUI.logLine = ORIG_LOG_LINE;
    window.jobUI.finish = ORIG_FINISH;
    window.jobUI.onComplete = ORIG_ON_COMPLETE;

    const path = (hash || '').replace(/^#/, '') || '/';
    const viewEl = document.getElementById('view');
    if (!viewEl) return;

    for (const r of routes) {
        const m = path.match(r.re);
        if (m) {
            const params = m.slice(1);
            setActiveNav(r.active);
            setCrumb(r.title);
            r.render(viewEl, params);
            viewEl.scrollTop = 0;
            return;
        }
    }

    // Unknown route → "coming soon" placeholder so nav doesn't dead-end.
    setActiveNav(null);
    setCrumb('未找到');
    renderComingSoon(viewEl, null, path);
}

/* ── token prompt overlay ─────────────────────────────────── */
const tokenPrompt = {
    overlay: null,
    _mount() {
        if (this.overlay) return this.overlay;
        const overlay = document.createElement('div');
        // Layout only — visual styling reuses existing .panel / .btn-* CSS.
        const os = overlay.style;
        os.position = 'fixed';
        os.inset = '0';
        os.display = 'none';
        os.alignItems = 'center';
        os.justifyContent = 'center';
        os.background = 'rgba(0, 0, 0, 0.55)';
        os.zIndex = '1000';
        os.padding = '16px';
        overlay.innerHTML = ''
            + '<div class="panel auth-panel">'
            +   '<h2>需要访问令牌</h2>'
            +   '<p>请在下方输入访问令牌以继续。</p>'
            +   '<input class="auth-input" type="password" placeholder="Bearer 令牌" spellcheck="false" autocomplete="off">'
            +   '<div class="auth-actions">'
            +     '<button class="btn-ghost auth-cancel" type="button">取消</button>'
            +     '<button class="btn-primary auth-submit" type="button">确定</button>'
            +   '</div>'
            + '</div>';
        const input = overlay.querySelector('.auth-input');
        const is = input.style;
        is.display = 'block';
        is.width = '100%';
        is.boxSizing = 'border-box';
        is.margin = '12px 0';
        is.padding = '10px 12px';
        const actions = overlay.querySelector('.auth-actions');
        actions.style.display = 'flex';
        actions.style.justifyContent = 'flex-end';
        actions.style.gap = '10px';
        document.body.appendChild(overlay);

        const submit = overlay.querySelector('.auth-submit');
        const cancel = overlay.querySelector('.auth-cancel');
        submit.addEventListener('click', () => this._submit(input.value));
        cancel.addEventListener('click', () => this.hide());
        input.addEventListener('keydown', (e) => {
            if (e.key === 'Enter') this._submit(input.value);
            if (e.key === 'Escape') this.hide();
        });
        this.overlay = overlay;
        return overlay;
    },
    _submit(token) {
        token = (token || '').trim();
        if (!token) {
            if (window.toast) window.toast.warn('请输入访问令牌', '');
            return;
        }
        if (window.api) window.api.setToken(token);
        this.hide();
        window.location.reload();
    },
    show() {
        const overlay = this._mount();
        overlay.style.display = 'flex';
        const input = overlay.querySelector('.auth-input');
        if (input) { input.value = ''; setTimeout(() => input.focus(), 0); }
    },
    hide() {
        if (this.overlay) this.overlay.style.display = 'none';
    }
};
window.authPrompt = tokenPrompt;

/* ── register hashchange + first render ───────────────────── */
window.addEventListener('hashchange', () => route(location.hash));
// ES modules execute after HTML is parsed, so #view is already present.
route(location.hash);
