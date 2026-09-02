/* owl-migrate SPA · INSERT generator view (ported from web/templates/insert.html) */
import { buildGeneratorView, tablesFieldHTML, collectTables } from './generator.js';
import { escapeHtml } from '../util.js';

/* 检测表列表缓存（模块级：每次 render 由 showDetectedTables 重拉） */
let detectedTables = [];
/* 渲染竞态令牌（模块级：跨 render 实例共享，防止旧响应覆盖新缓存） */
let renderToken = 0;

function getTargetTables(root) {
    const el = root.querySelector('#opt-tables');
    if (!el) return [];
    const list = (el.value || '').split(',').map(s => s.trim()).filter(Boolean);
    return [...new Set(list)];
}

function setTargetTables(root, list) {
    const el = root.querySelector('#opt-tables');
    if (el) el.value = list.join(',');
}

function renderPills(root) {
    const listEl = root.querySelector('#detected-list');
    const filterEl = root.querySelector('#detected-filter');
    if (!listEl || !filterEl) return;
    const q = (filterEl.value || '').trim().toLowerCase();
    const current = new Set(getTargetTables(root));
    const filtered = detectedTables.filter(t => {
        const label = (t.schema + '.' + t.name).toLowerCase();
        return !q || label.includes(q);
    });
    listEl.innerHTML = '';
    filtered.forEach(t => {
        const label = t.schema + '.' + t.name;
        const on = current.has(label);
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'table-pill' + (on ? ' on' : '');
        btn.textContent = label;
        btn.title = (t.columns != null ? t.columns + ' 列' : '') + (on ? ' · 点击移除' : ' · 点击加入');
        btn.addEventListener('click', () => {
            const list = getTargetTables(root);
            const i = list.indexOf(label);
            if (i >= 0) list.splice(i, 1); else list.push(label);
            setTargetTables(root, list);
            renderPills(root);
        });
        listEl.appendChild(btn);
    });
    const count = root.querySelector('#detected-count');
    if (count) count.textContent = filtered.length + ' / ' + detectedTables.length;
}

async function showDetectedTables(root) {
    const hint = root.querySelector('#detected-hint');
    if (!hint) return;
    const t = ++renderToken;
    try {
        const resp = await window.api.get('/api/v1/insert/tables') || {};
        if (t !== renderToken) return; // 已有更新的请求，丢弃过期响应
        if (!root.isConnected) return;
        detectedTables = resp.tables || [];
        if (detectedTables.length) {
            hint.innerHTML = '数据目录 <code>' + escapeHtml(resp.data_dir || '') + '</code> 检测到 '
                + '<span id="detected-count">' + detectedTables.length + ' / ' + detectedTables.length + '</span> 张表'
                + ' <button class="btn-ghost btn-sm" id="detected-toggle" type="button">展开</button>'
                + '<div class="detected-box" id="detected-box" style="display:none">'
                +   '<div class="detected-toolbar">'
                +     '<input type="text" id="detected-filter" class="mono" placeholder="筛选表名 / Schema…">'
                +   '</div>'
                +   '<div class="detected-list" id="detected-list"></div>'
                + '</div>';
            const toggle = hint.querySelector('#detected-toggle');
            const box = hint.querySelector('#detected-box');
            const filterEl = hint.querySelector('#detected-filter');
            toggle.addEventListener('click', () => {
                const open = box.style.display === 'none';
                box.style.display = open ? '' : 'none';
                toggle.textContent = open ? '收起' : '展开';
                if (open) renderPills(root);
            });
            filterEl.addEventListener('input', () => renderPills(root));
        } else {
            hint.textContent = '数据目录 ' + (resp.data_dir || '') + ' 暂无 CSV（' + (resp.error || '请先在导出页导出数据') + '）';
        }
    } catch (e) { /* hint is optional */ }
}

export const render = buildGeneratorView({
    overline: 'generate · insert',
    title: '生成 INSERT',
    subtitle: '从 CSV 数据文件生成 INSERT SQL（离线，无需数据库）。数据目录取自配置的 import.source_dir',
    endpoint: '/api/v1/insert/generate',
    downloadLabel: 'INSERT',
    formHTML:
        '<div class="field-help" id="detected-hint" style="margin-bottom:12px"></div>'
        + tablesFieldHTML('逗号分隔表名，留空 = 配置的表清单；实际以数据目录中的 CSV 为准')
        + '<div class="field">'
        +   '<label>每批行数 <code>batch_size</code></label>'
        +   '<input type="number" id="opt-batch-size" value="100" min="1" max="10000">'
        + '</div>'
        + '<div class="field">'
        +   '<label class="check"><input type="checkbox" id="opt-truncate"> INSERT 前加 <code>TRUNCATE TABLE</code></label>'
        + '</div>'
        + '<div class="field">'
        +   '<label class="check"><input type="checkbox" id="opt-no-quote"> 不引用标识符 <code>no_quote_identifiers</code></label>'
        + '</div>',
    afterRender: showDetectedTables,
    collectOptions: (root) => ({
        tables: collectTables(root),
        batch_size: parseInt(root.querySelector('#opt-batch-size').value) || 100,
        truncate: root.querySelector('#opt-truncate').checked,
        no_quote_identifiers: root.querySelector('#opt-no-quote').checked || null
    }),
});
