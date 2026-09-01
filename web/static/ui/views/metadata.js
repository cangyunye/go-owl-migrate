/* owl-migrate SPA · metadata view (ported from web/templates/metadata.html) */
/* ============================================================
   Ports: metadata-source type toggle (csv/xlsx/database), source
   type/DSN/schema prefill (DSN mask-safe), loadMeta ->
   api.post('/api/v1/metadata/load'), renderTables ->
   api.get('/api/v1/metadata/tables'), showDetail ->
   api.get('/api/v1/metadata/tables/<schema>/<name>'), validateMeta
   -> api.get('/api/v1/metadata/validate').

   XSS-safety (the SSR gap): the SSR built each row with an inline
   onclick using raw schema/name and interpolated all columns
   unsafely. Here:
   - Table rows are built with DOM APIs; the detail link uses
     data-schema/data-table attributes (set via dataset, never
     string interpolation) + one delegated click listener.
   - Detail columns are rendered with escapeHtml() for every
     server-derived value.
   ============================================================ */

import { escapeHtml } from '../util.js';

let dsnExamples = {};

/* 表列表缓存与分页状态（模块级：跨路由切换存活） */
let tablesCache = [];
let page = 1;
const pageSize = 50;

export async function render(root /*Element*/, params) {
    root.innerHTML = ''
        + '<div class="page-head reveal" style="--i:0">'
        +   '<div>'
        +     '<div class="overline">prepare · metadata</div>'
        +     '<h1>元数据</h1>'
        +     '<p class="subtitle">指定元数据来源并加载表结构，供 DDL / SELECT 生成与校验使用</p>'
        +   '</div>'
        + '</div>'

        + '<div class="panel reveal" style="--i:1">'
        +   '<div class="panel-head"><span class="panel-title">元数据来源</span></div>'
        +   '<div class="field">'
        +     '<label>来源类型</label>'
        +     '<select id="m-type">'
        +       '<option value="csv">CSV 目录</option>'
        +       '<option value="xlsx">XLSX 文件</option>'
        +       '<option value="database">数据库</option>'
        +     '</select>'
        +   '</div>'
        +   '<div class="field" id="f-csv">'
        +     '<label>CSV 元数据目录</label>'
        +     '<input type="text" id="m-csv-path" class="mono" value="./testdata/csv/">'
        +     '<div class="field-help">包含 <code>tables.csv</code> / <code>columns.csv</code> 等的目录</div>'
        +   '</div>'
        +   '<div class="field hidden" id="f-xlsx">'
        +     '<label>XLSX 文件路径</label>'
        +     '<input type="text" id="m-xlsx-path" class="mono" value="./metadata/schema.xlsx">'
        +   '</div>'
        +   '<div class="field hidden" id="f-db-type">'
        +     '<label>数据库类型</label>'
        +     '<select id="m-src-type"></select>'
        +   '</div>'
        +   '<div class="field hidden" id="f-db-dsn">'
        +     '<label>数据库 DSN</label>'
        +     '<select id="m-ds-pick" class="hidden" style="margin-bottom:6px"><option value="">— 手动输入 DSN —</option></select>'
        +     '<input type="text" id="m-src-dsn" class="mono">'
        +     '<div class="field-help" id="m-dsn-hint"></div>'
        +   '</div>'
        +   '<div class="field hidden" id="f-db-schema">'
        +     '<label>Schema</label>'
        +     '<input type="text" id="m-src-schema" class="mono">'
        +   '</div>'
        +   '<div class="form-actions">'
        +     '<button class="btn-primary" id="btn-load" type="button">'
        +       '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v12"/><path d="m8 11 4 4 4-4"/><path d="M8 5H4a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-4"/></svg>'
        +       '加载元数据'
        +     '</button>'
        +     '<button class="btn-ghost" id="btn-validate" type="button">校验</button>'
        +     '<span id="meta-status" class="status-msg"></span>'
        +   '</div>'
        + '</div>'

        + '<div class="panel reveal" style="--i:2;display:none" id="tables-panel">'
        +   '<div class="panel-head">'
        +     '<span class="panel-title">表列表 <span class="badge badge-accent" id="table-count"></span></span>'
        +   '</div>'
        +   '<div class="meta-toolbar">'
        +     '<input type="text" id="tables-filter" class="mono" placeholder="筛选表名 / Schema…">'
        +   '</div>'
        +   '<table class="data-table">'
        +     '<thead><tr><th>Schema</th><th>表名</th><th>列数</th><th>主键</th><th></th></tr></thead>'
        +     '<tbody id="tables-body"></tbody>'
        +   '</table>'
        +   '<div class="pager" id="tables-pager" style="display:none">'
        +     '<span class="field-help" id="pager-info" style="margin:0"></span>'
        +     '<button class="btn-ghost btn-sm" id="pager-prev" type="button">上一页</button>'
        +     '<button class="btn-ghost btn-sm" id="pager-next" type="button">下一页</button>'
        +   '</div>'
        + '</div>'

        + '<div class="panel reveal" style="--i:3;display:none" id="detail-panel">'
        +   '<div class="panel-head"><span class="panel-title" id="detail-title">表详情</span></div>'
        +   '<div id="detail-content"></div>'
        + '</div>'

        + '<div class="panel reveal" style="--i:4;display:none" id="validate-panel">'
        +   '<div class="panel-head">'
        +     '<span class="panel-title">校验结果 <span class="badge" id="validate-count"></span></span>'
        +   '</div>'
        +   '<ul id="validate-list" class="validate-list"></ul>'
        + '</div>';

    const typeSel = root.querySelector('#m-type');
    const csvPath = root.querySelector('#m-csv-path');
    const csvField = root.querySelector('#f-csv');
    const xlsxPath = root.querySelector('#m-xlsx-path');
    const xlsxField = root.querySelector('#f-xlsx');
    const srcType = root.querySelector('#m-src-type');
    const dsnInput = root.querySelector('#m-src-dsn');
    const dsPick = root.querySelector('#m-ds-pick');
    const schemaInput = root.querySelector('#m-src-schema');
    const hint = root.querySelector('#m-dsn-hint');
    const dbFields = ['f-db-type', 'f-db-dsn', 'f-db-schema'].map(id => root.querySelector('#' + id));
    const statusEl = root.querySelector('#meta-status');
    const tablesPanel = root.querySelector('#tables-panel');
    const tableCount = root.querySelector('#table-count');
    const tbody = root.querySelector('#tables-body');
    const detailPanel = root.querySelector('#detail-panel');
    const detailTitle = root.querySelector('#detail-title');
    const detailContent = root.querySelector('#detail-content');
    const validatePanel = root.querySelector('#validate-panel');
    const validateCount = root.querySelector('#validate-count');
    const validateList = root.querySelector('#validate-list');
    const btnLoad = root.querySelector('#btn-load');
    const btnValidate = root.querySelector('#btn-validate');
    const filterInput = root.querySelector('#tables-filter');
    const pagerEl = root.querySelector('#tables-pager');
    const pagerInfo = root.querySelector('#pager-info');
    const pagerPrev = root.querySelector('#pager-prev');
    const pagerNext = root.querySelector('#pager-next');

    /* ── metadata-type toggle ────────────────────────────────── */
    function toggleMetaFields() {
        const type = typeSel.value;
        csvField.classList.toggle('hidden', type !== 'csv');
        xlsxField.classList.toggle('hidden', type !== 'xlsx');
        const isDB = type === 'database';
        dbFields.forEach(el => el.classList.toggle('hidden', !isDB));
    }

    /* ── DSN hint ───────────────────────────────────────────── */
    function updateDsnHint() {
        const dialect = srcType.value;
        const example = dsnExamples[dialect];
        hint.textContent = example ? '格式示例：' + dsnExamples[dialect] : '';
        hint.classList.toggle('has-example', !!example);
    }

    /* ── payload builder ─────────────────────────────────────── */
    function buildMetaPayload() {
        const type = typeSel.value;
        const payload = { metadata: { type: type }, source: {} };
        if (type === 'csv') {
            payload.metadata.csv = { path: csvPath.value, column_name_matching: 'case_insensitive' };
        } else if (type === 'xlsx') {
            payload.metadata.xlsx = { path: xlsxPath.value };
        } else {
            payload.source = {
                type: srcType.value,
                dsn: dsnInput.value,
                schema: schemaInput.value,
            };
        }
        return payload;
    }

    function showStatus(msg, kind) {
        statusEl.textContent = msg;
        statusEl.className = 'status-msg' + (kind ? ' ' + kind : '');
    }

    /* ── load metadata ───────────────────────────────────────── */
    async function loadMeta() {
        showStatus('加载中…', null);
        btnLoad.disabled = true;
        try {
            const resp = await window.api.post('/api/v1/metadata/load', buildMetaPayload());
            tablesPanel.style.display = 'block';
            await renderTables();
            showStatus('✓ 已加载 ' + resp.table_count + ' 张表', 'ok');
            window.toast.ok('元数据加载完成', resp.table_count + ' 张表');
        } catch (e) {
            showStatus('✗ ' + (e && e.message || e), 'fail');
            window.toast.err('加载失败', e && e.message || '');
        } finally {
            btnLoad.disabled = false;
        }
    }

    /* ── table list (DOM-built, XSS-safe) ────────────────────── */
    async function renderTables() {
        tablesCache = await window.api.get('/api/v1/metadata/tables') || [];
        page = 1;
        applyFilterAndPage();
    }

    function applyFilterAndPage() {
        const q = (filterInput.value || '').trim().toLowerCase();
        const filtered = q
            ? tablesCache.filter(t =>
                (t.schema || '').toLowerCase().includes(q) ||
                (t.name || '').toLowerCase().includes(q))
            : tablesCache.slice();

        tableCount.textContent = filtered.length + ' / ' + tablesCache.length;
        const totalPages = Math.max(1, Math.ceil(filtered.length / pageSize));
        if (page < 1) page = 1;
        if (page > totalPages) page = totalPages;
        const start = (page - 1) * pageSize;
        const rows = filtered.slice(start, start + pageSize);

        tbody.innerHTML = '';
        rows.forEach(t => {
            const tr = document.createElement('tr');

            const tSchema = document.createElement('td');
            tSchema.className = 'mono';
            tSchema.textContent = t.schema;
            tr.appendChild(tSchema);

            const tName = document.createElement('td');
            tName.className = 'mono';
            tName.style.color = 'var(--text)';
            tName.style.fontWeight = '600';
            tName.textContent = t.name;
            tr.appendChild(tName);

            const tCols = document.createElement('td');
            tCols.className = 'mono';
            tCols.textContent = (t.columns && t.columns.length) || 0;
            tr.appendChild(tCols);

            const tPk = document.createElement('td');
            tPk.className = 'mono';
            tPk.style.color = 'var(--text-2)';
            tPk.textContent = ((t.primary_key || []).join(', ')) || '—';
            tr.appendChild(tPk);

            const tAction = document.createElement('td');
            const link = document.createElement('a');
            link.href = '#';
            link.dataset.schema = t.schema;
            link.dataset.table = t.name;
            link.textContent = '详情';
            tAction.appendChild(link);
            tr.appendChild(tAction);

            tbody.appendChild(tr);
        });

        pagerInfo.textContent = '共 ' + filtered.length + ' 张 · 第 ' + page + '/' + totalPages + ' 页';
        pagerPrev.disabled = page <= 1;
        pagerNext.disabled = page >= totalPages;
        pagerEl.style.display = filtered.length > pageSize ? '' : 'none';
    }

    /* ── table detail (escaped) ──────────────────────────────── */
    async function showDetail(schema, name) {
        const d = await window.api.get('/api/v1/metadata/tables/' + encodeURIComponent(schema) + '/' + encodeURIComponent(name));
        detailPanel.style.display = 'block';
        detailTitle.textContent = schema + '.' + name;
        let html = '<table class="data-table"><thead><tr><th>列名</th><th>类型</th><th>可空</th><th>默认值</th></tr></thead><tbody>';
        (d.columns || []).forEach(c => {
            html += '<tr><td class="mono" style="font-weight:600">' + escapeHtml(c.name) + '</td>' +
                '<td class="mono" style="color:var(--cyan)">' + escapeHtml(c.type) + (c.length ? '(' + escapeHtml(c.length) + ')' : '') + '</td>' +
                '<td>' + (c.nullable ? '<span class="st-ok">是</span>' : '<span style="color:var(--text-3)">否</span>') + '</td>' +
                '<td class="mono" style="color:var(--text-2)">' + escapeHtml(c.default || '—') + '</td></tr>';
        });
        html += '</tbody></table>';
        html += '<p class="field-help" style="margin-top:12px">主键：<span class="mono" style="color:var(--amber)">' + escapeHtml((d.primary_keys || []).join(', ')) + '</span></p>';
        detailContent.innerHTML = html;
        detailPanel.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
    }

    /* ── validate ────────────────────────────────────────────── */
    async function validateMeta() {
        btnValidate.disabled = true;
        try {
            const resp = await window.api.get('/api/v1/metadata/validate') || {};
            validatePanel.style.display = 'block';
            validateCount.textContent = resp.count + ' 项';
            validateList.innerHTML = '';
            if (resp.count === 0) {
                const li = document.createElement('li');
                li.className = 'sev-info';
                li.textContent = '✓ 无校验问题';
                validateList.appendChild(li);
                return;
            }
            (resp.errors || []).forEach((e, i) => {
                const li = document.createElement('li');
                li.className = 'sev-' + (e.severity || 'info');
                li.style.animationDelay = (i * 40) + 'ms';
                li.textContent = '[' + (e.severity || 'info') + '] ' + (e.message || '');
                validateList.appendChild(li);
            });
        } catch (e) {
            showStatus('✗ ' + (e && e.message || e), 'fail');
            window.toast.err('校验失败', e && e.message || '');
        } finally {
            btnValidate.disabled = false;
        }
    }

    /* ── wire events (single delegated listener, no inline onclick) ── */
    btnLoad.addEventListener('click', loadMeta);
    btnValidate.addEventListener('click', validateMeta);
    typeSel.addEventListener('change', toggleMetaFields);
    srcType.addEventListener('change', updateDsnHint);
    tbody.addEventListener('click', (e) => {
        const link = e.target.closest('a[data-table]');
        if (!link) return;
        e.preventDefault();
        showDetail(link.dataset.schema, link.dataset.table);
    });
    filterInput.addEventListener('input', () => { page = 1; applyFilterAndPage(); });
    pagerPrev.addEventListener('click', () => { page--; applyFilterAndPage(); });
    pagerNext.addEventListener('click', () => { page++; applyFilterAndPage(); });

    /* ── populate dialect select + hint from scenarios ───────── */
    try {
        const data = await window.api.get('/api/v1/scenarios');
        dsnExamples = data.dsn_examples || {};
        const dialects = ((data.scenarios && data.scenarios[0] && data.scenarios[0].fields)
            .find(f => f.name === 'source_type') || {}).options || [];
        dialects.forEach(d => {
            const o = document.createElement('option');
            o.value = d;
            o.textContent = d;
            srcType.appendChild(o);
        });
    } catch (e) {
        window.toast && window.toast.err('加载数据库类型失败', e && e.message || '');
    }

    /* ── prefill from the current config (values include the resolved DSN) ── */
    try {
        const cur = await window.api.get('/api/v1/config/current');
        const vs = (cur && cur.values) || {};
        if (cur && !cur.empty) {
            if (vs.metadata_type) typeSel.value = vs.metadata_type;
            if (vs.csv_path) csvPath.value = vs.csv_path;
            if (vs.xlsx_path) xlsxPath.value = vs.xlsx_path;
            if (vs.source_type) srcType.value = vs.source_type;
            if (vs.source_dsn) dsnInput.value = vs.source_dsn;
            if (vs.source_schema) schemaInput.value = vs.source_schema;
        }
    } catch (e) { /* config prefill is best-effort; leave fields blank */ }

    /* ── saved-datasource quick pick (DSN resolved server-side) ── */
    try {
        const dsList = await window.api.get('/api/v1/datasources') || [];
        if (dsList.length) {
            dsPick.classList.remove('hidden');
            dsList.forEach(function (d) {
                const o = document.createElement('option');
                o.value = d.name;
                o.textContent = d.name + ' · ' + (d.type || '?') + (d.schema ? ' · ' + d.schema : '');
                dsPick.appendChild(o);
            });
        }
        dsPick.addEventListener('change', async function () {
            if (!this.value) { dsnInput.value = ''; updateDsnHint(); return; }
            try {
                const p = await window.api.post('/api/v1/datasources/' + encodeURIComponent(this.value) + '/pick', {});
                if (p.type) srcType.value = p.type;
                if (p.schema && !schemaInput.value) schemaInput.value = p.schema;
                dsnInput.value = p.ref || ('datasource:' + this.value);
                hint.textContent = '已选数据源 ' + this.value + '，DSN 由服务端解密';
                hint.classList.add('has-example');
            } catch (e) {
                window.toast && window.toast.err('读取数据源失败', (e && e.message) || '');
            }
        });
    } catch (e) { /* data-source list is best-effort */ }

    /* ── auto-restore table list if metadata already loaded ──── */
    async function autoRestoreTables() {
        try {
            const st = await window.api.get('/api/v1/config/status');
            if (!st || !st.metadata_loaded) return;
            tablesPanel.style.display = 'block';
            await renderTables();
        } catch (e) { /* best-effort: leave the page blank */ }
    }

    toggleMetaFields();
    updateDsnHint();
    autoRestoreTables();
}
