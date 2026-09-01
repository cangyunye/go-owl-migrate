/* owl-migrate SPA · 数据源 view
   Manages reusable database connection profiles (web-UI only). Each profile
   stores type + schema + an encrypted DSN; the DSN is never returned by the
   API and is only resolved server-side when a config references it.
   ============================================================ */

import { escapeHtml } from '../util.js';

/* Module-level cache so re-renders reuse the dialect list without refetching. */
let dialects = null;
let dsnExamples = {}; // dialect -> format example, from /api/v1/scenarios

export async function render(root /*Element*/, params) {
    await ensureDialects();
    await ensureDsnExamples();

    root.innerHTML = ''
        + '<div class="page-head reveal" style="--i:0">'
        +   '<div>'
        +     '<div class="overline">prepare · data sources</div>'
        +     '<h1>数据源</h1>'
        +     '<p class="subtitle">保存可复用的数据库连接，配置页一键选择（DSN 加密存储）</p>'
        +   '</div>'
        +   '<div class="panel-actions">'
        +     '<button type="button" class="btn-primary" id="btn-new-ds">新建数据源</button>'
        +   '</div>'
        + '</div>'

        + '<div class="panel reveal" style="--i:1">'
        +   '<div class="panel-head"><span class="panel-title">数据源列表</span></div>'
        +   '<table class="data-table config-lib-table">'
        +     '<thead><tr><th>名称</th><th>类型</th><th>Schema</th><th>备注</th><th>更新时间</th><th>操作</th></tr></thead>'
        +     '<tbody id="ds-list-body"></tbody>'
        +   '</table>'
        +   '<p class="field-help" style="margin-top:12px">'
        +     '提示：DSN 密码在服务端加密存储，列表与详情不显示，配置页选择数据源后由服务端解密，浏览器不接触密钥。'
        +   '</p>'
        + '</div>';

    const btnNew = root.querySelector('#btn-new-ds');
    const tbody = root.querySelector('#ds-list-body');

    btnNew.addEventListener('click', () => dsModal(root, null, refresh));

    async function refresh() {
        try {
            const list = await window.api.get('/api/v1/datasources') || [];
            renderList(tbody, list, root, refresh);
        } catch (e) {
            tbody.innerHTML = '<tr><td colspan="6" class="lib-empty">' + escapeHtml((e && e.message) || '加载失败') + '</td></tr>';
        }
    }

    await refresh();
}

async function ensureDialects() {
    if (dialects) return dialects;
    try {
        dialects = await window.api.get('/api/v1/dialects') || [];
    } catch (e) {
        dialects = [];
    }
    return dialects;
}

/* Per-dialect DSN format examples (mirrors the config page hint). */
async function ensureDsnExamples() {
    if (dsnExamples && Object.keys(dsnExamples).length) return dsnExamples;
    try {
        const data = await window.api.get('/api/v1/scenarios');
        dsnExamples = data.dsn_examples || {};
    } catch (e) {
        dsnExamples = {};
    }
    return dsnExamples;
}

function renderList(tbody, list, root, refresh) {
    tbody.innerHTML = '';
    if (!list.length) {
        tbody.innerHTML = '<tr><td colspan="6" class="lib-empty">暂无数据源，点击右上角「新建数据源」添加</td></tr>';
        return;
    }
    list.forEach(function (ds) {
        const tr = document.createElement('tr');

        const tdName = document.createElement('td');
        tdName.className = 'mono';
        tdName.textContent = ds.name;
        tr.appendChild(tdName);

        const tdType = document.createElement('td');
        tdType.className = 'mono';
        tdType.textContent = ds.type || '-';
        tr.appendChild(tdType);

        const tdSchema = document.createElement('td');
        tdSchema.className = 'mono';
        tdSchema.textContent = ds.schema || '-';
        tr.appendChild(tdSchema);

        const tdRemark = document.createElement('td');
        tdRemark.textContent = ds.remark || '-';
        tr.appendChild(tdRemark);

        const tdUpdated = document.createElement('td');
        tdUpdated.className = 'mono';
        tdUpdated.style.color = 'var(--text-2)';
        tdUpdated.textContent = (ds.updated || '').replace('T', ' ').slice(0, 19);
        tr.appendChild(tdUpdated);

        const tdActions = document.createElement('td');
        tdActions.className = 'lib-actions';
        tdActions.appendChild(dsButton('编辑', 'btn-ghost', function () {
            dsModal(root, ds, refresh);
        }));
        tdActions.appendChild(dsButton('删除', 'btn-danger', function () { delDS(ds.name, refresh); }));
        tr.appendChild(tdActions);

        tbody.appendChild(tr);
    });
}

function dsButton(text, cls, onclick) {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = cls;
    b.textContent = text;
    b.addEventListener('click', onclick);
    return b;
}

async function delDS(name, refresh) {
    if (!window.confirm('确认删除数据源 "' + name + '"？')) return;
    try {
        await window.api.del('/api/v1/datasources/' + encodeURIComponent(name));
        window.toast.ok('已删除：' + name, '');
        refresh();
    } catch (e) {
        window.toast.err('删除失败', e && e.message || '');
    }
}

/* ── create / edit modal (reuses the DSN modal styling) ────── */
function dsModal(root, record, onChange) {
    const overlay = document.createElement('div');
    overlay.className = 'dsn-modal-overlay';
    overlay.innerHTML = ''
        + '<div class="dsn-modal" role="dialog" aria-modal="true">'
        +   '<div class="dsn-modal-head"><h3>' + (record ? '编辑数据源' : '新建数据源') + '</h3>'
        +     '<button type="button" class="btn-ghost dsn-modal-x" aria-label="关闭">×</button></div>'
        +   '<div class="dsn-modal-body">'
        +     '<div class="field"><label>名称 *</label><input name="ds-name" type="text" spellcheck="false" placeholder="例如: prod-oracle"></div>'
        +     '<div class="field"><label>类型 *</label><select name="ds-type"></select></div>'
        +     '<div class="field"><label>Schema</label><input name="ds-schema" type="text" spellcheck="false" placeholder="Oracle: 用户名; MySQL: 库名; PG: schema 名"></div>'
        +     '<div class="field"><label>DSN ' + (record ? '(留空保持不变)' : '*') + '</label>'
        +       '<input name="ds-dsn" type="text" spellcheck="false" autocomplete="off" class="mono">'
        +       '<div class="field-help" id="ds-dsn-example"></div>'
        +       '<div class="field-help">服务端加密保存；列表与详情不回显。</div>'
        +     '</div>'
        +     '<div class="field"><label>备注</label><input name="ds-remark" type="text" spellcheck="false" placeholder="可选"></div>'
        +   '</div>'
        +   '<div class="dsn-modal-actions">'
        +     '<button type="button" class="btn-ghost" id="ds-cancel">取消</button>'
        +     '<button type="button" class="btn-primary" id="ds-save">保存</button>'
        +   '</div>'
        + '</div>';
    root.appendChild(overlay);
    document.body.classList.add('modal-open');
    overlay.classList.add('open');

    const nameEl = overlay.querySelector('[name="ds-name"]');
    const typeEl = overlay.querySelector('[name="ds-type"]');
    const schemaEl = overlay.querySelector('[name="ds-schema"]');
    const dsnEl = overlay.querySelector('[name="ds-dsn"]');
    const remarkEl = overlay.querySelector('[name="ds-remark"]');
    const saveBtn = overlay.querySelector('#ds-save');
    const cancelBtn = overlay.querySelector('#ds-cancel');

    (dialects || []).forEach(function (d) {
        const o = document.createElement('option');
        o.value = d;
        o.textContent = d;
        typeEl.appendChild(o);
    });
    if (record) {
        nameEl.value = record.name; nameEl.disabled = true;
        if (record.type) typeEl.value = record.type;
        schemaEl.value = record.schema || '';
        remarkEl.value = record.remark || '';
    }

    /* Show a per-dialect DSN format example, like the config page. */
    const exampleEl = overlay.querySelector('#ds-dsn-example');
    function updateDsnExample() {
        if (!exampleEl) return;
        const ex = dsnExamples[typeEl.value] || '';
        exampleEl.textContent = ex ? '格式示例：' + ex : '';
        exampleEl.classList.toggle('has-example', !!ex);
    }
    typeEl.addEventListener('change', updateDsnExample);
    updateDsnExample();

    function close() {
        overlay.classList.remove('open');
        document.body.classList.remove('modal-open');
        if (overlay.parentNode) overlay.parentNode.removeChild(overlay);
    }

    cancelBtn.addEventListener('click', close);
    overlay.querySelector('.dsn-modal-x').addEventListener('click', close);
    overlay.addEventListener('click', function (e) { if (e.target === overlay) close(); });
    overlay.addEventListener('keydown', function (e) { if (e.key === 'Escape') close(); });

    saveBtn.addEventListener('click', async function () {
        const name = (nameEl.value || '').trim();
        if (!name) { window.toast.warn('请填写名称', ''); return; }
        if (!typeEl.value) { window.toast.warn('请选择类型', ''); return; }
        if (!dsnEl.value.trim() && !record) { window.toast.warn('请填写 DSN', ''); return; }
        saveBtn.disabled = true;
        try {
            const payload = {
                type: typeEl.value,
                schema: schemaEl.value.trim(),
                dsn: dsnEl.value,
                remark: remarkEl.value.trim()
            };
            if (record) {
                await window.api.put('/api/v1/datasources/' + encodeURIComponent(name), payload);
            } else {
                payload.name = name;
                await window.api.post('/api/v1/datasources', payload);
            }
            window.toast.ok('已保存数据源：' + name, '');
            close();
            onChange();
        } catch (e) {
            window.toast.err('保存失败', e && e.message || '');
        } finally {
            saveBtn.disabled = false;
        }
    });
}
