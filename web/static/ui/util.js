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
   running/cancelling get a pulsing dot; others are steady. */
export function statusBadge(s) {
    const map = {
        running: ['st-run', true], cancelling: ['st-warn', true],
        completed: ['st-ok', false], failed: ['st-fail', false],
        interrupted: ['st-warn', false], cancelled: ['st-warn', false]
    };
    const m = map[s] || ['st-run', false];
    const label = escapeHtml(s);
    return '<span class="' + m[0] + '"><span class="status-dot' + (m[1] ? ' pulse' : '') + '"></span>' + label + '</span>';
}
