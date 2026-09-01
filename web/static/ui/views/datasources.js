/* owl-migrate SPA · 数据源 view
   Manages reusable database connection profiles (web-UI only). Each profile
   stores type + schema + an encrypted DSN; the DSN is never returned by the
   API and is only resolved server-side when a config references it.
   ============================================================ */

import { escapeHtml } from '../util.js';

/* Module-level cache so re-renders reuse the dialect list without refetching. */
let dialects = null;

export async function render(root /*Element*/, params) {
    await ensureDialects();

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
        tdActions.appendChild(dsButton('测试连接', 'btn-ghost', function () {
            testConn(ds.type, 'datasource:' + ds.name, ds.schema);
        }));
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

/* Shared "test connection" — POSTs to /api/v1/conn/test. Accepts a plain DSN
   or a "datasource:<name>" ref (the server resolves it, so editing with a
   blank DSN still tests the stored secret). */
async function testConn(type, dsn, schema) {
    if (!type) { window.toast.warn('请选择类型', ''); return false; }
    if (!dsn) { window.toast.warn('请填写 DSN', ''); return false; }
    window.toast.show('正在测试连接…', '', 'info');
    try {
        const resp = await window.api.post('/api/v1/conn/test', { type, dsn, schema: schema || '' });
        if (resp.error) {
            window.toast.err('连接失败', resp.error);
            return false;
        }
        window.toast.ok('连接成功', resp.latency != null ? resp.latency + 'ms' : '');
        return true;
    } catch (e) {
        window.toast.err('连接失败', (e && e.message) || String(e));
        return false;
    }
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

/* ── structured DSN form helpers ──────────────────────────── */
function familyOf(t) {
    t = (t || '').toLowerCase();
    if (t === 'sqlite3' || t === 'duckdb') return 'file';
    if (t === 'oracle' || t === 'goldendb-oracle' || t === 'oceanbase-oracle') return 'oracle';
    if (t === 'mysql' || t === 'goldendb' || t === 'goldendb-mysql' || t === 'oceanbase' || t === 'oceanbase-mysql') return 'mysql';
    return 'postgres';
}

const dsDefaultPort = {
    mysql: '3306', goldendb: '3306', 'goldendb-mysql': '3306',
    'oceanbase': '2881', 'oceanbase-mysql': '2881',
    'oracle': '1521', 'goldendb-oracle': '1521', 'oceanbase-oracle': '2881',
    'postgres': '5432', 'panweidb': '5432', 'panweidb-mysql': '5432',
    'panweidb-oracle': '5432', 'opengaussdb': '5432'
};

function dsDbLabel(t) {
    switch (familyOf(t)) {
        case 'oracle': return '服务名/SID *';
        case 'file':   return '文件路径 *';
        default:       return '数据库名 *';
    }
}

/* Mirrors backend Build (internal/dsnfields) so a test can use a full DSN. */
function buildDSNClient(type, f) {
    const fam = familyOf(type);
    if (fam === 'file') return f.database || '';
    const userinfo = (f.username || '') + (f.password ? ':' + f.password : '');
    let dsn;
    if (fam === 'mysql') {
        dsn = userinfo + '@tcp(' + f.host + ':' + (f.port || '') + ')/' + (f.database || '');
    } else if (fam === 'oracle') {
        const scheme = type === 'oceanbase-oracle' ? 'oceanbase-oracle' : 'oracle';
        dsn = scheme + '://' + userinfo + '@' + f.host + ':' + (f.port || '') + '/' + (f.database || '');
    } else {
        dsn = 'postgres://' + userinfo + '@' + f.host + ':' + (f.port || '') + '/' + (f.database || '');
    }
    if (f.extra) dsn += '?' + f.extra;
    return dsn;
}

/* ── create / edit modal (structured fields, not one raw DSN) ── */
async function dsModal(root, record, onChange) {
    const isEdit = !!record;
    let detail = null;
    if (isEdit) {
        try {
            detail = await window.api.get('/api/v1/datasources/' + encodeURIComponent(record.name));
        } catch (e) { detail = null; }
    }
    const src = detail || record || {};
    const df = (detail && detail.fields) || {};

    const overlay = document.createElement('div');
    overlay.className = 'dsn-modal-overlay';
    overlay.innerHTML = ''
        + '<div class="dsn-modal" role="dialog" aria-modal="true">'
        +   '<div class="dsn-modal-head"><h3>' + (isEdit ? '编辑数据源' : '新建数据源') + '</h3>'
        +     '<button type="button" class="btn-ghost dsn-modal-x" aria-label="关闭">×</button></div>'
        +   '<div class="dsn-modal-body">'
        +     '<div class="field"><label>名称 *</label><input name="ds-name" type="text" spellcheck="false" placeholder="例如: prod-oracle"></div>'
        +     '<div class="field"><label>类型 *</label><select name="ds-type"></select></div>'
        +     '<div class="field"><label>Schema</label><input name="ds-schema" type="text" spellcheck="false" placeholder="Oracle: 用户名; MySQL: 库名; PG: schema 名"></div>'
        +     '<div class="field" data-conn><label>用户名 *</label><input name="ds-username" type="text" spellcheck="false" autocomplete="off"></div>'
        +     '<div class="field" data-conn><label id="ds-password-label">密码 *</label>'
        +       '<input name="ds-password" type="password" spellcheck="false" autocomplete="new-password">'
        +       (isEdit ? '<div class="field-help" id="ds-password-hint"></div>' : '')
        +     '</div>'
        +     '<div class="field" data-conn><label>主机 *</label><input name="ds-host" type="text" spellcheck="false" autocomplete="off"></div>'
        +     '<div class="field" data-conn><label>端口</label><input name="ds-port" type="text" spellcheck="false" autocomplete="off" inputmode="numeric"></div>'
        +     '<div class="field"><label id="ds-db-label">数据库名 *</label><input name="ds-database" type="text" spellcheck="false" autocomplete="off"></div>'
        +     '<div class="field" data-fam="extra"><label>连接参数</label>'
        +       '<input name="ds-extra" type="text" spellcheck="false" autocomplete="off" class="mono" placeholder="可选，如 sslmode=disable / charset=utf8mb4 / cluster=obcluster">'
        +     '</div>'
        +     '<div class="field"><label>备注</label><input name="ds-remark" type="text" spellcheck="false" placeholder="可选"></div>'
        +     '<div class="field-help">密码在服务端加密保存，列表与编辑不回显。</div>'
        +   '</div>'
        +   '<div class="dsn-modal-actions">'
        +     '<button type="button" class="btn-ghost" id="ds-test" style="margin-right:auto">测试连接</button>'
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
    const usernameEl = overlay.querySelector('[name="ds-username"]');
    const passwordEl = overlay.querySelector('[name="ds-password"]');
    const hostEl = overlay.querySelector('[name="ds-host"]');
    const portEl = overlay.querySelector('[name="ds-port"]');
    const databaseEl = overlay.querySelector('[name="ds-database"]');
    const extraEl = overlay.querySelector('[name="ds-extra"]');
    const remarkEl = overlay.querySelector('[name="ds-remark"]');
    const saveBtn = overlay.querySelector('#ds-save');
    const cancelBtn = overlay.querySelector('#ds-cancel');
    const testBtn = overlay.querySelector('#ds-test');
    const passwordLabel = overlay.querySelector('#ds-password-label');
    const passwordHint = overlay.querySelector('#ds-password-hint');

    (dialects || []).forEach(function (d) {
        const o = document.createElement('option');
        o.value = d;
        o.textContent = d;
        typeEl.appendChild(o);
    });

    nameEl.value = src.name || '';
    nameEl.disabled = isEdit;
    if (src.type) typeEl.value = src.type;
    schemaEl.value = src.schema || '';
    usernameEl.value = df.username || '';
    hostEl.value = df.host || '';
    portEl.value = df.port || '';
    databaseEl.value = df.database || '';
    extraEl.value = df.extra || '';
    remarkEl.value = src.remark || '';

    if (isEdit) {
        passwordEl.placeholder = '留空保持不变';
        passwordEl.value = '';
        if (detail && detail.password_set) {
            passwordLabel.textContent = '密码（已设置，留空保持不变）';
            if (passwordHint) passwordHint.textContent = '已设置密码，留空则沿用原密码。';
        } else {
            passwordLabel.textContent = '密码 *';
            if (passwordHint) passwordHint.textContent = '未设置密码，请填写。';
        }
    } else {
        passwordEl.placeholder = '必填';
        passwordLabel.textContent = '密码 *';
    }

    function applyFamily() {
        const fam = familyOf(typeEl.value);
        const isFile = fam === 'file';
        overlay.querySelectorAll('[data-conn]').forEach(function (el) { el.style.display = isFile ? 'none' : ''; });
        const extraWrap = overlay.querySelector('[data-fam="extra"]');
        if (extraWrap) extraWrap.style.display = isFile ? 'none' : '';
        const dbLabel = overlay.querySelector('#ds-db-label');
        if (dbLabel) dbLabel.textContent = dsDbLabel(typeEl.value);
        if (!isEdit && !portEl.value && dsDefaultPort[typeEl.value]) portEl.value = dsDefaultPort[typeEl.value];
    }
    typeEl.addEventListener('change', applyFamily);
    applyFamily();

    function readFields() {
        return {
            username: usernameEl.value.trim(),
            password: passwordEl.value,
            host: hostEl.value.trim(),
            port: portEl.value.trim(),
            database: databaseEl.value.trim(),
            extra: extraEl.value.trim()
        };
    }

    function close() {
        overlay.classList.remove('open');
        document.body.classList.remove('modal-open');
        if (overlay.parentNode) overlay.parentNode.removeChild(overlay);
    }

    cancelBtn.addEventListener('click', close);
    overlay.querySelector('.dsn-modal-x').addEventListener('click', close);
    overlay.addEventListener('click', function (e) { if (e.target === overlay) close(); });
    overlay.addEventListener('keydown', function (e) { if (e.key === 'Escape') close(); });

    testBtn.addEventListener('click', function () {
        if (!typeEl.value) { window.toast.warn('请选择类型', ''); return; }
        const f = readFields();
        if (isEdit && !f.password) {
            // Blank password on edit ⇒ test the stored secret via its ref.
            testConn(typeEl.value, 'datasource:' + src.name, schemaEl.value.trim());
            return;
        }
        const dsn = buildDSNClient(typeEl.value, f);
        if (!dsn) { window.toast.warn('请填写完整的连接信息', ''); return; }
        testConn(typeEl.value, dsn, schemaEl.value.trim());
    });

    saveBtn.addEventListener('click', async function () {
        const name = (nameEl.value || '').trim();
        if (!name) { window.toast.warn('请填写名称', ''); return; }
        if (!typeEl.value) { window.toast.warn('请选择类型', ''); return; }
        const f = readFields();
        if (familyOf(typeEl.value) === 'file') {
            if (!f.database) { window.toast.warn('请填写文件路径', ''); return; }
        } else {
            if (!f.username) { window.toast.warn('请填写用户名', ''); return; }
            if (!f.host) { window.toast.warn('请填写主机', ''); return; }
            if (!f.database) { window.toast.warn(dsDbLabel(typeEl.value).replace(' *', ''), ''); return; }
            if (!isEdit && !f.password) { window.toast.warn('请填写密码', ''); return; }
        }
        saveBtn.disabled = true;
        try {
            const payload = {
                type: typeEl.value,
                schema: schemaEl.value.trim(),
                remark: remarkEl.value.trim(),
                fields: f
            };
            if (isEdit) {
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
