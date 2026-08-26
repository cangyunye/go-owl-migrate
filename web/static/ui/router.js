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

/* ── nav metadata for active-link + crumb ────────────────── */
const NAV = [
    { route: '/', active: '/', title: '首页' },
    { route: '/config', active: '/config', title: '配置' },
    { route: '/metadata', active: '/metadata', title: '元数据' },
    { route: '/export-metadata', active: '/export-metadata', title: '元数据导出' },
    { route: '/ddl', active: '/ddl', title: 'DDL' },
    { route: '/select', active: '/select', title: 'SELECT' },
    { route: '/insert', active: '/insert', title: 'INSERT' },
    { route: '/migrate', active: '/migrate', title: '迁移' },
    { route: '/export', active: '/export', title: '导出' },
    { route: '/import', active: '/import', title: '导入' },
    { route: '/jobs', active: '/jobs', title: '任务' },
    { route: '/docs', active: '/docs', title: '文档' }
];

/* ── route table: ordered; first match wins ───────────────── */
const routes = [
    { re: /^\/$/, render: renderHome, active: '/', title: '首页' },
    { re: /^\/jobs$/, render: renderJobs, active: '/jobs', title: '任务' },
    { re: /^\/jobs\/([^/]+)$/, render: renderJobDetail, active: '/jobs', title: '任务详情' }
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
