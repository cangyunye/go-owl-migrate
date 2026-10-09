/* owl-migrate SPA · AI 供应商配置模态框
   ============================================================
   从 AI 助手页右上角供应商徽章触发（原配置页面板迁入）。
   安全不变量：Key 加密存本机凭据库，配置文件与页面永不回显；
   测试连接 / 获取模型列表优先使用输入框已粘贴但未保存的 Key。
   onSaved 在供应商配置或 Key 变更后回调（调用方刷新徽章/可用态）。
   ============================================================ */

import { escapeHtml, modalFocus } from './util.js';

const AI_PRESET_LABELS = {
    deepseek: 'DeepSeek', openai: 'OpenAI', moonshot: 'Moonshot Kimi',
    qwen: '通义千问 Qwen', glm: '智谱 GLM', ollama: 'Ollama（本机）', custom: '自定义端点',
};

export async function openProviderModal(onSaved) {
    if (document.querySelector('.dsn-modal-overlay.ai-provider-modal')) return; // 防重复打开
    let p = null;
    try { p = await window.api.get('/api/v1/ai/provider'); } catch (e) { p = null; }
    if (!p) {
        if (window.toast) window.toast.err('AI 供应商', '配置读取失败，请确认 serve 正常');
        return;
    }

    const overlay = document.createElement('div');
    overlay.className = 'dsn-modal-overlay ai-provider-modal';
    overlay.innerHTML = ''
        + '<div class="dsn-modal" role="dialog" aria-modal="true" aria-labelledby="ai-pm-title">'
        +   '<div class="dsn-modal-head"><h3 id="ai-pm-title">AI 供应商</h3>'
        +     '<span class="cfg-chip" id="ai-pm-key-chip">…</span>'
        +     '<button type="button" class="btn-ghost dsn-modal-x" aria-label="关闭">×</button></div>'
        +   '<div class="dsn-modal-body">'
        +     '<p class="field-help">任何 OpenAI 兼容端点。Key 经加密保存在本机凭据库（服务端调用时解密），配置文件与页面永不回显。测试连接 / 获取模型列表会优先使用上方已粘贴但未保存的 Key。</p>'
        +     '<div class="ai-form-grid">'
        +       '<label class="ai-fld"><span>供应商预设</span>'
        +         '<select id="ai-pm-select"></select>'
        +       '</label>'
        +       '<label class="ai-fld"><span>Base URL</span>'
        +         '<input type="text" id="ai-pm-base-url" class="mono" spellcheck="false" autocomplete="off" placeholder="https://api.deepseek.com">'
        +       '</label>'
        +       '<label class="ai-fld"><span>模型</span>'
        +         '<span class="upload-row">'
        +           '<span class="ai-model-wrap">'
        +             '<input type="text" id="ai-pm-model" class="mono" spellcheck="false" autocomplete="off" placeholder="填 URL + Key 后可探测">'
        +             '<div id="ai-pm-model-dd" class="ai-model-dd" hidden></div>'
        +           '</span>'
        +           '<button type="button" class="btn-ghost btn-sm" id="ai-pm-models-btn">获取模型列表</button>'
        +         '</span>'
        +       '</label>'
        +       '<label class="ai-fld"><span>API Key</span>'
        +         '<span class="upload-row">'
        +           '<input type="password" id="ai-pm-key" class="mono" autocomplete="new-password" placeholder="不回显；仅本机保存">'
        +           '<button type="button" class="btn-primary btn-sm" id="ai-pm-key-save">保存 Key</button>'
        +           '<button type="button" class="btn-ghost btn-sm" id="ai-pm-key-delete">删除 Key</button>'
        +         '</span>'
        +       '</label>'
        +     '</div>'
        +     '<details class="ai-advanced">'
        +       '<summary>高级参数（可选，留空用默认）</summary>'
        +       '<div class="ai-form-grid">'
        +         '<label class="ai-fld"><span>路由思考强度 effort</span>'
        +           '<select id="ai-pm-effort"><option value="">默认</option><option value="low">low</option><option value="high">high</option><option value="max">max</option></select>'
        +         '</label>'
        +         '<label class="ai-fld"><span>生成思考强度 plan_effort</span>'
        +           '<select id="ai-pm-plan-effort"><option value="">默认</option><option value="low">low</option><option value="high">high</option><option value="max">max</option></select>'
        +         '</label>'
        +         '<label class="ai-fld"><span>max_tokens</span>'
        +           '<input type="number" id="ai-pm-max-tokens" min="1" placeholder="32768">'
        +         '</label>'
        +         '<label class="ai-fld"><span>context_window</span>'
        +           '<input type="number" id="ai-pm-context-window" min="1" placeholder="1048576">'
        +         '</label>'
        +         '<label class="ai-fld"><span>超时 timeout</span>'
        +           '<input type="text" id="ai-pm-timeout" class="mono" placeholder="2m">'
        +         '</label>'
        +         '<label class="ai-fld"><span>Key 环境变量名 api_key_env</span>'
        +           '<input type="text" id="ai-pm-key-env" class="mono" spellcheck="false" placeholder="OWL_AI_API_KEY">'
        +         '</label>'
        +       '</div>'
        +     '</details>'
        +   '</div>'
        +   '<div class="dsn-modal-actions">'
        +     '<button type="button" class="btn-ghost" id="ai-pm-test" style="margin-right:auto">测试连接</button>'
        +     '<span class="status-msg" id="ai-pm-status" role="status"></span>'
        +     '<button type="button" class="btn-primary" id="ai-pm-save">保存供应商配置</button>'
        +     '<button type="button" class="btn-ghost" id="ai-pm-cancel">取消</button>'
        +   '</div>'
        + '</div>';

    document.body.appendChild(overlay);
    overlay.classList.add('open');
    document.body.classList.add('modal-open');
    const dsFocus = modalFocus(overlay);

    const $ = (s) => overlay.querySelector(s);
    const els = {
        select: $('#ai-pm-select'), baseUrl: $('#ai-pm-base-url'),
        model: $('#ai-pm-model'), modelDd: $('#ai-pm-model-dd'),
        key: $('#ai-pm-key'), keyChip: $('#ai-pm-key-chip'),
        keySave: $('#ai-pm-key-save'), keyDelete: $('#ai-pm-key-delete'),
        effort: $('#ai-pm-effort'), planEffort: $('#ai-pm-plan-effort'),
        maxTokens: $('#ai-pm-max-tokens'), contextWindow: $('#ai-pm-context-window'),
        timeout: $('#ai-pm-timeout'), keyEnv: $('#ai-pm-key-env'),
        save: $('#ai-pm-save'), test: $('#ai-pm-test'), cancel: $('#ai-pm-cancel'),
        modelsBtn: $('#ai-pm-models-btn'), status: $('#ai-pm-status'),
    };
    let presetURLs = {};
    let changed = false; // 供应商配置或 Key 是否在本框内变更过

    function status(msg, cls) {
        els.status.textContent = msg;
        els.status.className = 'status-msg' + (cls ? ' ' + cls : '');
    }
    function renderKeyChip(p) {
        els.keyChip.textContent = p.key_source === 'vault' ? 'Key · 本机凭据库'
            : p.key_source === 'env' ? 'Key · 环境变量 ' + (p.api_key_env || '')
            : '未配置 Key';
        els.keyChip.classList.toggle('badge-green', p.key_set === true);
    }
    function renderProvider(p) {
        presetURLs = p.preset_base_urls || {};
        els.select.innerHTML = (p.presets || []).map(name =>
            '<option value="' + name + '"' + (name === p.provider ? ' selected' : '') + '>'
            + (AI_PRESET_LABELS[name] || name) + '</option>').join('');
        els.baseUrl.value = p.base_url || '';
        els.model.value = p.model || '';
        els.effort.value = p.effort || '';
        els.planEffort.value = p.plan_effort || '';
        els.maxTokens.value = p.max_tokens || '';
        els.contextWindow.value = p.context_window || '';
        els.timeout.value = p.timeout || '';
        els.keyEnv.value = p.api_key_env || '';
        renderKeyChip(p);
    }
    renderProvider(p);

    /* ── 关闭 ── */
    function close() {
        overlay.classList.remove('open');
        document.body.classList.remove('modal-open');
        dsFocus.close();
        if (overlay.parentNode) overlay.parentNode.removeChild(overlay);
        if (changed && typeof onSaved === 'function') onSaved();
    }
    els.cancel.addEventListener('click', close);
    overlay.querySelector('.dsn-modal-x').addEventListener('click', close);
    overlay.addEventListener('click', (e) => { if (e.target === overlay) close(); });
    overlay.addEventListener('keydown', (e) => { if (e.key === 'Escape') close(); });

    /* ── 供应商预设切换 → base_url 吃预设默认 ── */
    els.select.addEventListener('change', () => {
        els.baseUrl.value = presetURLs[els.select.value] || '';
    });

    /* ── Key 保存 / 删除 ── */
    els.keySave.addEventListener('click', async () => {
        const key = els.key.value.trim();
        if (!key) { status('请先粘贴 API Key', 'fail'); return; }
        try {
            const r = await window.api.post('/api/v1/ai/key', { key });
            els.key.value = '';
            changed = true;
            status('✓ Key 已加密保存到本机凭据库', 'ok');
            /* 只刷 Key 徽章——不能重刷表单，会冲掉用户未保存的编辑 */
            renderKeyChip({ key_source: r.key_source || 'vault', key_set: true, api_key_env: els.keyEnv.value.trim() });
        } catch (e) { status('✗ ' + (e.message || e), 'fail'); }
    });
    els.keyDelete.addEventListener('click', async () => {
        if (!window.confirm('删除已保存的本机 Key？（环境变量不受影响）')) return;
        try {
            const r = await window.api.del('/api/v1/ai/key');
            changed = true;
            status('✓ Key 已删除', 'ok');
            const src = (r && r.key_source) || 'none';
            renderKeyChip({
                key_source: src, key_set: src !== 'none',
                api_key_env: els.keyEnv.value.trim() || 'OWL_AI_API_KEY',
            });
        } catch (e) { status('✗ ' + (e.message || e), 'fail'); }
    });

    /* ── 模型探测：自定义下拉，全量展示不过滤 ── */
    els.modelsBtn.addEventListener('click', async () => {
        status('探测模型列表…', 'pending');
        els.modelDd.hidden = true;
        try {
            const r = await window.api.post('/api/v1/ai/models', {
                base_url: els.baseUrl.value.trim(),
                key: els.key.value.trim(),
            });
            const models = r.models || [];
            els.modelDd.innerHTML = models.map(m =>
                '<button type="button" class="ai-model-opt" data-model="' + escapeHtml(m) + '">' + escapeHtml(m) + '</button>').join('');
            els.modelDd.hidden = models.length === 0;
            let note = '';
            if (r.effective_base_url && r.effective_base_url !== els.baseUrl.value.trim()) {
                els.baseUrl.value = r.effective_base_url; // 端点挂在 /v1 下：回填自适应路径
                note = '（端点挂在 /v1 路径下，已自动回填 Base URL，记得保存）';
            }
            status(models.length ? '✓ 探测到 ' + models.length + ' 个模型，点击选择' + note
                : '端点未返回模型列表，可手填', models.length ? 'ok' : 'fail');
        } catch (e) {
            els.modelDd.hidden = true;
            status('✗ ' + (e.message || e) + '（可手填模型继续）', 'fail');
        }
    });
    els.modelDd.addEventListener('click', (e) => {
        const opt = e.target.closest('.ai-model-opt');
        if (!opt) return;
        els.model.value = opt.dataset.model;
        els.modelDd.hidden = true;
    });
    els.model.addEventListener('focus', () => {
        if (els.modelDd.children.length) els.modelDd.hidden = false;
    });
    els.model.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') els.modelDd.hidden = true;
    });
    if (!window.__aiModelDdOutsideBound) {
        window.__aiModelDdOutsideBound = true;
        document.addEventListener('click', (e) => {
            const dd = document.getElementById('ai-model-dd') || document.getElementById('ai-pm-model-dd');
            if (dd && !dd.hidden && !e.target.closest('.ai-model-wrap')) dd.hidden = true;
        });
    }

    /* ── 保存供应商配置 ── */
    els.save.addEventListener('click', async () => {
        const body = {
            provider: els.select.value,
            base_url: els.baseUrl.value.trim(),
            model: els.model.value.trim(),
            api_key_env: els.keyEnv.value.trim(),
            effort: els.effort.value,
            plan_effort: els.planEffort.value,
        };
        if (els.maxTokens.value) body.max_tokens = Number(els.maxTokens.value);
        if (els.contextWindow.value) body.context_window = Number(els.contextWindow.value);
        if (els.timeout.value.trim()) body.timeout = els.timeout.value.trim();
        try {
            await window.api.put('/api/v1/ai/provider', body);
            changed = true;
            status('✓ 供应商配置已保存并生效', 'ok');
            renderProvider(await window.api.get('/api/v1/ai/provider'));
        } catch (e) { status('✗ ' + (e.message || e), 'fail'); }
    });

    /* ── 测试连接 ── */
    els.test.addEventListener('click', async () => {
        status('连接测试中…', 'pending');
        try {
            const r = await window.api.post('/api/v1/ai/test', {
                base_url: els.baseUrl.value.trim(), model: els.model.value.trim(),
                key: els.key.value.trim(),
            });
            if (r.ok) {
                let note = '';
                if (r.effective_base_url && r.effective_base_url !== els.baseUrl.value.trim()) {
                    els.baseUrl.value = r.effective_base_url;
                    note = '（端点挂在 /v1 路径下，已自动回填 Base URL，记得保存）';
                }
                status('✓ 连接成功（' + r.model + '，' + r.latency_ms + 'ms）' + note, 'ok');
            } else status('✗ ' + (r.error || '未知错误'), 'fail');
        } catch (e) { status('✗ ' + (e.message || e), 'fail'); }
    });
}
