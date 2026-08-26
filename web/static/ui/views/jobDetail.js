/* owl-migrate SPA · job detail view (full implementation in Task 6) */
export function render(root /*Element*/, params) {
    const id = params && params[0] ? params[0] : '';
    root.innerHTML = ''
        + '<div class="page-head"><h1>任务详情</h1></div>'
        + '<div class="panel"><p>任务 <code>' + id + '</code> 详情视图占位 — 后续任务实现。</p></div>';
}
