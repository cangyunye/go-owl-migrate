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
})();
