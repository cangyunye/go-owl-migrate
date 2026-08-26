/* owl-migrate SPA · import view (ported from web/templates/import.html) */
/* ============================================================
   Ports: startImport -> jobUI.start('/api/v1/import', {}), plus
   cancel (btn-cancel) and the progress term (#progress-log).

   jobUI is a singleton shared across views; its override methods
   are reset to the kernel originals at the top of every render so
   stale overrides never leak across navigation. This view does
   not override any of them, but resetting guarantees a prior
   migrate visit's onComplete/logLine/finish don't fire here.
   ============================================================ */

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
        +     '<div class="overline">execute · import</div>'
        +     '<h1>导入数据</h1>'
        +     '<p class="subtitle">将 CSV 数据导入目标库 — 使用当前配置的 import 与 target 段，支持断点续传</p>'
        +   '</div>'
        + '</div>'

        + '<div class="panel reveal" style="--i:1">'
        +   '<div class="panel-head"><span class="panel-title">导入任务</span></div>'
        +   '<p class="panel-desc">批量事务写入目标库。失败后可在「任务」页基于检查点恢复，已完成的表将自动跳过。</p>'
        +   '<div class="form-actions" style="border-top:none;margin-top:0;padding-top:0">'
        +     '<button class="btn-primary" id="btn-start" type="button">'
        +       '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M8 5v14l11-7z"/></svg>'
        +       '开始导入'
        +     '</button>'
        +     '<button class="btn-danger" id="btn-cancel" type="button" style="display:none">取消</button>'
        +   '</div>'
        + '</div>'

        + '<div id="progress-panel" class="panel reveal" style="--i:2;display:none">'
        +   '<div class="panel-head">'
        +     '<span class="panel-title">实时进度 <span class="badge badge-accent" id="job-id-badge"></span></span>'
        +     '<span class="live-dot"></span>'
        +   '</div>'
        +   '<div id="progress-log" class="term"></div>'
        + '</div>';

    async function startImport() {
        try { await window.jobUI.start('/api/v1/import', {}); }
        catch (e) { window.toast.err('启动失败', e && e.message || ''); }
    }

    root.querySelector('#btn-start').addEventListener('click', startImport);
    root.querySelector('#btn-cancel').addEventListener('click', () => window.jobUI.cancel());

    window.jobUI.bind('#progress-log');
}
