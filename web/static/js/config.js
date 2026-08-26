(function () {
    'use strict';

    let allScenarios = [];
    let dsnExamples = {};
    let dsnFamilies = {};   // dialect -> family key (mysql/oracle/postgres/oceanbase/file)
    let dsnFields = {};     // family key -> {db_label, db_placeholder, port, builder, has_cluster}
    let activeScenario = null;
    let previewTimer = null;

    const qs = new URLSearchParams(location.search);
    const initialScenario = qs.get('scenario') || 'migrate';

    init();

    async function init() {
        const data = await api.get('/api/v1/scenarios');
        allScenarios = data.scenarios;
        dsnExamples = data.dsn_examples || {};
        dsnFamilies = data.dsn_families || {};
        dsnFields = data.dsn_fields || {};
        renderPills();
        selectScenario(allScenarios.some(s => s.name === initialScenario) ? initialScenario : allScenarios[0].name);
        loadConfigLib();
    }

    function renderPills() {
        const nav = document.getElementById('scenario-pills');
        nav.innerHTML = '';
        allScenarios.forEach(s => {
            const a = document.createElement('a');
            a.className = 'pill';
            a.href = '/config?scenario=' + s.name;
            a.textContent = s.label;
            a.dataset.name = s.name;
            a.addEventListener('click', e => {
                e.preventDefault();
                history.replaceState(null, '', '/config?scenario=' + s.name);
                selectScenario(s.name);
            });
            nav.appendChild(a);
        });
    }

    function selectScenario(name) {
        activeScenario = allScenarios.find(s => s.name === name);
        document.querySelectorAll('.pill').forEach(p =>
            p.classList.toggle('active', p.dataset.name === name));
        document.getElementById('scenario-label').textContent = activeScenario.label;
        document.getElementById('scenario-cmd').textContent = activeScenario.command;
        document.getElementById('scenario-desc').textContent = activeScenario.description;
        renderForm();
        schedulePreview();
    }

    function renderForm() {
        const form = document.getElementById('cfg-form');
        form.innerHTML = '';
        activeScenario.fields.forEach(f => form.appendChild(buildField(f)));
        applyConditions();
    }

    function buildField(f) {
        const wrap = document.createElement('div');
        wrap.className = 'field';
        wrap.dataset.name = f.name;
        if (f.show_when) {
            wrap.dataset.condField = f.show_when.field;
            if (f.show_when.values && f.show_when.values.length) {
                wrap.dataset.condValues = JSON.stringify(f.show_when.values);
            } else {
                wrap.dataset.condValue = f.show_when.value;
            }
        }

        const label = document.createElement('label');
        label.textContent = f.label + (f.required ? ' *' : '');
        wrap.appendChild(label);

        let input;
        if (f.type === 'select') {
            input = document.createElement('select');
            f.options.forEach(opt => {
                const o = document.createElement('option');
                o.value = opt;
                o.textContent = opt;
                if (opt === f.default) o.selected = true;
                input.appendChild(o);
            });
        } else {
            input = document.createElement('input');
            input.type = f.type === 'password' ? 'password' : 'text';
            if (f.default) input.value = f.default;
            if (f.placeholder) input.placeholder = f.placeholder;
            if (isDSN(f.name)) input.classList.add('mono');
        }
        input.name = f.name;
        input.addEventListener('input', schedulePreview);
        input.addEventListener('change', () => { applyConditions(); updateDSNHint(f, wrap); schedulePreview(); });
        wrap.appendChild(input);

        if (f.help) {
            const help = document.createElement('div');
            help.className = 'field-help';
            help.textContent = f.help;
            wrap.appendChild(help);
        }

        // DSN fields get a structured modal editor that writes back into the
        // raw DSN input (and so into the live YAML preview), plus a test
        // connection button. The field itself is read-only display: clicking
        // it reopens the editor.
        if (isDSN(f.name)) {
            const side = f.name === 'source_dsn' ? 'source' : 'target';
            input.readOnly = true;
            input.classList.add('dsn-readonly');
            input.addEventListener('click', () => openDSNModal(side));
            const actions = document.createElement('div');
            actions.className = 'field-actions';
            const btn = document.createElement('button');
            btn.type = 'button';
            btn.className = 'btn-ghost btn-sm';
            btn.textContent = '结构化填写';
            btn.addEventListener('click', () => openDSNModal(side));
            const tbtn = document.createElement('button');
            tbtn.type = 'button';
            tbtn.className = 'btn-ghost btn-sm';
            tbtn.textContent = '测试连接';
            tbtn.addEventListener('click', () => testConn(side));
            const status = document.createElement('span');
            status.className = 'conn-status';
            status.dataset.side = side;
            actions.appendChild(btn);
            actions.appendChild(tbtn);
            actions.appendChild(status);
            wrap.appendChild(actions);
            updateDSNHint(f, wrap);
        }
        return wrap;
    }

    function isDSN(name) { return name === 'source_dsn' || name === 'target_dsn'; }

    // Keep the DSN field minimal: a short click-to-edit placeholder and the
    // field's own help. Per-dialect format examples now live inside the modal.
    function updateDSNHint(f, wrap) {
        const helpEl = wrap.querySelector('.field-help');
        const input = wrap.querySelector('input');
        if (!input) return;
        input.placeholder = f.placeholder || '';
        if (helpEl) helpEl.textContent = f.help || '';
    }

    function applyConditions() {
        document.querySelectorAll('#cfg-form .field[data-cond-field]').forEach(wrap => {
            const ctl = document.querySelector(`[name="${wrap.dataset.condField}"]`);
            let visible = false;
            if (ctl && wrap.dataset.condValues) {
                const values = JSON.parse(wrap.dataset.condValues);
                visible = values.indexOf(ctl.value) >= 0;
            } else if (ctl) {
                visible = ctl.value === wrap.dataset.condValue;
            }
            wrap.classList.toggle('hidden', !visible);
        });
        // Refresh DSN placeholders/help after visibility changes.
        activeScenario.fields.forEach(f => {
            if (isDSN(f.name)) {
                const wrap = document.querySelector(`#cfg-form .field[data-name="${f.name}"]`);
                if (wrap) updateDSNHint(f, wrap);
            }
        });
        updateSourceDBHint();
    }

    // In the migrate flow the source database is always required: even when the
    // structure comes from CSV/XLSX, the data rows are still exported from the
    // source DB. Show a clarifying note on the source DSN field whenever
    // metadata_type is not "database".
    function updateSourceDBHint() {
        const mt = document.querySelector(`#cfg-form [name="metadata_type"]`);
        const dsnWrap = document.querySelector(`#cfg-form .field[data-name="source_dsn"]`);
        if (!dsnWrap) return;
        let note = dsnWrap.querySelector('.source-dsn-note');
        if (mt && mt.value !== 'database') {
            if (!note) {
                note = document.createElement('div');
                note.className = 'field-note source-dsn-note';
                note.textContent = '提示：此流程仍会从源库导出数据。即使表结构由 CSV/XLSX 定义，也需要按源库类型填写 DSN。';
                dsnWrap.appendChild(note);
            }
            note.style.display = '';
        } else if (note) {
            note.style.display = 'none';
        }
    }

    // ── DSN structured editing modal ──────────────────────────────────────
    let modal = null; // { overlay, body, raw, data:{side,family,currentDsn} }

    function ensureModal() {
        if (modal) return modal;
        const overlay = document.createElement('div');
        overlay.className = 'dsn-modal-overlay';
        overlay.innerHTML =
            '<div class="dsn-modal" role="dialog" aria-modal="true">' +
            '<div class="dsn-modal-head"><h3 id="dsn-modal-title"></h3>' +
            '<button type="button" class="btn-ghost dsn-modal-x" aria-label="关闭">×</button></div>' +
            '<div class="dsn-modal-body" id="dsn-modal-body"></div>' +
            '<div class="dsn-modal-preview"><span>生成中的 DSN</span><pre id="dsn-modal-raw" class="mono"></pre></div>' +
            '<div class="dsn-modal-actions">' +
            '<button type="button" class="btn-ghost" id="dsn-modal-cancel">取消</button>' +
            '<button type="button" class="btn-primary" id="dsn-modal-apply">应用</button>' +
            '</div></div>';
        document.body.appendChild(overlay);
        overlay.addEventListener('click', e => { if (e.target === overlay) revertDSNModal(); });
        overlay.querySelector('.dsn-modal-x').addEventListener('click', revertDSNModal);
        overlay.querySelector('#dsn-modal-cancel').addEventListener('click', revertDSNModal);
        overlay.querySelector('#dsn-modal-apply').addEventListener('click', applyDSNModal);
        document.addEventListener('keydown', e => { if (e.key === 'Escape' && modal && modal.overlay.classList.contains('open')) revertDSNModal(); });
        modal = {
            overlay,
            body: overlay.querySelector('#dsn-modal-body'),
            raw: overlay.querySelector('#dsn-modal-raw'),
            data: null,
        };
        return modal;
    }

    function openDSNModal(side) {
        ensureModal();
        const typeInput = document.querySelector(`[name="${side}_type"]`);
        const dialect = typeInput ? typeInput.value : '';
        const family = dialect ? (dsnFamilies[dialect] || '') : '';
        const dsnInput = document.querySelector(`[name="${side}_dsn"]`);
        const current = dsnInput ? dsnInput.value : '';
        modal.data = { side, family, currentDsn: current };

        const meta = dsnFields[family] || {};
        const sideLabel = side === 'source' ? '源' : '目标';
        modal.overlay.querySelector('#dsn-modal-title').textContent = '编辑' + sideLabel + '数据库 DSN';

        renderModalBody(modal.body, family, meta);
        prefillModal(parseDSN(current, family), family);
        const ta = modal.body.querySelector('[name="modal_dsn_raw"]');
        if (ta) ta.value = current;
        modal.raw.textContent = current || '等待填写…';
        modal.overlay.classList.add('open');
        document.body.classList.add('modal-open');
        const first = modal.body.querySelector('input');
        if (first) first.focus();
    }

    function renderModalBody(body, family, meta) {
        body.innerHTML = '';
        if (family === 'file') {
            body.appendChild(modalField('path', '数据库文件路径', meta.db_placeholder || '例如: /path/to/xxx.db', 'text'));
            return;
        }
        const fields = [
            ['host', '主机', '例如: 127.0.0.1', 'text'],
            ['port', '端口', '默认 ' + (meta.port || ''), 'text'],
            ['user', '用户', '登录用户名', 'text'],
        ];
        // OceanBase MySQL tenant mode: tenant/cluster fold into the username.
        if (meta.has_tenant) {
            fields.push(['tenant', '租户', 'OceanBase 租户（可选）', 'text']);
            fields.push(['cluster', '集群', 'OceanBase 集群（可选）', 'text']);
        }
        fields.push(['password', '密码', '数据库登录密码', 'password']);
        fields.push(['db', meta.db_label || '数据库', meta.db_placeholder || '', 'text']);
        if (!meta.has_tenant && meta.has_cluster) {
            fields.push(['cluster', '集群', '例如: obcluster（可选）', 'text']);
        }
        fields.forEach(([key, label, ph, type]) => body.appendChild(modalField(key, label, ph, type)));
        // Editable raw DSN: the single source of truth reflected on apply.
        const rawWrap = document.createElement('div');
        rawWrap.className = 'field';
        rawWrap.dataset.key = 'raw';
        const rl = document.createElement('label');
        rl.textContent = '原始 DSN（可直接编辑）';
        rawWrap.appendChild(rl);
        const ta = document.createElement('textarea');
        ta.className = 'mono dsn-raw-input';
        ta.name = 'modal_dsn_raw';
        ta.addEventListener('input', onRawInput);
        rawWrap.appendChild(ta);
        body.appendChild(rawWrap);
    }

    function modalField(key, label, ph, type) {
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
        input.addEventListener('input', onModalInput);
        wrap.appendChild(input);
        return wrap;
    }

    function mval(key) {
        const el = modal && modal.body ? modal.body.querySelector(`[data-key="${key}"] input`) : null;
        return el ? (el.value || '') : '';
    }

    function assembleModal() {
        const meta = dsnFields[modal.data.family] || {};
        if (modal.data.family === 'file') return mval('path');
        if (!meta.builder) return '';
        // Missing components become editable $placeholders (db is optional), so
        // the DSN is always generated live and 应用 always writes something.
        const ph = { user: '$user', password: '$pass', host: '$host', port: '$port', db: '$db', tenant: '$tenant', cluster: '$cluster' };
        let out = meta.builder.replace(/\{(\w+)\}/g, (m, k) => {
            let v;
            // OceanBase MySQL tenant mode: the full OceanBase login is
            // user@tenant#cluster, folded into the {user} token.
            if (k === 'user' && meta.has_tenant) {
                let u = mval('user');
                if (!u) {
                    v = ph.user;
                } else {
                    const t = mval('tenant');
                    const c = mval('cluster');
                    if (t) u += '@' + t;
                    if (c) u += '#' + c;
                    v = u;
                }
            } else {
                v = mval(k);
                if (!v) v = ph[k] || '';
            }
            // Leave $placeholder tokens unencoded so they stay human-editable.
            const isToken = /^\$[A-Za-z]+$/.test(v);
            if (meta.url_style && !isToken && (k === 'user' || k === 'password' || k === 'db')) v = encodeURIComponent(v);
            return v;
        });
        if (modal.data.family === 'postgres') {
            out = out.split(/\s+/).filter(t => t && !/^\w+=$/.test(t)).join(' ');
        }
        out = out.replace(/\s+/g, ' ').trim();
        if (meta.has_cluster && !meta.has_tenant) {
            const c = mval('cluster');
            if (c) out += (out.indexOf('?') >= 0 ? '&' : '?') + 'cluster=' + encodeURIComponent(c);
        }
        return out;
    }

    function onModalInput() {
        const assembled = assembleModal();
        const ta = modal.body.querySelector('[name="modal_dsn_raw"]');
        if (ta) ta.value = assembled || modal.data.currentDsn;
        modal.raw.textContent = assembled || modal.data.currentDsn || '等待填写…';
        const dsnInput = document.querySelector(`[name="${modal.data.side}_dsn"]`);
        if (assembled && dsnInput) {
            dsnInput.value = assembled;
            schedulePreview();
        }
    }

    // User edits the raw DSN directly: bypass structured assembly.
    function onRawInput() {
        const ta = modal.body.querySelector('[name="modal_dsn_raw"]');
        const v = ta ? ta.value : '';
        modal.raw.textContent = v || '等待填写…';
        const dsnInput = document.querySelector(`[name="${modal.data.side}_dsn"]`);
        if (v && dsnInput) {
            dsnInput.value = v;
            schedulePreview();
        }
    }

    // Revert the DSN field to its value when the modal was opened.
    function revertDSNModal() {
        if (!modal || !modal.data) return;
        const dsnInput = document.querySelector(`[name="${modal.data.side}_dsn"]`);
        if (dsnInput) { dsnInput.value = modal.data.currentDsn; schedulePreview(); }
        closeDSNModal();
    }

    function applyDSNModal() {
        if (!modal || !modal.data) return;
        const assembled = assembleModal();
        const dsnInput = document.querySelector(`[name="${modal.data.side}_dsn"]`);
        if (assembled && dsnInput) { dsnInput.value = assembled; schedulePreview(); }
        closeDSNModal();
    }

    function closeDSNModal() {
        if (!modal) return;
        modal.overlay.classList.remove('open');
        document.body.classList.remove('modal-open');
    }

    // Test the connection with the current type + DSN via the backend.
    async function testConn(side) {
        const status = document.querySelector(`.conn-status[data-side="${side}"]`);
        const setStatus = (msg, cls) => {
            if (!status) return;
            status.textContent = msg;
            status.className = 'conn-status ' + cls;
            setTimeout(() => { status.textContent = ''; }, 6000);
        };
        const typeInput = document.querySelector(`[name="${side}_type"]`);
        const dsnInput = document.querySelector(`[name="${side}_dsn"]`);
        const schemaInput = document.querySelector(`[name="${side}_schema"]`);
        const dsn = dsnInput ? dsnInput.value : '';
        if (!dsn) { setStatus('请先填写 DSN', 'fail'); return; }
        setStatus('连接中…', 'pending');
        try {
            const resp = await api.post('/api/v1/conn/test', {
                type: typeInput ? typeInput.value : '',
                dsn,
                schema: schemaInput ? schemaInput.value : '',
            });
            if (resp.error) {
                setStatus('✗ ' + resp.error, 'fail');
            } else {
                setStatus('✓ 连接成功 ' + (resp.latency != null ? resp.latency + 'ms' : ''), 'ok');
                // On a passing connection, offer the existing schemas as a
                // dropdown so the schema can be picked instead of typed.
                enableSchemaSelect(side, resp.schemas || []);
            }
        } catch (e) {
            setStatus('✗ ' + e.message, 'fail');
        }
    }

    // Replace the plain schema text input with a dropdown of existing schemas
    // discovered on the live connection. Re-running a successful test refreshes
    // the options; a refresh button is added for convenience.
    function enableSchemaSelect(side, schemas) {
        if (!Array.isArray(schemas) || !schemas.length) return;
        const wrap = document.querySelector(`#cfg-form .field[data-name="${side}_schema"]`);
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

    // Best-effort parse of an existing DSN back into structured fields so the
    // modal can prefill. Fills only the recognised pieces; the raw DSN is
    // always preserved in the live preview and on cancel.
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
        // OceanBase MySQL tenant mode: user@tenant#cluster:pass@tcp(...)/db
        if (family === 'oceanbase-mysql') {
            const om = dsn.match(/^(.+?):([^@]*)@tcp\(([^:]+):(\d+)\)\/(.*)$/);
            if (om) {
                const u = om[1];
                let user = u, tenant = '', cluster = '';
                const h = u.indexOf('#');
                const at = u.indexOf('@');
                if (h >= 0) { cluster = u.slice(h + 1); }
                const base = h >= 0 ? u.slice(0, h) : u;
                if (at >= 0) { tenant = base.slice(at + 1); user = base.slice(0, at); }
                o.user = decodeURIComponent(user);
                o.tenant = tenant;
                o.cluster = cluster;
                o.password = decodeURIComponent(om[2]);
                o.host = om[3]; o.port = om[4]; o.db = om[5];
                return o;
            }
        }
        const m = dsn.match(/^([A-Za-z0-9_.-]+):([^@]*)@tcp\(([^:]+):(\d+)\)\/(.*)$/);
        if (m) {
            o.user = m[1]; o.password = decodeURIComponent(m[2]);
            o.host = m[3]; o.port = m[4]; o.db = m[5];
            return o;
        }
        let u;
        try { u = new URL(dsn); } catch (e) { return o; }
        if (/^(oracle|oboracle|oceanbase-oracle|postgres|postgresql):/.test(u.protocol)) {
            o.user = u.username || '';
            o.password = u.password || '';
            o.host = u.hostname || '';
            o.port = u.port || '';
            o.db = (u.pathname || '').replace(/^\//, '');
            if (u.searchParams.get('cluster')) o.cluster = u.searchParams.get('cluster');
            return o;
        }
        return o;
    }

    function prefillModal(o, family) {
        const set = (key, v) => {
            if (!v) return;
            const el = modal.body.querySelector(`[data-key="${key}"] input`);
            if (el) el.value = v;
        };
        if (family === 'file') { set('path', o.path || o.db || ''); return; }
        set('host', o.host); set('port', o.port);
        set('user', o.user); set('password', o.password);
        set('tenant', o.tenant);
        set('db', o.db); set('cluster', o.cluster);
    }

    function collectValues() {
        const values = {};
        document.querySelectorAll('#cfg-form .field:not(.hidden) [name]').forEach(el => {
            values[el.name] = el.value;
        });
        return values;
    }

    function schedulePreview() {
        clearTimeout(previewTimer);
        const dot = document.getElementById('live-dot');
        dot.classList.add('busy');
        previewTimer = setTimeout(renderPreview, 350);
    }

    async function renderPreview() {
        const dot = document.getElementById('live-dot');
        const pre = document.getElementById('yaml-preview');
        try {
            const resp = await api.post(`/api/v1/scenarios/${activeScenario.name}/build`,
                { values: collectValues(), save: false });
            pre.innerHTML = highlightYAML(resp.yaml || '');
        } catch (e) {
            pre.innerHTML = '<span class="yaml-err">' + escapeHtml(e.message) + '</span>';
        } finally {
            dot.classList.remove('busy');
        }
    }

    document.getElementById('btn-save').addEventListener('click', async () => {
        const status = document.getElementById('save-status');
        const pathEl = document.getElementById('saved-path');
        try {
            const resp = await api.post(`/api/v1/scenarios/${activeScenario.name}/build`,
                { values: collectValues(), save: true });
            status.textContent = '✓ 已保存为当前配置';
            status.className = 'save-status ok';
            pathEl.textContent = resp.path ? '已写入文件：' + resp.path : '';
        } catch (e) {
            status.textContent = '✗ ' + e.message;
            status.className = 'save-status fail';
        }
        setTimeout(() => { status.textContent = ''; }, 3000);
    });

    // Fill the active scenario's form fields from a {name: value} map.
    function applyFormValues(values) {
        document.querySelectorAll('#cfg-form [name]').forEach(el => {
            const val = values[el.name];
            if (val !== undefined && val !== '') el.value = val;
        });
        applyConditions();
        activeScenario.fields.forEach(f => {
            if (isDSN(f.name)) {
                const wrap = document.querySelector(`#cfg-form .field[data-name="${f.name}"]`);
                if (wrap) updateDSNHint(f, wrap);
            }
        });
    }

    // Apply an uploaded/loaded config: switch scenario, fill form, show YAML.
    function applyConfigResp(resp) {
        if (resp.scenario && allScenarios.some(s => s.name === resp.scenario)) {
            history.replaceState(null, '', '/config?scenario=' + resp.scenario);
            selectScenario(resp.scenario);
        }
        applyFormValues(resp.values || {});
        clearTimeout(previewTimer);
        document.getElementById('live-dot').classList.remove('busy');
        document.getElementById('yaml-preview').innerHTML = highlightYAML(resp.yaml || '');
    }

    // ── Config library ──

    async function loadConfigLib() {
        const list = await api.get('/api/v1/configs');
        renderConfigLib(list);
    }

    function renderConfigLib(list) {
        const tbody = document.getElementById('config-lib-body');
        tbody.innerHTML = '';
        if (!list.length) {
            tbody.innerHTML = '<tr><td colspan="6" class="lib-empty">暂无保存的配置，上传后即可复用</td></tr>';
            return;
        }
        list.forEach(c => {
            const tr = document.createElement('tr');
            const flow = [c.source_type, c.target_type].filter(Boolean).join(' → ') || '-';
            tr.innerHTML =
                `<td class="mono">${c.name}</td>` +
                `<td>${c.scenario || '-'}</td>` +
                `<td class="mono">${flow}</td>` +
                `<td>${humanSize(c.size)}</td>` +
                `<td>${(c.modified || '').replace('T', ' ').slice(0, 19)}</td>` +
                `<td class="lib-actions"></td>`;
            const actions = tr.querySelector('.lib-actions');
            actions.appendChild(libButton('加载', 'btn-primary', () => loadConfig(c.name)));
            const dl = document.createElement('a');
            dl.className = 'btn-ghost'; dl.textContent = '下载';
            dl.href = '/api/v1/configs/' + encodeURIComponent(c.name);
            actions.appendChild(dl);
            actions.appendChild(libButton('删除', 'btn-danger', () => deleteConfig(c.name)));
            tbody.appendChild(tr);
        });
    }

    function libButton(text, cls, onclick) {
        const b = document.createElement('button');
        b.type = 'button'; b.className = cls; b.textContent = text;
        b.addEventListener('click', onclick);
        return b;
    }

    async function loadConfig(name) {
        const status = document.getElementById('upload-status');
        try {
            const resp = await api.post('/api/v1/configs/' + encodeURIComponent(name) + '/load', {});
            applyConfigResp(resp);
            status.textContent = '✓ 已加载配置：' + name; status.className = 'status-msg ok';
        } catch (e) {
            status.textContent = '✗ ' + e.message; status.className = 'status-msg fail';
        }
    }

    async function deleteConfig(name) {
        if (!confirm('确认删除配置 "' + name + '"？')) return;
        const status = document.getElementById('upload-status');
        try {
            await api.del('/api/v1/configs/' + encodeURIComponent(name));
            status.textContent = '✓ 已删除：' + name; status.className = 'status-msg ok';
            loadConfigLib();
        } catch (e) {
            status.textContent = '✗ ' + e.message; status.className = 'status-msg fail';
        }
    }

    function humanSize(bytes) {
        if (!bytes && bytes !== 0) return '-';
        if (bytes < 1024) return bytes + ' B';
        if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
        return (bytes / 1024 / 1024).toFixed(1) + ' MB';
    }

    // Upload a YAML config into the library (and make it the active config).
    window.uploadFile = async function () {
        const input = document.getElementById('cfg-file');
        const status = document.getElementById('upload-status');
        if (!input.files.length) {
            status.textContent = '✗ 请先选择文件'; status.className = 'status-msg fail'; return;
        }
        const file = input.files[0];
        const text = await file.text();
        try {
            const resp = await api.post('/api/v1/configs', { name: file.name, yaml: text });
            applyConfigResp(resp);
            loadConfigLib();
            status.textContent = '✓ 已存入配置库并填入表单：' + resp.name;
            status.className = 'status-msg ok';
            clearFilePicker();
        } catch (e) {
            status.textContent = '✗ ' + e.message; status.className = 'status-msg fail';
        }
    };

    function clearFilePicker() {
        const input = document.getElementById('cfg-file');
        input.value = '';
        document.getElementById('file-picker').classList.remove('has-file');
        document.getElementById('cfg-file-name').textContent = '.yaml / .yml 配置文件';
    }

    // Reflect the chosen file in the custom picker.
    document.getElementById('cfg-file').addEventListener('change', function () {
        const picker = document.getElementById('file-picker');
        const nameEl = document.getElementById('cfg-file-name');
        if (this.files.length) {
            picker.classList.add('has-file');
            nameEl.textContent = this.files[0].name;
        } else {
            clearFilePicker();
        }
    });
})();
