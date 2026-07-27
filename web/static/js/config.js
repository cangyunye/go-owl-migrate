(function () {
    'use strict';

    let allScenarios = [];
    let dsnExamples = {};
    let activeScenario = null;
    let previewTimer = null;

    const qs = new URLSearchParams(location.search);
    const initialScenario = qs.get('scenario') || 'migrate';

    init();

    async function init() {
        const data = await api.get('/api/v1/scenarios');
        allScenarios = data.scenarios;
        dsnExamples = data.dsn_examples || {};
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
            wrap.dataset.condValue = f.show_when.value;
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
            input.type = 'text';
            if (f.default) input.value = f.default;
            if (isDSN(f.name)) input.classList.add('mono');
        }
        input.name = f.name;
        input.addEventListener('input', schedulePreview);
        input.addEventListener('change', () => { applyConditions(); updateDSNHint(f, wrap); schedulePreview(); });
        wrap.appendChild(input);

        const help = document.createElement('div');
        help.className = 'field-help';
        help.textContent = f.help || '';
        wrap.appendChild(help);

        if (isDSN(f.name)) updateDSNHint(f, wrap);
        return wrap;
    }

    function isDSN(name) { return name === 'source_dsn' || name === 'target_dsn'; }

    // Show a per-dialect DSN format hint under the DSN input, driven by the
    // neighbouring type dropdown.
    function updateDSNHint(f, wrap) {
        const typeName = f.name === 'source_dsn' ? 'source_type' : 'target_type';
        const typeField = activeScenario.fields.find(x => x.name === typeName);
        const helpEl = wrap.querySelector('.field-help');
        if (!typeField) return;
        const typeInput = document.querySelector(`[name="${typeName}"]`);
        const dialect = typeInput ? typeInput.value : '';
        const example = dsnExamples[dialect];
        helpEl.textContent = example ? '格式示例：' + example : (f.help || '');
        helpEl.classList.toggle('has-example', !!example);
    }

    function applyConditions() {
        document.querySelectorAll('#cfg-form .field[data-cond-field]').forEach(wrap => {
            const ctl = document.querySelector(`[name="${wrap.dataset.condField}"]`);
            const visible = ctl && ctl.value === wrap.dataset.condValue;
            wrap.classList.toggle('hidden', !visible);
        });
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
            pre.textContent = resp.yaml;
        } catch (e) {
            pre.innerHTML = '<span class="yaml-err">' + e.message + '</span>';
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
        document.getElementById('yaml-preview').textContent = resp.yaml || '';
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
            input.value = '';
        } catch (e) {
            status.textContent = '✗ ' + e.message; status.className = 'status-msg fail';
        }
    };
})();
