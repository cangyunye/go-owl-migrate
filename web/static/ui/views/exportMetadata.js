/* owl-migrate SPA · export-metadata view (ported from web/templates/export_metadata.html) */
/* ============================================================
   Ports: source-type select populated from /api/v1/scenarios
   (source_type options) + dsn_examples hint, prefill of
   source.type/source.schema from /api/v1/config (DSN is prefilled
   only when the config value is NOT masked), and doExport ->
   api.post('/api/v1/metadata/export', {source:{type,dsn,schema},
   format, scope}). Result file tabs and error text are HTML-escaped.

   DSN prefill mask-safety: GET /api/v1/config masks the DSN password
   (config.MaskDSN -> literal '******'). Prefilling a masked secret
   is wrong — the user cannot see the real password and would re-send
   the mask. So when the config DSN contains an asterisk we skip the
   DSN prefill entirely and leave the field blank for the user to
   type the real DSN.
   ============================================================ */

import { escapeHtml } from '../util.js';

const MASK_RE = /\*/;

/* Module-scoped dsn_examples; populated during init and read by updateHint. */
let dsnExamples = {};

export async function render(root /*Element*/, params) {
    root.innerHTML = ''
        + '<div class="page-head reveal" style="--i:0">'
        +   '<div>'
        +     '<div class="overline">prepare · extract</div>'
        +     '<h1>元数据导出</h1>'
        +     '<p class="subtitle">从源库提取元数据并导出为 CSV / SQL 文件 — 供离线迁移使用</p>'
        +   '</div>'
        + '</div>'

        + '<div class="panel reveal" style="--i:1">'
        +   '<div class="panel-head"><span class="panel-title">导出参数</span></div>'
        +   '<div class="field">'
        +     '<label>数据库类型</label>'
        +     '<select id="em-src-type"></select>'
        +   '</div>'
        +   '<div class="field">'
        +     '<label>DSN</label>'
        +     '<input type="text" id="em-src-dsn" class="mono" placeholder="user/pass@host:port/service">'
        +     '<div class="field-help" id="em-dsn-hint"></div>'
        +   '</div>'
        +   '<div class="field">'
        +     '<label>Schema</label>'
        +     '<input type="text" id="em-src-schema" class="mono" placeholder="SCOTT">'
        +   '</div>'
        +   '<div class="field">'
        +     '<label>导出格式</label>'
        +     '<select id="em-format">'
        +       '<option value="csv">CSV（多文件）</option>'
        +       '<option value="sql">SQL（INSERT 语句）</option>'
        +     '</select>'
        +   '</div>'
        +   '<div class="field">'
        +     '<label>范围</label>'
        +     '<input type="text" id="em-scope" class="mono" value="all" placeholder="all / schema:NAME / table:T1,T2">'
        +     '<div class="field-help"><code>all</code> = 整个 schema · <code>schema:NAME</code> = 指定 schema · <code>table:T1,T2</code> = 指定表</div>'
        +   '</div>'
        +   '<div class="form-actions">'
        +     '<button class="btn-primary" id="btn-export" type="button">'
        +       '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" x2="12" y1="15" y2="3"/></svg>'
        +       '导出元数据'
        +     '</button>'
        +     '<span id="em-status" class="status-msg"></span>'
        +   '</div>'
        + '</div>'

        + '<div class="panel reveal" style="--i:2;display:none" id="em-result">'
        +   '<div class="panel-head">'
        +     '<span class="panel-title">导出结果 <span class="badge badge-green" id="em-count"></span></span>'
        +     '<a id="em-download" class="btn-primary btn-sm" href="/api/v1/metadata/export/download">下载 ZIP</a>'
        +   '</div>'
        +   '<div id="em-files"></div>'
        + '</div>';

    const sel = root.querySelector('#em-src-type');
    const dsnInput = root.querySelector('#em-src-dsn');
    const schemaInput = root.querySelector('#em-src-schema');
    const hint = root.querySelector('#em-dsn-hint');
    const scopeInput = root.querySelector('#em-scope');
    const formatSel = root.querySelector('#em-format');
    const resultEl = root.querySelector('#em-result');
    const countEl = root.querySelector('#em-count');
    const filesEl = root.querySelector('#em-files');
    const statusEl = root.querySelector('#em-status');
    const btn = root.querySelector('#btn-export');

    function updateHint() {
        const dialect = sel.value;
        const example = dsnExamples[dialect];
        hint.textContent = example ? '格式示例：' + dsnExamples[dialect] : '';
        hint.classList.toggle('has-example', !!example);
    }

    async function doExport() {
        statusEl.textContent = '导出中…'; statusEl.className = 'status-msg';
        btn.disabled = true;
        try {
            const resp = await window.api.post('/api/v1/metadata/export', {
                source: {
                    type: sel.value,
                    dsn: dsnInput.value,
                    schema: schemaInput.value,
                },
                format: formatSel.value,
                scope: scopeInput.value,
            });
            resultEl.style.display = 'block';
            countEl.textContent = resp.table_count + ' 张表 · ' + resp.count + ' 个文件';
            let html = '<div class="file-tabs">'
                + (resp.files || []).map(f =>
                    '<span class="file-tab" title="' + escapeHtml(String(f.content || '').length) + ' bytes">'
                    + escapeHtml(f.name) + '</span>').join('')
                + '</div>';
            if (resp.errors && resp.errors.length) {
                html += '<p class="status-msg fail" style="margin-top:10px">'
                    + resp.errors.map(escapeHtml).join('<br>') + '</p>';
            }
            filesEl.innerHTML = html;
            statusEl.textContent = '✓ 导出完成'; statusEl.className = 'status-msg ok';
            window.toast.ok('元数据导出完成', resp.table_count + ' 张表，' + resp.count + ' 个文件');
        } catch (e) {
            statusEl.textContent = '✗ ' + (e && e.message || e); statusEl.className = 'status-msg fail';
            window.toast.err('导出失败', e && e.message || '');
        } finally {
            btn.disabled = false;
        }
    }

    btn.addEventListener('click', doExport);
    sel.addEventListener('change', updateHint);

    /* ── populate dialect select + hint from scenarios ─────────── */
    try {
        const data = await window.api.get('/api/v1/scenarios');
        dsnExamples = data.dsn_examples || {};
        const dialects = ((data.scenarios && data.scenarios[0] && data.scenarios[0].fields)
            .find(f => f.name === 'source_type') || {}).options || [];
        dialects.forEach(d => {
            const o = document.createElement('option');
            o.value = d;
            o.textContent = d;
            sel.appendChild(o);
        });
    } catch (e) {
        window.toast && window.toast.err('加载数据库类型失败', e && e.message || '');
    }

    /* ── prefill source.type/schema (DSN mask-safe) ────────────── */
    try {
        const cfg = await window.api.get('/api/v1/config');
        if (cfg && cfg.source) {
            if (cfg.source.type) sel.value = cfg.source.type;
            if (cfg.source.dsn && !MASK_RE.test(cfg.source.dsn)) dsnInput.value = cfg.source.dsn;
            if (cfg.source.schema) schemaInput.value = cfg.source.schema;
        }
    } catch (e) { /* config prefill is best-effort; leave fields blank */ }

    updateHint();
}
