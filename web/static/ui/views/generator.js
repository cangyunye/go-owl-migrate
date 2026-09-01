/* owl-migrate SPA · shared generator view factory (ddl/select/insert) */
import { escapeHtml } from '../util.js';

/**
 * buildGeneratorView({ title, overline, subtitle, endpoint, formHTML, downloadLabel, collectOptions })
 * returns a view render(root, params) that renders a page-head + a gen form panel
 * (options) + a file-list/sql-preview panel, wires generate() (api.post endpoint),
 * and uses window.renderGenFiles / window.humanSize / window.toast.
 *
 * `formHTML` is the options field markup (copy the exact field/checkbox/select
 * markup from the corresponding SSR template's {{define "content"}}); `collectOptions`
 * reads the current option values from the root element when 生成 is pressed.
 */
export function buildGeneratorView(cfg) {
    const { title, overline, subtitle, endpoint, downloadLabel, formHTML, collectOptions } = cfg;
    return async function render(root /*Element*/, params) {
        root.innerHTML = ''
            + '<div class="page-head"><div>'
            +   '<div class="overline">' + escapeHtml(overline) + '</div>'
            +   '<h1>' + escapeHtml(title) + '</h1>'
            +   '<p class="subtitle">' + escapeHtml(subtitle) + '</p>'
            + '</div></div>'
            + '<div class="panel gen-layout">'
            +   '<div class="panel gen-side">'
            +     '<div class="panel-head"><span class="panel-title">选项</span></div>'
            +     formHTML
            +     '<div class="form-actions">'
            +       '<button class="btn-primary" id="btn-gen" type="button">生成</button>'
            +       '<span class="status-msg" id="gen-status"></span>'
            +       '<span class="badge badge-accent" id="file-count" style="display:none"></span>'
            +       '<button class="btn-ghost btn-sm" id="btn-download" type="button" style="display:none">下载</button>'
            +     '</div>'
            +   '</div>'
            +   '<div class="panel">'
            +     '<div class="panel-head"><span class="panel-title">输出</span></div>'
            +     '<div class="file-tabs" id="file-list"></div>'
            +     '<div class="code-box"><pre class="sql-view" id="sql-preview"></pre></div>'
            +   '</div>'
            + '</div>'

            + '<div class="panel reveal" style="--i:3" id="gen-history">'
            +   '<div class="panel-head"><span class="panel-title">历史' + cfg.downloadLabel + ' <span class="badge" id="genh-count"></span></span></div>'
            +   '<div id="genh-list" class="gen-history"></div>'
            + '</div>';

        const status = root.querySelector('#gen-status');
        const btn = root.querySelector('#btn-gen');
        const fc = root.querySelector('#file-count');
        const dlBtn = root.querySelector('#btn-download');
        const listEl = root.querySelector('#file-list');
        const previewEl = root.querySelector('#sql-preview');
        const kind = (endpoint.match(/\/api\/v1\/(\w+)\/generate/) || [])[1] || 'ddl';
        const histList = root.querySelector('#genh-list');
        const histCount = root.querySelector('#genh-count');
        (async function loadGenHistory() {
            try {
                const data = await window.api.get('/api/v1/generations?kind=' + encodeURIComponent(kind));
                const items = data.items || [];
                histCount.textContent = items.length + ' 次';
                if (!items.length) { histList.innerHTML = '<p class="field-help">暂无历史</p>'; return; }
                histList.innerHTML = items.map(it => {
                    const t = it.created_at ? new Date(it.created_at.replace(' ', 'T') + 'Z').toLocaleString() : '—';
                    const src = it.source_label || '未知来源';
                    return '<div class="gen-row">'
                        + '<span class="gen-time">' + escapeHtml(t) + '</span>'
                        + '<span class="gen-src">' + escapeHtml(src) + '</span>'
                        + '<span class="gen-meta">' + (it.file_count || 0) + ' 文件</span>'
                        + '<span class="gen-size">' + (window.humanSize ? window.humanSize(it.size_bytes) : (it.size_bytes + ' B')) + '</span>'
                        + '<span class="gen-actions">'
                        +   '<a href="#" data-browse="' + it.id + '">浏览</a>'
                        +   '<a href="' + window.api.downloadURL(endpoint.replace(/\/generate$/, '/download') + '?id=' + it.id) + '">下载</a>'
                        + '</span>'
                        + '</div>';
                }).join('');
                histList.querySelectorAll('a[data-browse]').forEach(a => {
                    a.addEventListener('click', async (e) => {
                        e.preventDefault();
                        try {
                            const f = await window.api.get('/api/v1/generations/' + a.dataset.browse + '/files');
                            const files = f.files || [];
                            listEl.innerHTML = files.map(x => '<div class="file-tab">'
                                + escapeHtml(x.name) + '</div>').join('');
                        } catch (err) {
                            window.toast && window.toast.err('读取历史失败', (err && err.message) || '');
                        }
                    });
                });
            } catch (e) { /* history is best-effort */ }
        })();

        btn.addEventListener('click', async () => {
            status.textContent = '生成中…'; status.className = 'status-msg';
            btn.disabled = true;
            try {
                const opts = collectOptions(root);
                const resp = await window.api.post(endpoint, opts);
                fc.style.display = 'inline-flex';
                fc.textContent = resp.count + ' 个文件';
                dlBtn.style.display = 'inline-flex';
                window.renderGenFiles(resp.files, listEl, previewEl);
                status.textContent = '✓ ' + resp.count + ' 个文件'; status.className = 'status-msg ok';
                if (window.toast) window.toast.ok(downloadLabel + ' 生成完成', resp.count + ' 个文件');
            } catch (e) {
                status.textContent = '✗ ' + (e && e.message || e); status.className = 'status-msg fail';
                if (window.toast) window.toast.err('生成失败', e && e.message || '');
            } finally { btn.disabled = false; }
        });

        dlBtn.addEventListener('click', () => { window.location = window.api.downloadURL(endpoint.replace(/\/generate$/, '/download')); });

        /* prefill the target-tables box from the active config (best-effort) */
        const tablesInput = root.querySelector('#opt-tables');
        if (tablesInput) {
            try {
                const cur = await window.api.get('/api/v1/config/current');
                const t = cur && cur.values && cur.values.tables;
                if (root.isConnected && t) tablesInput.value = t;
            } catch (e) { /* prefill is optional */ }
        }
        if (cfg.afterRender) cfg.afterRender(root);
    };
}

export function tablesFieldHTML(help) {
    return '<div class="field">'
        +   '<label>目标表 <code>tables</code></label>'
        +   '<input type="text" id="opt-tables" class="mono" placeholder="留空或 * = 全部">'
        +   '<div class="field-help">' + (help || '逗号分隔表名，支持 schema.table 与通配符，如 EMP,SCOTT.DEPT,T_*') + '</div>'
        + '</div>';
}

export function collectTables(root) {
    const el = root.querySelector('#opt-tables');
    return el ? (el.value.trim() || null) : null;
}
