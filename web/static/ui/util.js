/* owl-migrate SPA · shared view helpers (ES module) */

function localEscape(s) {
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

/* Reuse the kernel escapeHtml exposed on window by app.js; fall back to a
   local copy so the views stay safe even if app.js is not loaded yet. */
export function escapeHtml(s) {
    if (s === null || s === undefined) return '';
    if (typeof window !== 'undefined' && typeof window.escapeHtml === 'function') {
        return window.escapeHtml(s);
    }
    return localEscape(s);
}

/* Status badge — matches the SSR templates' map exactly:
   running/cancelling get a pulsing dot; others are steady. The raw API
   value stays in the title attribute; the visible label is Chinese. */
const STATUS_LABELS = {
    running: '运行中', cancelling: '取消中', completed: '已完成',
    completed_with_errors: '部分失败', failed: '失败',
    interrupted: '已中断', cancelled: '已取消'
};

export function statusBadge(s) {
    const map = {
        running: ['st-run', true], cancelling: ['st-warn', true],
        completed: ['st-ok', false], completed_with_errors: ['st-warn', false],
        failed: ['st-fail', false],
        interrupted: ['st-warn', false], cancelled: ['st-warn', false]
    };
    const m = map[s] || ['st-run', false];
    const label = STATUS_LABELS[s] || escapeHtml(s);
    return '<span class="' + m[0] + '" title="' + escapeHtml(s) + '"><span class="status-dot' + (m[1] ? ' pulse' : '') + '"></span>' + label + '</span>';
}

const FOCUSABLE = 'a[href], button:not([disabled]), textarea, input, select, [tabindex]:not([tabindex="-1"])';

/* modalFocus keeps Tab cycling inside a dialog overlay and restores focus to
   the trigger on close. Pair every open with .open() and every close path
   with .close(). */
export function modalFocus(overlay) {
    let prev = null;
    function onKey(e) {
        if (e.key !== 'Tab') return;
        const items = Array.from(overlay.querySelectorAll(FOCUSABLE))
            .filter(el => el.offsetParent !== null);
        if (!items.length) return;
        const first = items[0];
        const last = items[items.length - 1];
        if (e.shiftKey && document.activeElement === first) {
            e.preventDefault();
            last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
            e.preventDefault();
            first.focus();
        }
    }
    return {
        open() {
            prev = document.activeElement;
            overlay.addEventListener('keydown', onKey);
        },
        close() {
            overlay.removeEventListener('keydown', onKey);
            if (prev && typeof prev.focus === 'function') prev.focus();
            prev = null;
        },
    };
}
