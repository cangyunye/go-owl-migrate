/* owl-migrate SPA · config view (ported from web/templates/config.html +
   web/static/js/config.js) */
/* ============================================================
   Ports: scenario pills + dynamic field form (select/text, DSN
   hints, conditional show/hide), live YAML preview via
   POST /api/v1/scenarios/<name>/build {values,save:false} +
   window.highlightYAML, save via {save:true}, and the config
   library (list / load / delete / upload + file picker).

   SPA-native differences vs the SSR IIFE:
   - Scenario is SPA-internal: module-level `activeScenario`
     persists across re-renders (user pref). Pills call
     selectScenario(name) directly (no history.replaceState, no
     hard navigation). Initial scenario is resolved from a hash
     query (#/config?scenario=x) if present, else the persisted
     activeScenario, else 'migrate'.
   - No window.uploadFile global — the file input change listener
     and upload button are bound inside render().
   - XSS-safe: the config-library table rows are built with DOM
     APIs + textContent (never interpolate server values into
     innerHTML). Form values render through DOM APIs.
   - Mask-safe: loaded/uploaded DSN values containing '*' are not
     prefilled into editable DSN fields.
   ============================================================ */

import { escapeHtml } from '../util.js';

const MASK_RE = /\*/;

/* Module-level state — survives re-renders (user pref). */
let allScenarios = [];
let dsnExamples = {};
let dsnFamilies = {};   // dialect -> family key (mysql/oracle/postgres/oceanbase/file)
let dsnFields = {};     // family key -> {db_label, db_placeholder, port, builder, has_cluster}
let activeScenario = null;
let previewTimer = null;
/* DSN structured-editing modal state (persists across re-renders). */
let dsnModal = null;
let dsnModalCache = {};

function isDSN(name) {
    return name === 'source_dsn' || name === 'target_dsn';
}

function sideOf(name) {
    return isDSN(name) ? (name === 'source_dsn' ? 'source' : 'target') : null;
}

function humanSize(bytes) {
    if (!bytes && bytes !== 0) return '-';
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
    return (bytes / 1024 / 1024).toFixed(1) + ' MB';
}

/* Read a `#/config?scenario=x`-style query from the hash (best-effort). */
function scenarioFromHash() {
    try {
        const q = location.hash.split('?')[1] || '';
        return q ? new URLSearchParams(q).get('scenario') : null;
    } catch (e) { return null; }
}

export async function render(root /*Element*/, params) {
    if (previewTimer) { clearTimeout(previewTimer); previewTimer = null; }

    root.innerHTML = ''
        + '<div class="page-head reveal" style="--i:0">'
        +   '<div>'
        +     '<div class="overline">prepare · config</div>'
        +     '<h1>配置</h1>'
        +     '<p class="subtitle">选择场景，填写表单，实时生成配置</p>'
        +   '</div>'
        +   '<div class="panel-actions">'
        +     '<a class="btn-ghost btn-sm" href="' + window.api.downloadURL('/api/v1/config/download') + '">下载当前配置</a>'
        +   '</div>'
        + '</div>'

        + '<div class="panel reveal" style="--i:1">'
        +   '<div class="panel-head"><span class="panel-title">配置库</span></div>'
        +   '<div class="upload-row">'
        +     '<label class="file-picker" for="cfg-file" id="file-picker">'
        +       '<span class="file-picker-label">选择文件</span>'
        +       '<span class="file-picker-name" id="cfg-file-name">.yaml / .yml 配置文件</span>'
        +     '</label>'
        +     '<input type="file" id="cfg-file" accept=".yaml,.yml" hidden>'
        +     '<button type="button" class="btn-primary" id="btn-upload">上传到配置库</button>'
        +     '<span id="upload-status" class="status-msg"></span>'
        +   '</div>'
        +   '<table class="data-table config-lib-table">'
        +     '<thead><tr><th>名称</th><th>场景</th><th>源 → 目标</th><th>大小</th><th>修改时间</th><th>操作</th></tr></thead>'
        +     '<tbody id="config-lib-body"></tbody>'
        +   '</table>'
        + '</div>'

        + '<nav class="pills reveal" style="--i:2" id="scenario-pills"></nav>'

        + '<div class="cfg-layout">'
        +   '<section class="panel reveal" style="--i:3">'
        +     '<div class="panel-head">'
        +       '<span class="panel-title" id="scenario-label">—</span>'
        +       '<code class="cmd-badge" id="scenario-cmd"></code>'
        +     '</div>'
        +     '<p class="scenario-desc" id="scenario-desc"></p>'
        +     '<form id="cfg-form" autocomplete="off"></form>'
        +     '<div class="form-actions">'
        +       '<button type="button" class="btn-primary" id="btn-save">'
        +         '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15.2 3a2 2 0 0 1 1.4.6l3.8 3.8a2 2 0 0 1 .6 1.4V19a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z"/><path d="M17 21v-7a1 1 0 0 0-1-1H8a1 1 0 0 0-1 1v7"/><path d="M7 3v4a1 1 0 0 0 1 1h7"/></svg>'
        +         '保存为当前配置'
        +       '</button>'
        +       '<span class="save-status" id="save-status"></span>'
        +     '</div>'
        +     '<p class="saved-path" id="saved-path"></p>'
        +   '</section>'

        +   '<aside class="panel cfg-preview-panel reveal" style="--i:4">'
        +     '<div class="panel-head">'
        +       '<span class="panel-title">配置预览</span>'
        +       '<span class="live-dot" id="live-dot"></span>'
        +     '</div>'
        +     '<div class="code-box">'
        +       '<pre class="yaml-view" id="yaml-preview"><span class="yaml-hint">填写左侧表单后，这里实时显示生成的 YAML…</span></pre>'
        +     '</div>'
        +   '</aside>'
        + '</div>';

    /* ── element refs (scoped to this view) ─────────────────── */
    const pillsNav = root.querySelector('#scenario-pills');
    const labelEl = root.querySelector('#scenario-label');
    const cmdEl = root.querySelector('#scenario-cmd');
    const descEl = root.querySelector('#scenario-desc');
    const formEl = root.querySelector('#cfg-form');
    const liveDot = root.querySelector('#live-dot');
    const yamlPre = root.querySelector('#yaml-preview');
    const saveStatus = root.querySelector('#save-status');
    const savedPath = root.querySelector('#saved-path');
    const btnSave = root.querySelector('#btn-save');
    const filePicker = root.querySelector('#file-picker');
    const fileInput = root.querySelector('#cfg-file');
    const filePickerName = root.querySelector('#cfg-file-name');
    const btnUpload = root.querySelector('#btn-upload');
    const uploadStatus = root.querySelector('#upload-status');
    const libBody = root.querySelector('#config-lib-body');

    /* ── scenario pills ─────────────────────────────────────── */
    function renderPills() {
        pillsNav.innerHTML = '';
        allScenarios.forEach(s => {
            const a = document.createElement('a');
            a.className = 'pill';
            a.href = '#/config';
            a.textContent = s.label;
            a.dataset.name = s.name;
            a.addEventListener('click', e => {
                e.preventDefault();
                selectScenario(s.name);
            });
            pillsNav.appendChild(a);
        });
    }

    /* ── scenario selection + dynamic form ──────────────────── */
    function selectScenario(name) {
        const sc = allScenarios.find(s => s.name === name);
        if (!sc) return;
        activeScenario = sc;
        pillsNav.querySelectorAll('.pill').forEach(p =>
            p.classList.toggle('active', p.dataset.name === name));
        labelEl.textContent = sc.label;
        cmdEl.textContent = sc.command || '';
        descEl.textContent = sc.description || '';
        renderForm();
        schedulePreview();
    }

    function renderForm() {
        formEl.innerHTML = '';
        activeScenario.fields.forEach(f => formEl.appendChild(buildField(f)));
        applyConditions();
    }

    function buildField(f) {
        const wrap = document.createElement('div');
        wrap.className = 'field';
        wrap.dataset.name = f.name;
        if (f.show_when) {
            wrap.dataset.condField = f.show_when.field;
            wrap.dataset.condValue = f.show_when.value;
        }

        const label = document.createElement('label');
        label.textContent = f.label + (f.required ? ' *' : '');
        wrap.appendChild(label);

        let input;
        if (f.type === 'select') {
            input = document.createElement('select');
            (f.options || []).forEach(opt => {
                const o = document.createElement('option');
                o.value = opt;
                o.textContent = opt;
                if (opt === f.default) o.selected = true;
                input.appendChild(o);
            });
        } else {
            input = document.createElement('input');
            input.type = 'text';
            if (f.default) input.value = f.default;
            if (isDSN(f.name)) {
                input.classList.add('mono', 'dsn-readonly');
                input.readOnly = true;
                input.addEventListener('click', () => openDSNModal(sideOf(f.name)));
            }
        }
        input.name = f.name;
        input.addEventListener('input', schedulePreview);
        input.addEventListener('change', () => { applyConditions(); refreshDSNHints(); schedulePreview(); });
        wrap.appendChild(input);

        if (isDSN(f.name)) {
            const side = sideOf(f.name);
            const actions = document.createElement('div');
            actions.className = 'field-actions';
            const structBtn = document.createElement('button');
            structBtn.type = 'button';
            structBtn.className = 'btn-ghost btn-sm';
            structBtn.textContent = '结构化填写';
            structBtn.addEventListener('click', () => openDSNModal(side));
            const testBtn = document.createElement('button');
            testBtn.type = 'button';
            testBtn.className = 'btn-ghost btn-sm';
            testBtn.textContent = '测试连接';
            testBtn.addEventListener('click', () => testConn(side));
            const pickBtn = document.createElement('button');
            pickBtn.type = 'button';
            pickBtn.className = 'btn-ghost btn-sm';
            pickBtn.textContent = '从数据源选择';
            pickBtn.addEventListener('click', () => openDataSourcePicker(f.name));
            const saveBtn = document.createElement('button');
            saveBtn.type = 'button';
            saveBtn.className = 'btn-ghost btn-sm';
            saveBtn.textContent = '存为数据源';
            saveBtn.addEventListener('click', () => openSaveAsDataSource(side));
            const status = document.createElement('span');
            status.className = 'conn-status';
            status.dataset.side = side;
            actions.appendChild(structBtn);
            actions.appendChild(testBtn);
            actions.appendChild(pickBtn);
            actions.appendChild(saveBtn);
            actions.appendChild(status);
            wrap.appendChild(actions);
        }

        const help = document.createElement('div');
        help.className = 'field-help';
        help.textContent = f.help || '';
        wrap.appendChild(help);

        if (isDSN(f.name)) updateDSNHint(f, wrap);
        return wrap;
    }

    /* Show a per-dialect DSN format hint under the DSN input, driven by the
       neighbouring type dropdown. */
    function updateDSNHint(f, wrap) {
        const typeName = f.name === 'source_dsn' ? 'source_type' : 'target_type';
        const typeField = activeScenario.fields.find(x => x.name === typeName);
        const helpEl = wrap.querySelector('.field-help');
        if (!typeField || !helpEl) return;
        const typeInput = formEl.querySelector(`[name="${typeName}"]`);
        const dialect = typeInput ? typeInput.value : '';
        const example = dsnExamples[dialect];
        helpEl.textContent = example ? '格式示例：' + example : (f.help || '');
        helpEl.classList.toggle('has-example', !!example);
    }

    function refreshDSNHints() {
        activeScenario.fields.forEach(f => {
            if (!isDSN(f.name)) return;
            const wrap = formEl.querySelector(`.field[data-name="${f.name}"]`);
            if (wrap) updateDSNHint(f, wrap);
        });
    }

    function applyConditions() {
        formEl.querySelectorAll('.field[data-cond-field]').forEach(wrap => {
            const ctl = formEl.querySelector(`[name="${wrap.dataset.condField}"]`);
            const visible = ctl && ctl.value === wrap.dataset.condValue;
            wrap.classList.toggle('hidden', !visible);
        });
    }

    /* ── data-source picker (web-only: reusable connection profiles) ────── */
    async function openDataSourcePicker(dsnFieldName) {
        const side = dsnFieldName === 'source_dsn' ? 'source' : 'target';
        const typeName = side + '_type';
        const schemaName = side + '_schema';

        let list;
        try {
            list = await window.api.get('/api/v1/datasources') || [];
        } catch (e) {
            window.toast.warn('数据源加载失败', (e && e.message) || String(e));
            return;
        }
        if (!list.length) {
            window.toast.warn('暂无数据源', '请先在「数据源」页新建一个');
            return;
        }

        const overlay = document.createElement('div');
        overlay.className = 'dsn-modal-overlay';
        overlay.innerHTML = ''
            + '<div class="dsn-modal" role="dialog" aria-modal="true">'
            +   '<div class="dsn-modal-head"><h3>选择数据源</h3>'
            +     '<button type="button" class="btn-ghost dsn-modal-x" aria-label="关闭">×</button></div>'
            +   '<div class="dsn-modal-body">'
            +     '<div class="field"><label>数据源</label><select name="ds-pick" class="mono"></select>'
            +       '<div class="field-help">选中后自动填充类型、schema，DSN 由服务端加密解析。</div></div>'
            +     '<p class="field-note">数据源列表与详情不回显 DSN；密码仅在服务端解密。</p>'
            +   '</div>'
            +   '<div class="dsn-modal-actions">'
            +     '<button type="button" class="btn-ghost" id="ds-pick-cancel">取消</button>'
            +     '<button type="button" class="btn-primary" id="ds-pick-apply">应用</button>'
            +   '</div>'
            + '</div>';
        document.body.appendChild(overlay);
        document.body.classList.add('modal-open');
        overlay.classList.add('open');

        const sel = overlay.querySelector('[name="ds-pick"]');
        list.forEach(ds => {
            const o = document.createElement('option');
            o.value = ds.name;
            o.textContent = ds.name + ' · ' + (ds.type || '?') + (ds.schema ? ' · ' + ds.schema : '');
            sel.appendChild(o);
        });

        function close() {
            overlay.classList.remove('open');
            document.body.classList.remove('modal-open');
            if (overlay.parentNode) overlay.parentNode.removeChild(overlay);
        }
        overlay.querySelector('.dsn-modal-x').addEventListener('click', close);
        overlay.querySelector('#ds-pick-cancel').addEventListener('click', close);
        overlay.addEventListener('click', e => { if (e.target === overlay) close(); });
        overlay.addEventListener('keydown', e => { if (e.key === 'Escape') close(); });

        overlay.querySelector('#ds-pick-apply').addEventListener('click', async () => {
            const name = sel.value;
            if (!name) { window.toast.warn('请选择数据源', ''); return; }
            try {
                const resp = await window.api.post('/api/v1/datasources/' + encodeURIComponent(name) + '/pick', {});
                const typeInput = formEl.querySelector(`[name="${typeName}"]`);
                const schemaInput = formEl.querySelector(`[name="${schemaName}"]`);
                const dsnInput = formEl.querySelector(`[name="${dsnFieldName}"]`);
                if (typeInput && resp.type) typeInput.value = resp.type;
                if (schemaInput && resp.schema) schemaInput.value = resp.schema;
                if (dsnInput) dsnInput.value = resp.ref || ('datasource:' + name);
                applyConditions();
                refreshDSNHints();
                schedulePreview();
                window.toast.ok('已选用数据源：' + name, '');
                close();
            } catch (e) {
                window.toast.err('应用数据源失败', (e && e.message) || String(e));
            }
        });
    }

    /* ── save the current side's connection as a reusable data source ────── */
    function openSaveAsDataSource(side) {
        const typeInput = formEl.querySelector(`[name="${side}_type"]`);
        const dsnInput = formEl.querySelector(`[name="${side}_dsn"]`);
        const schemaInput = formEl.querySelector(`[name="${side}_schema"]`);
        const type = typeInput ? typeInput.value : '';
        const dsn = dsnInput ? dsnInput.value : '';
        const schema = schemaInput ? schemaInput.value : '';
        if (!dsn) { window.toast.warn('请先填写 DSN', ''); return; }
        if (dsn.indexOf('datasource:') === 0) {
            window.toast.warn('当前 DSN 来自数据源', '无需重复保存，改名请直接在「数据源」页操作');
            return;
        }

        const overlay = document.createElement('div');
        overlay.className = 'dsn-modal-overlay';
        overlay.innerHTML = ''
            + '<div class="dsn-modal" role="dialog" aria-modal="true">'
            +   '<div class="dsn-modal-head"><h3>保存为数据源</h3>'
            +     '<button type="button" class="btn-ghost dsn-modal-x" aria-label="关闭">×</button></div>'
            +   '<div class="dsn-modal-body">'
            +     '<div class="field"><label>名称 *</label><input name="ds-name" type="text" spellcheck="false" autocomplete="off" placeholder="例如: prod-oracle"></div>'
            +     '<div class="field"><label>类型</label><input readonly type="text" class="mono" value="' + escapeHtml(type) + '"></div>'
            +     '<div class="field"><label>Schema</label><input name="ds-schema" type="text" spellcheck="false" value="' + escapeHtml(schema) + '"></div>'
            +     '<div class="field"><label>DSN</label><input readonly type="text" class="mono" value="' + escapeHtml(dsn) + '"></div>'
            +     '<p class="field-note">将保存为可复用的数据源（DSN 服务端加密存储）。</p>'
            +   '</div>'
            +   '<div class="dsn-modal-actions">'
            +     '<button type="button" class="btn-ghost" id="ds-saveas-cancel">取消</button>'
            +     '<button type="button" class="btn-primary" id="ds-saveas-save">保存</button>'
            +   '</div>'
            + '</div>';
        document.body.appendChild(overlay);
        document.body.classList.add('modal-open');
        overlay.classList.add('open');

        const nameEl = overlay.querySelector('[name="ds-name"]');
        const schemaEl = overlay.querySelector('[name="ds-schema"]');

        function close() {
            overlay.classList.remove('open');
            document.body.classList.remove('modal-open');
            if (overlay.parentNode) overlay.parentNode.removeChild(overlay);
        }
        overlay.querySelector('.dsn-modal-x').addEventListener('click', close);
        overlay.querySelector('#ds-saveas-cancel').addEventListener('click', close);
        overlay.addEventListener('click', e => { if (e.target === overlay) close(); });
        overlay.addEventListener('keydown', e => { if (e.key === 'Escape') close(); });

        overlay.querySelector('#ds-saveas-save').addEventListener('click', async () => {
            const name = (nameEl.value || '').trim();
            if (!name) { window.toast.warn('请填写名称', ''); return; }
            const save = overlay.querySelector('#ds-saveas-save');
            save.disabled = true;
            try {
                await window.api.post('/api/v1/datasources', {
                    name, type, schema: schemaEl.value.trim(), dsn, remark: ''
                });
                window.toast.ok('已保存数据源：' + name, '');
                close();
            } catch (e) {
                window.toast.err('保存失败', (e && e.message) || String(e));
            } finally {
                save.disabled = false;
            }
        });
    }

    /* ── DSN structured-editing modal (ported from SSR config.js) ───────── */
    function openDSNModal(side) {
        ensureDSNModal();
        const typeInput = formEl.querySelector(`[name="${side}_type"]`);
        const dialect = typeInput ? typeInput.value : '';
        const family = dialect ? (dsnFamilies[dialect] || '') : '';
        const dsnInput = formEl.querySelector(`[name="${side}_dsn"]`);
        const current = dsnInput ? dsnInput.value : '';
        dsnModal.data = { side, family, currentDsn: current };

        const meta = dsnFields[family] || {};
        const sideLabel = side === 'source' ? '源' : '目标';
        dsnModal.overlay.querySelector('#dsn-modal-title').textContent = '编辑' + sideLabel + '数据库 DSN';

        renderDSNModalBody(dsnModal.body, family, meta);
        const parsed = parseDSN(current, family);
        const hasAny = parsed && Object.values(parsed).some(v => v !== undefined && v !== '');
        prefillDSNModal(hasAny ? parsed : (dsnModalCache[side] || {}), family);
        const ta = dsnModal.body.querySelector('[name="modal_dsn_raw"]');
        if (ta) ta.value = current;
        dsnModal.raw.textContent = current || '等待填写…';
        dsnModal.overlay.classList.add('open');
        document.body.classList.add('modal-open');
        const first = dsnModal.body.querySelector('input');
        if (first) first.focus();
    }

    function ensureDSNModal() {
        if (dsnModal) return dsnModal;
        const overlay = document.createElement('div');
        overlay.className = 'dsn-modal-overlay';
        overlay.innerHTML = ''
            + '<div class="dsn-modal" role="dialog" aria-modal="true">'
            + '<div class="dsn-modal-head"><h3 id="dsn-modal-title"></h3>'
            + '<button type="button" class="btn-ghost dsn-modal-x" aria-label="关闭">×</button></div>'
            + '<div class="dsn-modal-body" id="dsn-modal-body"></div>'
            + '<div class="dsn-modal-preview"><span>生成中的 DSN</span><pre id="dsn-modal-raw" class="mono"></pre></div>'
            + '<div class="dsn-modal-actions">'
            + '<button type="button" class="btn-ghost" id="dsn-modal-cancel">取消</button>'
            + '<button type="button" class="btn-primary" id="dsn-modal-apply">应用</button>'
            + '</div></div>';
        document.body.appendChild(overlay);
        overlay.addEventListener('click', e => { if (e.target === overlay) revertDSNModal(); });
        overlay.querySelector('.dsn-modal-x').addEventListener('click', revertDSNModal);
        overlay.querySelector('#dsn-modal-cancel').addEventListener('click', revertDSNModal);
        overlay.querySelector('#dsn-modal-apply').addEventListener('click', applyDSNModal);
        document.addEventListener('keydown', e => { if (e.key === 'Escape' && dsnModal && dsnModal.overlay.classList.contains('open')) revertDSNModal(); });
        dsnModal = {
            overlay,
            body: overlay.querySelector('#dsn-modal-body'),
            raw: overlay.querySelector('#dsn-modal-raw'),
            data: null,
        };
        return dsnModal;
    }

    function renderDSNModalBody(body, family, meta) {
        body.innerHTML = '';
        if (family === 'file') {
            body.appendChild(dsnModalField('path', '数据库文件路径', meta.db_placeholder || '例如: /path/to/xxx.db', 'text'));
            return;
        }
        const fields = [
            ['host', '主机', '例如: 127.0.0.1', 'text'],
            ['port', '端口', '默认 ' + (meta.port || ''), 'text'],
            ['user', '用户', '登录用户名', 'text'],
        ];
        if (meta.has_tenant) {
            fields.push(['tenant', '租户', 'OceanBase 租户（可选）', 'text']);
            fields.push(['cluster', '集群', 'OBProxy(2883) 多集群必填；直连 OBServer(2881) 无需', 'text']);
        }
        fields.push(['password', '密码', '数据库登录密码', 'password']);
        fields.push(['db', meta.db_label || '数据库', meta.db_placeholder || '', 'text']);
        if (!meta.has_tenant && meta.has_cluster) {
            fields.push(['cluster', '集群', '例如: obcluster（可选）', 'text']);
        }
        fields.forEach(([key, label, ph, type]) => body.appendChild(dsnModalField(key, label, ph, type)));
        const rawWrap = document.createElement('div');
        rawWrap.className = 'field';
        rawWrap.dataset.key = 'raw';
        const rl = document.createElement('label');
        rl.textContent = '原始 DSN（可直接编辑）';
        rawWrap.appendChild(rl);
        const ta = document.createElement('textarea');
        ta.className = 'mono dsn-raw-input';
        ta.name = 'modal_dsn_raw';
        ta.addEventListener('input', onDSNModalRawInput);
        rawWrap.appendChild(ta);
        body.appendChild(rawWrap);
    }

    function dsnModalField(key, label, ph, type) {
        const wrap = document.createElement('div');
        wrap.className = 'field';
        wrap.dataset.key = key;
        const l = document.createElement('label');
        l.textContent = label;
        wrap.appendChild(l);
        const input = document.createElement('input');
        input.type = type;
        input.name = 'modal_dsn_' + key;
        input.placeholder = ph;
        if (key === 'host' || key === 'port' || key === 'db' || key === 'path') input.classList.add('mono');
        input.addEventListener('input', onDSNModalInput);
        wrap.appendChild(input);
        return wrap;
    }

    function mval(key) {
        const el = dsnModal && dsnModal.body ? dsnModal.body.querySelector(`[data-key="${key}"] input`) : null;
        return el ? (el.value || '') : '';
    }

    function assembleDSNModal() {
        const meta = dsnFields[dsnModal.data.family] || {};
        if (dsnModal.data.family === 'file') return mval('path');
        if (!meta.builder) return '';
        const PH = { user: '$user', password: '$pass', host: '$host', port: '$port' };
        const OPTIONAL = { db: true, tenant: true, cluster: true };
        let out = meta.builder.replace(/\{(\w+)\}/g, (m, k) => {
            let v;
            if (k === 'user' && meta.has_tenant) {
                let u = mval('user');
                if (!u) { v = PH.user; }
                else if (u.indexOf('@') >= 0 || u.indexOf('#') >= 0) {
                    // User already contains a full user@tenant#cluster (or a
                    // partial user@tenant / user#cluster) — treat it verbatim
                    // instead of re-appending tenant/cluster, which would
                    // double them up (e.g. gzmonitor@sit#ngbossjf@sit#ngbossjf).
                    v = u;
                }
                else {
                    const t = mval('tenant');
                    const c = mval('cluster');
                    if (t) u += '@' + t;
                    if (c) u += '#' + c;
                    v = u;
                }
            } else {
                v = mval(k);
                if (!v) v = OPTIONAL[k] ? '' : (PH[k] || '');
            }
            const isToken = /^\$[A-Za-z]+$/.test(v);
            // go-sql-driver parses `user:pass@tcp(host:port)/db` verbatim (no
            // percent-decoding), so URL-encoding @ etc. in user/password/db
            // breaks authentication. Only scheme:// DSNs (oracle://, …)
            // percent-encode their userinfo.
            const urlEncode = meta.url_style && meta.builder && meta.builder.indexOf('@tcp(') < 0;
            if (v && urlEncode && !isToken && (k === 'user' || k === 'password' || k === 'db')) v = encodeURIComponent(v);
            return v;
        });
        if (dsnModal.data.family === 'postgres') {
            out = out.split(/\s+/).filter(t => t && !/^\w+=$/.test(t)).join(' ');
        }
        out = out.replace(/\s+/g, ' ').trim();
        if (meta.has_cluster && !meta.has_tenant) {
            const c = mval('cluster');
            if (c) out += (out.indexOf('?') >= 0 ? '&' : '?') + 'cluster=' + encodeURIComponent(c);
        }
        return out;
    }

    function onDSNModalInput() {
        saveDSNModalCache();
        const assembled = assembleDSNModal();
        const ta = dsnModal.body.querySelector('[name="modal_dsn_raw"]');
        if (ta) ta.value = assembled || dsnModal.data.currentDsn;
        dsnModal.raw.textContent = assembled || dsnModal.data.currentDsn || '等待填写…';
        const dsnInput = formEl.querySelector(`[name="${dsnModal.data.side}_dsn"]`);
        if (assembled && dsnInput) { dsnInput.value = assembled; schedulePreview(); }
    }

    function saveDSNModalCache() {
        if (!dsnModal || !dsnModal.data) return;
        dsnModalCache[dsnModal.data.side] = {
            host: mval('host'), port: mval('port'), user: mval('user'),
            password: mval('password'), db: mval('db'),
            tenant: mval('tenant'), cluster: mval('cluster'), path: mval('path'),
        };
    }

    function onDSNModalRawInput() {
        if (dsnModal && dsnModal.data) dsnModalCache[dsnModal.data.side] = { raw: dsnModal.body.querySelector('[name="modal_dsn_raw"]').value };
        const ta = dsnModal.body.querySelector('[name="modal_dsn_raw"]');
        const v = ta ? ta.value : '';
        dsnModal.raw.textContent = v || '等待填写…';
        const dsnInput = formEl.querySelector(`[name="${dsnModal.data.side}_dsn"]`);
        if (v && dsnInput) { dsnInput.value = v; schedulePreview(); }
    }

    function revertDSNModal() {
        if (!dsnModal || !dsnModal.data) return;
        const dsnInput = formEl.querySelector(`[name="${dsnModal.data.side}_dsn"]`);
        if (dsnInput) { dsnInput.value = dsnModal.data.currentDsn; schedulePreview(); }
        closeDSNModal();
    }

    function applyDSNModal() {
        if (!dsnModal || !dsnModal.data) return;
        const assembled = assembleDSNModal();
        const dsnInput = formEl.querySelector(`[name="${dsnModal.data.side}_dsn"]`);
        if (assembled && dsnInput) { dsnInput.value = assembled; schedulePreview(); }
        closeDSNModal();
    }

    function closeDSNModal() {
        if (!dsnModal) return;
        dsnModal.overlay.classList.remove('open');
        document.body.classList.remove('modal-open');
    }

    function parseDSN(dsn, family) {
        dsn = (dsn || '').trim();
        const o = {};
        if (!dsn) return o;
        if (family === 'file') { o.path = dsn; return o; }
        if (family === 'postgres' && !/:\/\//.test(dsn)) {
            dsn.split(/\s+/).forEach(t => {
                const i = t.indexOf('=');
                if (i > 0) {
                    const k = t.slice(0, i).toLowerCase();
                    const v = t.slice(i + 1);
                    if (k === 'dbname') o.db = v; else o[k] = v;
                }
            });
            return o;
        }
        const m = dsn.match(/^(.*)@tcp\(([^)]+)\)(.*)$/);
        if (m) {
            const auth = m[1];
            const hostport = m[2];
            let rest = m[3];
            const colon = auth.indexOf(':');
            let user = colon >= 0 ? auth.slice(0, colon) : auth;
            o.password = colon >= 0 ? auth.slice(colon + 1) : '';
            if (family === 'oceanbase-mysql') {
                const h = user.indexOf('#');
                const at = user.indexOf('@');
                if (h >= 0) { o.cluster = user.slice(h + 1); }
                const base = h >= 0 ? user.slice(0, h) : user;
                if (at >= 0) { o.tenant = base.slice(at + 1); o.user = base.slice(0, at); }
                else { o.user = base; }
            } else {
                o.user = user;
            }
            if (rest.indexOf('?') >= 0) {
                const qi = rest.indexOf('?');
                const params = new URLSearchParams(rest.slice(qi + 1));
                rest = rest.slice(0, qi);
                if (params.get('cluster')) o.cluster = params.get('cluster');
            }
            const hp = hostport.split(':');
            o.host = hp[0];
            o.port = hp[1] || '';
            o.db = rest.replace(/^\//, '');
            try { o.user = decodeURIComponent(o.user); } catch (e) {}
            try { o.password = decodeURIComponent(o.password); } catch (e) {}
            return o;
        }
        let u;
        try { u = new URL(dsn); } catch (e) { return o; }
        if (/^(oracle|oboracle|oceanbase-oracle|postgres|postgresql):/.test(u.protocol)) {
            o.user = u.username || '';
            o.password = u.password || '';
            // WHATWG URL returns the userinfo percent-encoded; decode so
            // user@tenant#cluster splitting below sees raw characters.
            try { o.user = decodeURIComponent(o.user); } catch (e) {}
            try { o.password = decodeURIComponent(o.password); } catch (e) {}
            o.host = u.hostname || '';
            o.port = u.port || '';
            o.db = (u.pathname || '').replace(/^\//, '');
            if (u.searchParams.get('cluster')) o.cluster = u.searchParams.get('cluster');
            if (family === 'oceanbase-oracle') {
                // obconnector-go format: user@tenant#cluster inside the URL userinfo.
                const h = o.user.indexOf('#');
                const at = o.user.indexOf('@');
                if (h >= 0) { o.cluster = o.cluster || o.user.slice(h + 1); o.user = o.user.slice(0, h); }
                if (at >= 0) { o.tenant = o.user.slice(at + 1); o.user = o.user.slice(0, at); }
            }
        }
        return o;
    }

    function prefillDSNModal(o, family) {
        const set = (key, v) => {
            if (!v) return;
            const el = dsnModal.body.querySelector(`[data-key="${key}"] input`);
            if (el) el.value = v;
        };
        if (family === 'file') { set('path', o.path || o.db || ''); return; }
        set('host', o.host); set('port', o.port);
        set('user', o.user); set('password', o.password);
        set('tenant', o.tenant);
        set('db', o.db); set('cluster', o.cluster);
    }

    /* ── connection test (ported from SSR config.js) ────────────────────── */
    async function testConn(side) {
        const status = formEl.querySelector(`.conn-status[data-side="${side}"]`);
        const setStatus = (msg, cls) => {
            if (!status) return;
            status.textContent = msg;
            status.className = 'conn-status ' + cls;
            setTimeout(() => { status.textContent = ''; }, 6000);
        };
        const typeInput = formEl.querySelector(`[name="${side}_type"]`);
        const dsnInput = formEl.querySelector(`[name="${side}_dsn"]`);
        const schemaInput = formEl.querySelector(`[name="${side}_schema"]`);
        const dsn = dsnInput ? dsnInput.value : '';
        if (!dsn) { setStatus('请先填写 DSN', 'fail'); return; }
        setStatus('连接中…', 'pending');
        try {
            const resp = await window.api.post('/api/v1/conn/test', {
                type: typeInput ? typeInput.value : '',
                dsn,
                schema: schemaInput ? schemaInput.value : '',
            });
            if (resp.error) {
                setStatus('✗ ' + resp.error, 'fail');
            } else {
                setStatus('✓ 连接成功 ' + (resp.latency != null ? resp.latency + 'ms' : ''), 'ok');
                enableSchemaSelect(side, resp.schemas || []);
            }
        } catch (e) {
            setStatus('✗ ' + (e.message || String(e)), 'fail');
        }
    }

    function enableSchemaSelect(side, schemas) {
        if (!Array.isArray(schemas) || !schemas.length) return;
        const wrap = formEl.querySelector(`.field[data-name="${side}_schema"]`);
        if (!wrap) return;
        let select = wrap.querySelector(`select[name="${side}_schema"]`);
        let input = wrap.querySelector(`input[name="${side}_schema"]`);
        const current = select ? select.value : (input ? input.value : '');
        if (!select) {
            select = document.createElement('select');
            select.name = side + '_schema';
            select.addEventListener('change', schedulePreview);
            if (input) input.replaceWith(select);
            else wrap.appendChild(select);
        }
        select.innerHTML = '';
        if (current === '') {
            const ph = document.createElement('option');
            ph.value = '';
            ph.selected = true;
            ph.textContent = '— 请选择 schema —';
            select.appendChild(ph);
        }
        schemas.forEach(s => {
            const o = document.createElement('option');
            o.value = s;
            o.textContent = s;
            if (s === current) o.selected = true;
            select.appendChild(o);
        });
        let refresh = wrap.querySelector('.schema-refresh');
        if (!refresh) {
            refresh = document.createElement('button');
            refresh.type = 'button';
            refresh.className = 'btn-ghost btn-sm schema-refresh';
            refresh.textContent = '刷新';
            refresh.addEventListener('click', () => testConn(side));
            wrap.appendChild(refresh);
        }
    }

    function collectValues() {
        const values = {};
        formEl.querySelectorAll('.field:not(.hidden) [name]').forEach(el => {
            values[el.name] = el.value;
        });
        return values;
    }

    /* ── live preview ───────────────────────────────────────── */
    function schedulePreview() {
        if (previewTimer) clearTimeout(previewTimer);
        liveDot.classList.add('busy');
        previewTimer = setTimeout(renderPreview, 350);
    }

    async function renderPreview() {
        liveDot.classList.add('busy');
        try {
            const resp = await window.api.post(`/api/v1/scenarios/${activeScenario.name}/build`,
                { values: collectValues(), save: false });
            if (root.isConnected) yamlPre.innerHTML = window.highlightYAML(resp.yaml || '');
        } catch (e) {
            if (root.isConnected) yamlPre.innerHTML = '<span class="yaml-err">' + escapeHtml((e && e.message) || String(e)) + '</span>';
        } finally {
            if (root.isConnected) liveDot.classList.remove('busy');
        }
    }

    /* ── save as current config ─────────────────────────────── */
    async function saveConfig() {
        saveStatus.textContent = '保存中…'; saveStatus.className = 'save-status';
        try {
            const resp = await window.api.post(`/api/v1/scenarios/${activeScenario.name}/build`,
                { values: collectValues(), save: true });
            saveStatus.textContent = '✓ 已保存为当前配置';
            saveStatus.className = 'save-status ok';
            savedPath.textContent = (resp && resp.path) ? '已写入文件：' + resp.path : '';
            if (window.refreshConfigBar) window.refreshConfigBar();
        } catch (e) {
            saveStatus.textContent = '✗ ' + ((e && e.message) || String(e));
            saveStatus.className = 'save-status fail';
        }
        setTimeout(() => { if (root.isConnected) saveStatus.textContent = ''; }, 3000);
    }

    /* ── form value helpers (mask-safe DSN prefill) ──────────── */
    function applyFormValues(values) {
        values = values || {};
        formEl.querySelectorAll('[name]').forEach(el => {
            const val = values[el.name];
            if (val === undefined || val === '') return;
            if (isDSN(el.name) && MASK_RE.test(val)) return;
            el.value = val;
        });
        applyConditions();
        refreshDSNHints();
    }

    function applyConfigResp(resp) {
        if (resp && resp.scenario && allScenarios.some(s => s.name === resp.scenario)) {
            selectScenario(resp.scenario);
        }
        applyFormValues((resp && resp.values) || {});
        if (previewTimer) clearTimeout(previewTimer);
        liveDot.classList.remove('busy');
        if (root.isConnected) yamlPre.innerHTML = window.highlightYAML((resp && resp.yaml) || '');
    }

    /* ── config library ─────────────────────────────────────── */
    async function loadConfigLib() {
        try {
            const list = await window.api.get('/api/v1/configs') || [];
            if (root.isConnected) renderConfigLib(list);
        } catch (e) {
            if (root.isConnected) {
                libBody.innerHTML = '<tr><td colspan="6" class="lib-empty">' + escapeHtml((e && e.message) || '加载失败') + '</td></tr>';
            }
        }
    }

    function renderConfigLib(list) {
        libBody.innerHTML = '';
        if (!list.length) {
            libBody.innerHTML = '<tr><td colspan="6" class="lib-empty">暂无保存的配置，上传后即可复用</td></tr>';
            return;
        }
        list.forEach(c => {
            const tr = document.createElement('tr');
            const flow = [c.source_type, c.target_type].filter(Boolean).join(' → ') || '-';

            const tdName = document.createElement('td');
            tdName.className = 'mono';
            tdName.textContent = c.name;
            tr.appendChild(tdName);

            const tdScenario = document.createElement('td');
            tdScenario.textContent = c.scenario || '-';
            tr.appendChild(tdScenario);

            const tdFlow = document.createElement('td');
            tdFlow.className = 'mono';
            tdFlow.textContent = flow;
            tr.appendChild(tdFlow);

            const tdSize = document.createElement('td');
            tdSize.textContent = humanSize(c.size);
            tr.appendChild(tdSize);

            const tdModified = document.createElement('td');
            tdModified.textContent = (c.modified || '').replace('T', ' ').slice(0, 19);
            tr.appendChild(tdModified);

            const tdActions = document.createElement('td');
            tdActions.className = 'lib-actions';
            tdActions.appendChild(libButton('加载', 'btn-primary', () => loadConfig(c.name)));
            const dl = document.createElement('a');
            dl.className = 'btn-ghost';
            dl.textContent = '下载';
            dl.href = '/api/v1/configs/' + encodeURIComponent(c.name);
            tdActions.appendChild(dl);
            tdActions.appendChild(libButton('删除', 'btn-danger', () => deleteConfig(c.name)));
            tr.appendChild(tdActions);

            libBody.appendChild(tr);
        });
    }

    function libButton(text, cls, onclick) {
        const b = document.createElement('button');
        b.type = 'button';
        b.className = cls;
        b.textContent = text;
        b.addEventListener('click', onclick);
        return b;
    }

    function setUploadStatus(msg, kind) {
        uploadStatus.textContent = msg;
        uploadStatus.className = 'status-msg' + (kind ? ' ' + kind : '');
    }

    async function loadConfig(name) {
        try {
            const resp = await window.api.post('/api/v1/configs/' + encodeURIComponent(name) + '/load', {});
            applyConfigResp(resp);
            if (window.refreshConfigBar) window.refreshConfigBar();
            setUploadStatus('✓ 已加载配置：' + name, 'ok');
        } catch (e) {
            setUploadStatus('✗ ' + ((e && e.message) || String(e)), 'fail');
        }
    }

    async function deleteConfig(name) {
        if (!window.confirm('确认删除配置 "' + name + '"？')) return;
        try {
            await window.api.del('/api/v1/configs/' + encodeURIComponent(name));
            setUploadStatus('✓ 已删除：' + name, 'ok');
            loadConfigLib();
        } catch (e) {
            setUploadStatus('✗ ' + ((e && e.message) || String(e)), 'fail');
        }
    }

    /* ── upload (no window.uploadFile global) ────────────────── */
    async function uploadFile() {
        if (!fileInput.files.length) {
            setUploadStatus('✗ 请先选择文件', 'fail');
            return;
        }
        const file = fileInput.files[0];
        let text;
        try { text = await file.text(); } catch (e) {
            setUploadStatus('✗ 读取文件失败', 'fail');
            return;
        }
        btnUpload.disabled = true;
        try {
            const resp = await window.api.post('/api/v1/configs', { name: file.name, yaml: text });
            applyConfigResp(resp);
            if (window.refreshConfigBar) window.refreshConfigBar();
            loadConfigLib();
            setUploadStatus('✓ 已存入配置库并填入表单：' + ((resp && resp.name) || file.name), 'ok');
            clearFilePicker();
        } catch (e) {
            setUploadStatus('✗ ' + ((e && e.message) || String(e)), 'fail');
        } finally {
            btnUpload.disabled = false;
        }
    }

    function clearFilePicker() {
        fileInput.value = '';
        filePicker.classList.remove('has-file');
        filePickerName.textContent = '.yaml / .yml 配置文件';
    }

    /* ── wire events ────────────────────────────────────────── */
    btnSave.addEventListener('click', saveConfig);
    btnUpload.addEventListener('click', uploadFile);
    fileInput.addEventListener('change', function () {
        if (this.files.length) {
            filePicker.classList.add('has-file');
            filePickerName.textContent = this.files[0].name;
        } else {
            clearFilePicker();
        }
    });

    /* ── load scenarios, choose initial, render lib ──────────── */
    try {
        const data = await window.api.get('/api/v1/scenarios');
        if (!root.isConnected) return;
        allScenarios = data.scenarios || [];
        dsnExamples = data.dsn_examples || {};
        dsnFamilies = data.dsn_families || {};
        dsnFields = data.dsn_fields || {};
        renderPills();
        selectScenario(resolveInitialScenario());
    } catch (e) {
        if (root.isConnected) {
            const msg = ((e && e.message) || String(e)) === 'unauthorized'
                ? '服务已启用令牌鉴权，请输入访问令牌后重试。'
                : ((e && e.message) || String(e));
            labelEl.textContent = '加载场景失败';
            descEl.textContent = msg;
        }
        return;
    }
    loadConfigLib();

    /* restore the server's current config into the form */
    try {
        const cur = await window.api.get('/api/v1/config/current');
        if (root.isConnected && cur && !cur.empty) applyConfigResp(cur);
    } catch (e) {}

    function resolveInitialScenario() {
        const fromHash = scenarioFromHash();
        if (fromHash && allScenarios.some(s => s.name === fromHash)) return fromHash;
        if (activeScenario && allScenarios.some(s => s.name === activeScenario.name)) return activeScenario.name;
        return 'migrate';
    }
}
