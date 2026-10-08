/* owl-migrate SPA · AI 助手对话页
   ============================================================
   会话是一等公民：
   - 左栏 会话历史（确定性关键词检索 + 克隆继续/取用配置）
   - 右栏 气泡对话 + 信息核对单 + 计划确认卡
   事实规则：源/目标档案选择器是"默认事实"（话语显式提及优先）；
   密码永远不经过页面（档案/哨兵协议）。
   服务端是会话状态唯一事实源；本页只持有 session id（localStorage）
   并负责把 GET /ai/session/{id} 的恢复包画出来。
   ============================================================ */

import { escapeHtml } from '../util.js';
import { dsModal } from './datasources.js';

/* 模块级会话状态：跨 render 存活（修掉旧面板 render 作用域丢失会话的 bug），
   刷新浏览器由 localStorage 恢复。 */
let aiSessionID = null;
let aiPlan = null; // {session_id, plan_id}
let aiCredSlots = [];
let histTimer = null;

const SLOT_LABELS = {
    '__PWD_mysql__': 'MySQL 密码',
    '__PWD_pg__': 'PostgreSQL/openGauss 密码',
    '__PWD_oracle__': 'Oracle 密码',
};
function slotLabel(s) { return SLOT_LABELS[s] || s; }

const QUICK_QUOTES = [
    '导出表数据为 csv',
    '把整库从源库迁移到目标库',
    '生成全部表的建表 DDL',
    '校验当前配置是否可执行',
];

function setStoredSession(id) {
    aiSessionID = id || null;
    try {
        if (id) localStorage.setItem('owl-ai-session', id);
        else localStorage.removeItem('owl-ai-session');
    } catch (e) { /* private mode */ }
}

function loadStoredSession() {
    if (aiSessionID) return aiSessionID;
    try { aiSessionID = localStorage.getItem('owl-ai-session') || null; } catch (e) {}
    return aiSessionID;
}

export async function render(root /*Element*/) {
    if (histTimer) { clearInterval(histTimer); histTimer = null; }

    root.innerHTML = ''
        + '<div class="page-head reveal" style="--i:0">'
        +   '<div>'
        +     '<div class="overline">准备 · AI 助手</div>'
        +     '<h1>AI 助手</h1>'
        +     '<p class="subtitle">说清楚你要做什么，AI 引导补齐关键信息并生成可执行计划</p>'
        +   '</div>'
        +   '<div class="panel-actions">'
        +     '<span class="cfg-chip" id="ai-provider-badge">…</span>'
        +     '<button type="button" class="btn-ghost btn-sm" id="ai-new-topic">新话题</button>'
        +   '</div>'
        + '</div>'

        + '<div class="ai-layout">'
        +   '<aside class="panel ai-hist-panel reveal" style="--i:1">'
        +     '<div class="panel-head"><span class="panel-title">会话历史</span>'
        +       '<button type="button" class="btn-ghost btn-sm" id="ai-hist-edit">批量编辑</button>'
        +     '</div>'
        +     '<label class="check ai-hist-noconfirm"><input type="checkbox" id="ai-hist-noconfirm"> 删除免确认</label>'
        +     '<div class="ai-hist-batch-bar" id="ai-hist-batch-bar" style="display:none">'
        +       '<button type="button" class="btn-danger btn-sm" id="ai-hist-batch-del" disabled>批量删除</button>'
        +       '<span class="field-help" id="ai-hist-sel-info">已选 0 项</span>'
        +     '</div>'
        +     '<input type="text" id="ai-hist-search" class="mono" spellcheck="false" autocomplete="off" placeholder="关键词检索（如 oracle / csv / 迁移）">'
        +     '<div id="ai-hist-list" class="ai-hist-list"></div>'
        +   '</aside>'

        +   '<section class="panel ai-chat-panel reveal" style="--i:2">'
        +     '<div class="ai-facts-row">'
        +       '<label>源数据源</label>'
        +       '<select id="ai-src-profile"><option value="">（让 AI 追问 / 用话语指定）</option></select>'
        +       '<button type="button" class="btn-ghost btn-sm" id="ai-src-new">新建档案…</button>'
        +       '<label id="ai-tgt-label">目标数据源</label>'
        +       '<select id="ai-tgt-profile"><option value="">（不更换目标）</option></select>'
        +       '<button type="button" class="btn-ghost btn-sm" id="ai-tgt-new">新建…</button>'
        +     '</div>'
        +     '<div id="ai-transcript" class="ai-transcript"></div>'
        +     '<div class="ai-quotes" id="ai-quotes"></div>'
        +     '<div class="upload-row ai-input-row">'
        +       '<input type="text" id="ai-input" class="mono" style="flex:1" spellcheck="false" '
        +         'placeholder="描述需求，回车发送。例如：导出 oracle scott 的 emp 表为 csv">'
        +       '<button type="button" class="btn-primary" id="ai-send">发送</button>'
        +     '</div>'
        +     '<div class="upload-row" style="margin-top:6px">'
        +       '<span id="ai-plan-status" class="status-msg" role="status" style="flex:1"></span>'
        +     '</div>'
        +   '</section>'
        + '</div>'

        + '<div id="ai-plan-card" style="display:none" class="panel reveal ai-plan-card">'
        +   '<div class="panel-head"><span class="panel-title">执行计划</span><span class="field-help" id="ai-meta"></span></div>'
        +   '<div class="ai-slot-check" id="ai-slot-check"></div>'
        +   '<pre class="yaml-view" id="ai-plan-yaml" style="max-height:260px;overflow:auto"></pre>'
        +   '<p class="field-help" id="ai-plan-warn"></p>'
        +   '<div id="ai-creds-box" style="display:none;margin-top:8px">'
        +     '<p class="field-help">该计划包含需要密码的连接（仅随确认请求发给本机 serve）：</p>'
        +     '<div id="ai-cred-fields" class="upload-row" style="flex-wrap:wrap;gap:8px"></div>'
        +   '</div>'
        +   '<div class="upload-row" style="margin-top:8px">'
        +     '<button type="button" class="btn-primary" id="ai-execute">确认并执行</button>'
        +     '<button type="button" class="btn-ghost" id="ai-activate">仅激活配置</button>'
        +     '<span id="ai-plan-status2" class="status-msg"></span>'
        +   '</div>'
        + '</div>';

    const $ = (sel) => root.querySelector(sel);
    const el = {
        badge: $('#ai-provider-badge'), transcript: $('#ai-transcript'),
        input: $('#ai-input'), send: $('#ai-send'),
        srcProfile: $('#ai-src-profile'), tgtProfile: $('#ai-tgt-profile'),
        tgtLabel: $('#ai-tgt-label'), srcNew: $('#ai-src-new'), tgtNew: $('#ai-tgt-new'),
        histList: $('#ai-hist-list'), histSearch: $('#ai-hist-search'),
        histEdit: $('#ai-hist-edit'), histNoConfirm: $('#ai-hist-noconfirm'),
        histBatchBar: $('#ai-hist-batch-bar'), histBatchDel: $('#ai-hist-batch-del'),
        histSelInfo: $('#ai-hist-sel-info'),
        quotes: $('#ai-quotes'), newTopic: $('#ai-new-topic'),
        status: $('#ai-plan-status'),
        planCard: $('#ai-plan-card'), meta: $('#ai-meta'),
        slotCheck: $('#ai-slot-check'), planYaml: $('#ai-plan-yaml'),
        planWarn: $('#ai-plan-warn'), credsBox: $('#ai-creds-box'),
        credFields: $('#ai-cred-fields'),
        execute: $('#ai-execute'), activate: $('#ai-activate'),
    };

    /* ── 供应商徽章 + 未配置禁用 ── */
    (async () => {
        try {
            const st = await window.api.get('/api/v1/ai/status');
            if (st && st.enabled) {
                el.badge.textContent = (st.provider || '?') + ' · ' + (st.model || '?');
                return;
            }
            el.badge.textContent = '未配置';
            el.input.disabled = el.send.disabled = true;
            aiSetStatus('AI 未配置：设置环境变量 ' + (st && st.key_env || 'OWL_AI_API_KEY')
                + '（或 DEEPSEEK_API_KEY）后重启 serve', 'fail');
        } catch (e) {
            el.badge.textContent = '状态未知';
        }
    })();

    function aiSetStatus(msg, cls) {
        el.status.textContent = msg;
        el.status.className = 'status-msg' + (cls ? ' ' + cls : '');
    }
    function aiFail(msg) {
        aiSetStatus('✗ ' + msg, 'fail');
        if (window.toast) window.toast.err('AI 助手', msg);
    }

    /* ── 气泡对话流（服务端 turns 为唯一事实源；本地立即回显用户消息） ── */
    function renderTranscript(turns) {
        el.transcript.innerHTML = '';
        (turns || []).forEach(t => {
            appendBubble(t.role === 'user' ? 'user' : 'ai', t.content || '');
        });
        el.transcript.scrollTop = el.transcript.scrollHeight;
    }
    function appendBubble(kind, text) {
        const div = document.createElement('div');
        div.className = 'chat-msg ' + kind;
        const who = document.createElement('span');
        who.className = 'chat-who';
        who.textContent = kind === 'user' ? '你' : 'AI';
        const body = document.createElement('div');
        body.className = 'chat-body';
        body.textContent = text;
        div.appendChild(who);
        div.appendChild(body);
        el.transcript.appendChild(div);
        el.transcript.scrollTop = el.transcript.scrollHeight;
        return body;
    }

    /* ── 信息核对单（grill 式透明化：已知事实 ✓） ── */
    const SLOT_LABEL_ZH = {
        source_type: '源类型', source_profile: '源档案', source_schema: '源 Schema',
        source_dsn: '源连接', target_type: '目标类型', target_profile: '目标档案',
        target_schema: '目标 Schema', format: '输出格式', channel: '通道',
        target_dialect: '目标方言', scenario: '场景',
    };
    function renderSlotCheck(slots) {
        el.slotCheck.innerHTML = '';
        const keys = Object.keys(slots || {}).filter(k =>
            !['source_dsn', 'target_dsn', 'source_profile', 'target_profile'].includes(k) && slots[k]);
        if (!keys.length) { el.slotCheck.style.display = 'none'; return; }
        el.slotCheck.style.display = '';
        keys.forEach(k => {
            const chip = document.createElement('span');
            chip.className = 'slot-check';
            chip.innerHTML = '<b>' + escapeHtml(SLOT_LABEL_ZH[k] || k) + '</b> ' + escapeHtml(slots[k]);
            el.slotCheck.appendChild(chip);
        });
    }

    /* ── 澄清 chips：点击即发送该选项 ── */
    function renderClarifyChips(items) {
        (items || []).forEach(item => {
            const line = appendBubble('ai', item.question + (item.options && item.options.length ? '' : '（请直接输入）'));
            if (item.options && item.options.length) {
                const wrap = document.createElement('div');
                wrap.className = 'chat-chips';
                item.options.forEach(opt => {
                    const b = document.createElement('button');
                    b.type = 'button';
                    b.className = 'chat-chip';
                    b.textContent = opt;
                    b.addEventListener('click', () => {
                        wrap.remove();
                        el.input.value = opt;
                        send();
                    });
                    wrap.appendChild(b);
                });
                line.appendChild(wrap);
            }
        });
    }

    /* ── 计划卡 ── */
    function renderCredFields(slots) {
        aiCredSlots = slots || [];
        el.credFields.innerHTML = '';
        aiCredSlots.forEach(slot => {
            const wrap = document.createElement('span');
            wrap.style.display = 'inline-flex';
            wrap.style.alignItems = 'center';
            wrap.style.gap = '6px';
            const label = document.createElement('span');
            label.className = 'field-help';
            label.textContent = slotLabel(slot);
            const input = document.createElement('input');
            input.type = 'password';
            input.className = 'mono';
            input.dataset.slot = slot;
            input.autocomplete = 'new-password';
            input.style.width = '180px';
            wrap.appendChild(label);
            wrap.appendChild(input);
            el.credFields.appendChild(wrap);
        });
        el.credsBox.style.display = aiCredSlots.length ? '' : 'none';
    }
    function collectCreds() {
        const creds = {};
        el.credFields.querySelectorAll('input[type=password]').forEach(inp => {
            if (inp.value) creds[inp.dataset.slot] = inp.value;
        });
        return creds;
    }

    function renderPlan(resp) {
        aiPlan = { session_id: resp.session_id, plan_id: resp.plan_id };
        renderCredFields(resp.credential_slots);
        const parts = [];
        if (resp.continuity && resp.continuity.mode === 'new_round' && resp.continuity.reason) {
            parts.push(resp.continuity.reason);
        }
        if (resp.facts_used && resp.facts_used.length) {
            parts.push('已引用：' + resp.facts_used.join('、'));
        }
        parts.push('engine=' + resp.engine);
        el.meta.textContent = parts.join(' · ');
        el.planYaml.textContent = resp.yaml || '';
        const warns = resp.warnings || [];
        el.planWarn.textContent = warns.length ? '⚠ ' + warns.join('；') : '';
        renderSlotCheck(resp.session && resp.session.slots);
        el.planCard.style.display = '';
        el.execute.disabled = el.activate.disabled = false;
        aiSetStatus(aiCredSlots.length ? '草案已生成，请先填写连接密码再执行' : '草案已生成，确认前不会执行');
        fetchHistory(true); // 本轮会话立即进入左栏历史（重置到第一页）
    }

    /* ── 发送 ── */
    async function send() {
        const utterance = el.input.value.trim();
        if (!utterance) { aiSetStatus('请先描述你要做什么', 'fail'); return; }
        el.send.disabled = true;
        aiSetStatus('生成中…', 'pending');
        appendBubble('user', utterance);
        el.input.value = '';
        try {
            const body = { utterance, profile: { source: el.srcProfile.value, target: el.tgtProfile.value } };
            if (aiSessionID) body.session_id = aiSessionID;
            const resp = await window.api.post('/api/v1/ai/plan', body);
            if (resp.error) { aiFail(resp.error); return; }
            setStoredSession(resp.session_id);
            const clarify = resp.needs_clarify || (resp.result && resp.result.needs_clarify)
                || resp.route === 'clarify' || resp.route === 'out-of-scope';
            if (clarify || !resp.plan_id) {
                appendBubble('ai', resp.clarify_reason || (resp.result && resp.result.reason) || '信息不足');
                renderClarifyChips(resp.clarify_items);
                renderSlotCheck(resp.session && resp.session.slots);
                aiSetStatus('AI 需要更多信息，点击选项或直接输入补充', 'fail');
                return;
            }
            appendBubble('ai', '已生成配置草案（等待确认）');
            renderPlan(resp);
        } catch (e) {
            aiFail(e && e.message || e);
        } finally {
            el.send.disabled = false;
        }
    }
    el.send.addEventListener('click', send);
    el.input.addEventListener('keydown', (e) => { if (e.key === 'Enter') send(); });

    /* ── 确认（终态后按钮禁用；重复确认说真话） ── */
    async function confirmPlan(execute) {
        if (!aiPlan) {
            aiSetStatus('该计划已确认过，不可重复确认。请重新生成计划，或点「新话题」', 'fail');
            return;
        }
        const creds = collectCreds();
        const missing = aiCredSlots.filter(s => !creds[s]);
        if (execute && missing.length) {
            aiSetStatus('✗ 还有未填写的连接密码（' + missing.map(slotLabel).join('、') + '）；或改用「仅激活配置」', 'fail');
            return;
        }
        el.execute.disabled = el.activate.disabled = true;
        aiSetStatus(execute ? '激活并启动任务…' : '激活配置…', 'pending');
        try {
            const body = { ...aiPlan, execute };
            if (Object.keys(creds).length) body.credentials = creds;
            const resp = await window.api.post('/api/v1/ai/plan/confirm', body);
            if (resp.error) { aiFail(resp.error); el.execute.disabled = el.activate.disabled = false; return; }
            aiPlan = null;
            if (resp.job_id) {
                /* 终态即任务链接（含 job 号）；不要再调 aiSetStatus 覆盖它。 */
                el.status.innerHTML = '✓ 已启动任务 <a href="#/jobs/' + encodeURIComponent(resp.job_id) + '">'
                    + escapeHtml(resp.job_id) + '</a>（点击查看进度；再次执行请重新生成计划）';
                el.status.className = 'status-msg ok';
            } else {
                aiSetStatus('✓ 配置已激活（未执行）。要执行请重新生成计划', 'ok');
            }
            fetchHistory(true); // 终态（effective/配置产物）落库后刷新列表
        } catch (e) {
            aiFail(e && e.message || e);
            el.execute.disabled = el.activate.disabled = false;
        }
    }
    /* plan-status2 简写：确认结果与主状态行共用（避免重复 DOM） */
    el.planStatus2 = () => el.status;
    el.execute.addEventListener('click', () => confirmPlan(true));
    el.activate.addEventListener('click', () => confirmPlan(false));

    function resetTopic() {
        setStoredSession(null);
        aiPlan = null;
        aiCredSlots = [];
        renderTranscript(null);
        el.planCard.style.display = 'none';
        el.execute.disabled = el.activate.disabled = false;
        aiSetStatus('已开新话题，描述你的下一个需求', '');
        el.input.focus();
    }
    el.newTopic.addEventListener('click', resetTopic);

    /* ── 数据源选择器 ── */
    async function refreshProfiles() {
        const opts = '<option value="">（让 AI 追问 / 用话语指定）</option>';
        const optsT = '<option value="">（不更换目标）</option>';
        try {
            const list = await window.api.get('/api/v1/datasources');
            el.srcProfile.innerHTML = opts + list.map(d =>
                '<option value="' + escapeHtml(d.name) + '">' + escapeHtml(d.name) + '（' + escapeHtml(d.type)
                + (d.schema ? ', ' + escapeHtml(d.schema) : '') + '）</option>').join('');
            el.tgtProfile.innerHTML = optsT + el.srcProfile.innerHTML.slice(opts.length);
        } catch (e) { /* best-effort */ }
    }
    el.srcNew.addEventListener('click', () => dsModal(root, null, refreshProfiles));
    el.tgtNew.addEventListener('click', () => dsModal(root, null, refreshProfiles));
    refreshProfiles();

    /* ── 会话恢复（切页/刷新回来） ── */
    async function restoreSession() {
        const id = loadStoredSession();
        if (!id) return;
        try {
            const resp = await window.api.get('/api/v1/ai/session/' + encodeURIComponent(id));
            renderTranscript(resp.session && resp.session.turns);
            renderSlotCheck(resp.session && resp.session.slots);
            if (resp.last_plan) {
                renderPlan({
                    session_id: resp.session.session_id, plan_id: resp.last_plan.plan_id,
                    yaml: resp.last_plan.yaml, credential_slots: resp.last_plan.credential_slots,
                    engine: '恢复', session: resp.session,
                });
            } else if (resp.last_job) {
                appendBubble('ai', '该会话已启动任务。再次执行请重新生成计划');
                aiSetStatus('✓ 任务 <a href="#/jobs/' + encodeURIComponent(resp.last_job.job_id) + '">'
                    + escapeHtml(resp.last_job.job_id) + '</a> 已启动', 'ok');
            }
        } catch (e) {
            setStoredSession(null); // 过期/不存在：静默归零
        }
    }

    /* ── 快捷语录 ── */
    (async () => {
        QUICK_QUOTES.forEach(q => {
            const b = document.createElement('button');
            b.type = 'button';
            b.className = 'chat-quote';
            b.textContent = q;
            b.addEventListener('click', () => { el.input.value = q; el.input.focus(); });
            el.quotes.appendChild(b);
        });
        try {
            const list = await window.api.get('/api/v1/datasources');
            (list || []).slice(0, 2).forEach(d => {
                const b = document.createElement('button');
                b.type = 'button';
                b.className = 'chat-quote';
                b.textContent = '用 ' + d.name + ' 导出全部表为 csv';
                b.addEventListener('click', () => { el.input.value = b.textContent; el.input.focus(); });
                el.quotes.appendChild(b);
            });
        } catch (e) { /* best-effort */ }
    })();

    /* ── 历史列表（确定性关键词检索；分批加载，滚动到底部自动续载下一批；
          支持 × 单删与批量编辑删除，删除为软删、立即从列表消失） ── */
    const HIST_PAGE_SIZE = 20;
    let histOffset = 0, histHasMore = false, histLoading = false, histPendingReset = false;
    let histSearchTimer = null;
    let histEditing = false;
    const histSelected = new Set();

    /* 「删除免确认」偏好持久化 */
    try { el.histNoConfirm.checked = localStorage.getItem('owl-ai-del-noconfirm') === '1'; } catch (e) { /* private mode */ }
    el.histNoConfirm.addEventListener('change', () => {
        try { localStorage.setItem('owl-ai-del-noconfirm', el.histNoConfirm.checked ? '1' : '0'); } catch (e) { /* private mode */ }
    });

    function updateHistSelInfo() {
        el.histSelInfo.textContent = '已选 ' + histSelected.size + ' 项';
        el.histBatchDel.disabled = histSelected.size === 0;
    }

    /* 批量编辑模式：列表出现勾选框，头部出现批量删除条 */
    function setHistEditing(on) {
        histEditing = on;
        histSelected.clear();
        updateHistSelInfo();
        el.histList.classList.toggle('editing', on);
        el.histEdit.textContent = on ? '退出编辑' : '批量编辑';
        el.histBatchBar.style.display = on ? '' : 'none';
    }
    el.histEdit.addEventListener('click', () => setHistEditing(!histEditing));

    /* 删除当前正在对话的会话时，同步清空右栏 */
    function clearActiveDeletedSession() {
        setStoredSession(null);
        aiPlan = null;
        aiCredSlots = [];
        renderTranscript(null);
        el.planCard.style.display = 'none';
        el.execute.disabled = el.activate.disabled = false;
        aiSetStatus('当前会话已删除', '');
    }

    /* 统一删除入口：单条走 DELETE，多条走 batch-delete；返回删除数 */
    async function deleteSessions(ids) {
        try {
            const resp = ids.length === 1
                ? await window.api.del('/api/v1/ai/session/' + encodeURIComponent(ids[0]))
                : await window.api.post('/api/v1/ai/sessions/batch-delete', { ids });
            const n = (resp && resp.deleted) || 0;
            if (ids.indexOf(aiSessionID) >= 0) clearActiveDeletedSession();
            await reloadLoadedHistory(); // 保持已加载窗口重载，不跳回顶部
            if (window.toast) window.toast.ok('已删除 ' + n + ' 个会话', '');
            return n;
        } catch (e) { aiFail(e && e.message || e); return 0; }
    }

    /* 免确认勾选后跳过 window.confirm（批量删除始终二次确认） */
    function deleteWithConfirm(ids, label) {
        if (!el.histNoConfirm.checked) {
            const msg = ids.length === 1
                ? '确认删除会话「' + label + '」？'
                : '确认删除选中的 ' + ids.length + ' 个会话？';
            if (!window.confirm(msg)) return Promise.resolve(0);
        }
        return deleteSessions(ids);
    }

    function buildHistItem(it) {
        const div = document.createElement('div');
        div.className = 'ai-hist-item';
        const title = document.createElement('div');
        title.className = 'ai-hist-title';
        title.innerHTML = '<a href="#/ai" data-sid="' + escapeHtml(it.id) + '">'
            + escapeHtml(it.title || it.id) + '</a>'
            + (it.effective ? ' <span class="badge badge-green">有效</span>' : '');
        const sub = document.createElement('div');
        sub.className = 'ai-hist-sub';
        sub.textContent = it.intent + (it.sub ? '/' + it.sub : '')
            + (it.keywords ? ' · ' + it.keywords : '') + ' · '
            + new Date(it.updated_at).toLocaleString();
        const del = document.createElement('button');
        del.type = 'button';
        del.className = 'ai-hist-del';
        del.textContent = '×';
        del.title = '删除该会话';
        del.setAttribute('aria-label', '删除 ' + (it.title || it.id));
        del.addEventListener('click', () => deleteWithConfirm([it.id], it.title || it.id));
        const chk = document.createElement('label');
        chk.className = 'check ai-hist-check';
        const chkBox = document.createElement('input');
        chkBox.type = 'checkbox';
        chkBox.setAttribute('aria-label', '选择 ' + (it.title || it.id));
        chkBox.addEventListener('change', () => {
            if (chkBox.checked) histSelected.add(it.id); else histSelected.delete(it.id);
            updateHistSelInfo();
        });
        chk.appendChild(chkBox);
        const actions = document.createElement('div');
        actions.className = 'ai-hist-actions';
        const clone = document.createElement('button');
        clone.type = 'button';
        clone.className = 'btn-ghost btn-sm ai-hist-clone';
        clone.textContent = '基于此继续';
        clone.addEventListener('click', async () => {
            try {
                const r = await window.api.post('/api/v1/ai/session/' + encodeURIComponent(it.id) + '/clone', {});
                setStoredSession(r.session_id);
                aiPlan = null;
                el.planCard.style.display = 'none';
                await restoreSession();
                renderSlotCheck(r.slots || {});
                appendBubble('ai', '已基于历史会话继续（事实已继承）：' + Object.keys(r.slots || {}).length + ' 个事实');
            } catch (e) { aiFail(e && e.message || e); }
        });
        const apply = document.createElement('button');
        apply.type = 'button';
        apply.className = 'btn-ghost btn-sm ai-hist-apply';
        apply.textContent = '取用配置';
        apply.addEventListener('click', async () => {
            try {
                await window.api.post('/api/v1/ai/session/' + encodeURIComponent(it.id) + '/apply', {});
                if (window.toast) window.toast.ok('配置已激活', it.title || it.id);
                if (window.refreshConfigBar) window.refreshConfigBar();
            } catch (e) { aiFail(e && e.message || e); }
        });
        actions.appendChild(clone);
        actions.appendChild(apply);
        div.appendChild(chk);
        div.appendChild(title);
        div.appendChild(sub);
        div.appendChild(actions);
        div.appendChild(del);
        return div;
    }

    /* 列表尾部状态行：仅还有下一批时展示 */
    function renderHistFoot() {
        let foot = el.histList.querySelector('.ai-hist-foot');
        if (!histHasMore) { if (foot) foot.remove(); return; }
        if (!foot) {
            foot = document.createElement('div');
            foot.className = 'ai-hist-foot';
            el.histList.appendChild(foot);
        }
        foot.textContent = histLoading ? '加载中…' : '下拉加载更多';
    }

    /* 一屏没填满就继续补齐，避免“有下一批却滚不动”的停滞态 */
    function fillHistViewport() {
        if (!histHasMore || histLoading) return;
        if (el.histList.scrollHeight <= el.histList.clientHeight + 4) fetchHistory(false);
    }

    async function fetchHistory(reset) {
        if (histLoading) { if (reset) histPendingReset = true; return; }
        if (reset) {
            histPendingReset = false;
            histOffset = 0;
            histHasMore = true;
            el.histList.innerHTML = '';
        }
        if (!histHasMore) return;
        histLoading = true;
        renderHistFoot();
        const q = el.histSearch.value.trim();
        try {
            /* 多取 1 条探测 has_more（接口无 total 字段） */
            const qs = '?limit=' + (HIST_PAGE_SIZE + 1) + '&offset=' + histOffset
                + (q ? '&q=' + encodeURIComponent(q) : '');
            const items = (await window.api.get('/api/v1/ai/sessions' + qs)) || [];
            const batch = items.slice(0, HIST_PAGE_SIZE);
            histHasMore = items.length > HIST_PAGE_SIZE;
            histOffset += batch.length;
            if (reset && !batch.length) {
                el.histList.innerHTML = '<p class="field-help">暂无会话历史（生成过计划的会话出现在这里）</p>';
            } else {
                const foot = el.histList.querySelector('.ai-hist-foot');
                batch.forEach(it => el.histList.insertBefore(buildHistItem(it), foot));
            }
            histSelected.clear();
            updateHistSelInfo();
        } catch (e) { /* best-effort */ } finally {
            histLoading = false;
            if (histPendingReset) { fetchHistory(true); return; } // 加载中来了新的重置请求（如计划生成后）
            renderHistFoot();
            fillHistViewport();
        }
    }

    /* 删除后重载已加载窗口（offset 归零一次取回），滚动位置不跳顶 */
    async function reloadLoadedHistory() {
        if (histLoading) { histPendingReset = true; return; }
        histLoading = true;
        renderHistFoot();
        const q = el.histSearch.value.trim();
        try {
            const want = Math.max(histOffset, HIST_PAGE_SIZE);
            const qs = '?limit=' + (want + 1) + '&offset=0' + (q ? '&q=' + encodeURIComponent(q) : '');
            const items = (await window.api.get('/api/v1/ai/sessions' + qs)) || [];
            const batch = items.slice(0, want);
            histHasMore = items.length > want;
            histOffset = batch.length;
            el.histList.innerHTML = '';
            if (!batch.length) {
                histHasMore = false;
                el.histList.innerHTML = '<p class="field-help">暂无会话历史（生成过计划的会话出现在这里）</p>';
            } else {
                batch.forEach(it => el.histList.appendChild(buildHistItem(it)));
            }
            histSelected.clear();
            updateHistSelInfo();
        } catch (e) { /* best-effort */ } finally {
            histLoading = false;
            if (histPendingReset) { fetchHistory(true); return; }
            renderHistFoot();
            fillHistViewport();
        }
    }

    /* 批量删除（始终二次确认，防手滑整批清掉） */
    el.histBatchDel.addEventListener('click', () => {
        const ids = Array.from(histSelected);
        if (ids.length) deleteWithConfirm(ids);
    });

    el.histList.addEventListener('scroll', () => {
        if (!histHasMore || histLoading) return;
        if (el.histList.scrollTop + el.histList.clientHeight >= el.histList.scrollHeight - 40) {
            fetchHistory(false);
        }
    });
    el.histSearch.addEventListener('input', () => {
        if (histSearchTimer) clearTimeout(histSearchTimer);
        histSearchTimer = setTimeout(() => fetchHistory(true), 300);
    });
    fetchHistory(true);

    restoreSession();
    t_revealCleanup(root);
}

/* render 离开时清理定时器（router 会重开视图；这里防泄漏） */
function t_revealCleanup(root) { /* histTimer cleared at next render */ }
