/* owl-migrate SPA · export view (ported from web/templates/export.html) */
/* ============================================================
   Ports: online/offline tab toggle (switchMode), online
   startExport -> jobUI.start('/api/v1/export', {}), offline
   form (off-mode csv/xlsx toggle, format/dir/xlsx-path fields,
   startOffline -> api.post('/api/v1/export/offline', payload)),
   plus result rendering (file tabs + errors). File names and
   error text are HTML-escaped.

   jobUI is a singleton shared across views; its override methods
   are reset to the kernel originals at the top of every render so
   stale overrides never leak across navigation. This view does
   not override any of them, but resetting guarantees a prior
   migrate visit's onComplete/logLine/finish don't fire here.
   ============================================================ */

import { escapeHtml } from '../util.js';

/* Kernel originals — captured once at module load (app.js runs first). */
const ORIG_LOG_LINE = window.jobUI.logLine;
const ORIG_FINISH = window.jobUI.finish;
const ORIG_ON_COMPLETE = window.jobUI.onComplete;

export function render(root /*Element*/, params) {
    /* ── reset overrides: no cross-view leakage ────────────────── */
    window.jobUI.logLine = ORIG_LOG_LINE;
    window.jobUI.finish = ORIG_FINISH;
    window.jobUI.onComplete = ORIG_ON_COMPLETE;

    root.innerHTML = ''
        + '<div class="page-head reveal" style="--i:0">'
        +   '<div>'
        +     '<div class="overline">execute · export</div>'
        +     '<h1>导出数据</h1>'
        +     '<p class="subtitle">从源库导出数据为 CSV / SQL / XLSX — 使用当前配置的 source 与 export 段</p>'
        +   '</div>'
        + '</div>'

        + '<nav class="tabs reveal" style="--i:1">'
        +   '<button type="button" class="tab active" data-mode="online">在线导出</button>'
        +   '<button type="button" class="tab" data-mode="offline">离线转换</button>'
        + '</nav>'

        /* ── 在线导出 ─────────────────────────────────────────── */
        + '<div id="mode-online">'
        +   '<div class="panel reveal" style="--i:2">'
        +     '<div class="panel-head"><span class="panel-title">在线导出（需要源库连接）</span></div>'
        +     '<p class="panel-desc">游标分页读取源库，并行写入 CSV / SQL / XLSX。任务进度实时推送。</p>'
        +     '<div class="form-actions" style="border-top:none;margin-top:0;padding-top:0">'
        +       '<button class="btn-primary" id="btn-start" type="button">'
        +         '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M8 5v14l11-7z"/></svg>'
        +         '开始导出'
        +       '</button>'
        +       '<button class="btn-danger" id="btn-cancel" type="button" style="display:none">取消</button>'
        +     '</div>'
        +   '</div>'
        +   '<div id="progress-panel" class="panel reveal" style="--i:3;display:none">'
        +     '<div class="panel-head">'
        +       '<span class="panel-title">实时进度 <span class="badge badge-accent" id="job-id-badge"></span></span>'
        +       '<span class="live-dot"></span>'
        +     '</div>'
        +     '<div id="progress-log" class="term"></div>'
        +   '</div>'
        + '</div>'

        /* ── 离线转换 ─────────────────────────────────────────── */
        + '<div id="mode-offline" style="display:none">'
        +   '<div class="panel reveal" style="--i:2">'
        +     '<div class="panel-head"><span class="panel-title">离线转换（无需数据库）</span></div>'
        +     '<p class="panel-desc">从本地 CSV 目录或 XLSX 文件转换为目标格式。</p>'
        +     '<div class="field">'
        +       '<label>输入模式</label>'
        +       '<select id="off-mode">'
        +         '<option value="csv">CSV 目录</option>'
        +         '<option value="xlsx">XLSX 文件</option>'
        +       '</select>'
        +     '</div>'
        +     '<div class="field" id="f-off-csv">'
        +       '<label>CSV 数据目录</label>'
        +       '<input type="text" id="off-data-dir" class="mono" value="./output/data/" placeholder="./output/data/">'
        +       '<div class="field-help">包含 <code>{schema}.{table}.csv</code> 文件的目录</div>'
        +     '</div>'
        +     '<div class="field hidden" id="f-off-xlsx">'
        +       '<label>XLSX 文件路径</label>'
        +       '<input type="text" id="off-xlsx-path" class="mono" placeholder="./data.xlsx">'
        +     '</div>'
        +     '<div class="field">'
        +       '<label>输出格式</label>'
        +       '<select id="off-format">'
        +         '<option value="csv">CSV</option>'
        +         '<option value="sql">SQL（INSERT 语句）</option>'
        +         '<option value="xlsx">XLSX</option>'
        +       '</select>'
        +     '</div>'
        +     '<div class="form-actions">'
        +       '<button class="btn-primary" id="btn-offline" type="button">'
        +         '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M13 2 3 14h9l-1 8 10-12h-9l1-8z"/></svg>'
        +         '离线导出'
        +       '</button>'
        +       '<span id="off-status" class="status-msg"></span>'
        +     '</div>'
        +   '</div>'
        +   '<div class="panel reveal" style="--i:3;display:none" id="off-result">'
        +     '<div class="panel-head">'
        +       '<span class="panel-title">离线导出结果 <span class="badge badge-green" id="off-count"></span></span>'
        +       '<a class="btn-primary btn-sm" href="' + window.api.downloadURL('/api/v1/export/offline/download') + '">下载 ZIP</a>'
        +     '</div>'
        +     '<div id="off-files"></div>'
        +   '</div>'
        + '</div>'

        + '<div class="panel reveal" style="--i:4" id="off-history">'
        +   '<div class="panel-head"><span class="panel-title">历史离线导出 <span class="badge" id="offh-count"></span></span></div>'
        +   '<div id="offh-list" class="gen-history"></div>'
        + '</div>';

    function switchMode(m) {
        root.querySelectorAll('.tab[data-mode]').forEach(t =>
            t.classList.toggle('active', t.dataset.mode === m));
        root.querySelector('#mode-online').style.display = m === 'online' ? 'block' : 'none';
        root.querySelector('#mode-offline').style.display = m === 'offline' ? 'block' : 'none';
    }

    function toggleOfflineFields() {
        const mode = root.querySelector('#off-mode').value;
        root.querySelector('#f-off-csv').classList.toggle('hidden', mode !== 'csv');
        root.querySelector('#f-off-xlsx').classList.toggle('hidden', mode !== 'xlsx');
    }

    async function startExport() {
        try { await window.jobUI.start('/api/v1/export', {}); }
        catch (e) { window.toast.err('启动失败', e && e.message || ''); }
    }

    async function startOffline() {
        const status = root.querySelector('#off-status');
        status.textContent = '导出中…'; status.className = 'status-msg';
        root.querySelector('#btn-offline').disabled = true;
        try {
            const mode = root.querySelector('#off-mode').value;
            const payload = { format: root.querySelector('#off-format').value };
            if (mode === 'xlsx') payload.xlsx_path = root.querySelector('#off-xlsx-path').value;
            else payload.data_dir = root.querySelector('#off-data-dir').value;

            const resp = await window.api.post('/api/v1/export/offline', payload);
            root.querySelector('#off-result').style.display = 'block';
            root.querySelector('#off-count').textContent =
                resp.table_count + ' 张表 · ' + resp.total_rows + ' 行';
            const div = root.querySelector('#off-files');
            div.innerHTML = '<div class="file-tabs">'
                + (resp.files || []).map(f => '<span class="file-tab">' + escapeHtml(f.name) + '</span>').join('')
                + '</div>';
            if (resp.errors && resp.errors.length) {
                div.innerHTML += '<p class="status-msg fail" style="margin-top:10px">'
                    + resp.errors.map(escapeHtml).join('<br>') + '</p>';
            }
            status.textContent = '✓ 完成'; status.className = 'status-msg ok';
            window.toast.ok('离线导出完成', resp.table_count + ' 张表，' + resp.total_rows + ' 行');
        } catch (e) {
            status.textContent = '✗ ' + (e && e.message || ''); status.className = 'status-msg fail';
            window.toast.err('离线导出失败', e && e.message || '');
        } finally {
            root.querySelector('#btn-offline').disabled = false;
        }
    }

    const offHistList = root.querySelector('#offh-list');
    const offHistCount = root.querySelector('#offh-count');
    (async function loadOffHistory() {
        try {
            const data = await window.api.get('/api/v1/generations?kind=export-offline');
            const items = data.items || [];
            offHistCount.textContent = items.length + ' 次';
            if (!items.length) { offHistList.innerHTML = '<p class="field-help">暂无历史</p>'; return; }
            offHistList.innerHTML = items.map(it => {
                const t = it.created_at ? new Date(it.created_at.replace(' ', 'T') + 'Z').toLocaleString() : '—';
                const src = it.source_label || '未知来源';
                return '<div class="gen-row">'
                    + '<span class="gen-time">' + escapeHtml(t) + '</span>'
                    + '<span class="gen-src">' + escapeHtml(src) + '</span>'
                    + '<span class="gen-meta">' + (it.file_count || 0) + ' 文件</span>'
                    + '<span class="gen-size">' + (window.humanSize ? window.humanSize(it.size_bytes) : (it.size_bytes + ' B')) + '</span>'
                    + '<span class="gen-actions">'
                    +   '<a href="#" data-browse="' + it.id + '">浏览</a>'
                    +   '<a href="' + window.api.downloadURL('/api/v1/export/offline/download?id=' + it.id) + '">下载</a>'
                    + '</span>'
                    + '</div>';
            }).join('');
            offHistList.querySelectorAll('a[data-browse]').forEach(a => {
                a.addEventListener('click', async (e) => {
                    e.preventDefault();
                    try {
                        const f = await window.api.get('/api/v1/generations/' + a.dataset.browse + '/files');
                        const files = f.files || [];
                        root.querySelector('#off-files').innerHTML = files.map(x => '<div class="file-tab">'
                            + escapeHtml(x.name) + '</div>').join('');
                    } catch (err) {
                        window.toast && window.toast.err('读取历史失败', (err && err.message) || '');
                    }
                });
            });
        } catch (e) { /* history is best-effort */ }
    })();

    root.querySelectorAll('.tab[data-mode]').forEach(t =>
        t.addEventListener('click', () => switchMode(t.dataset.mode)));
    root.querySelector('#off-mode').addEventListener('change', toggleOfflineFields);
    root.querySelector('#btn-start').addEventListener('click', startExport);
    root.querySelector('#btn-cancel').addEventListener('click', () => window.jobUI.cancel());
    root.querySelector('#btn-offline').addEventListener('click', startOffline);

    window.jobUI.bind('#progress-log');

    switchMode('online');
}
