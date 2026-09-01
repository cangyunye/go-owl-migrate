/* owl-migrate SPA · INSERT generator view (ported from web/templates/insert.html) */
import { buildGeneratorView, tablesFieldHTML, collectTables } from './generator.js';
import { escapeHtml } from '../util.js';

async function showDetectedTables(root) {
    const hint = root.querySelector('#detected-hint');
    if (!hint) return;
    try {
        const resp = await window.api.get('/api/v1/insert/tables') || {};
        if (!root.isConnected) return;
        const names = (resp.tables || []).map(t => t.schema + '.' + t.name);
        if (names.length) {
            hint.innerHTML = '数据目录 <code>' + escapeHtml(resp.data_dir || '') + '</code> 检测到 '
                + names.length + ' 张表：' + names.map(n => '<code>' + escapeHtml(n) + '</code>').join(' ');
        } else {
            hint.textContent = '数据目录 ' + (resp.data_dir || '') + ' 暂无 CSV（' + (resp.error || '请先在导出页导出数据') + '）';
        }
    } catch (e) { /* hint is optional */ }
}

export const render = buildGeneratorView({
    overline: 'generate · insert',
    title: '生成 INSERT',
    subtitle: '从 CSV 数据文件生成 INSERT SQL（离线，无需数据库）。数据目录取自配置的 import.source_dir',
    endpoint: '/api/v1/insert/generate',
    downloadLabel: 'INSERT',
    formHTML:
        '<div class="field-help" id="detected-hint" style="margin-bottom:12px"></div>'
        + tablesFieldHTML('逗号分隔表名，留空 = 配置的表清单；实际以数据目录中的 CSV 为准')
        + '<div class="field">'
        +   '<label>每批行数 <code>batch_size</code></label>'
        +   '<input type="number" id="opt-batch-size" value="100" min="1" max="10000">'
        + '</div>'
        + '<div class="field">'
        +   '<label class="check"><input type="checkbox" id="opt-truncate"> INSERT 前加 <code>TRUNCATE TABLE</code></label>'
        + '</div>'
        + '<div class="field">'
        +   '<label class="check"><input type="checkbox" id="opt-no-quote"> 不引用标识符 <code>no_quote_identifiers</code></label>'
        + '</div>',
    afterRender: showDetectedTables,
    collectOptions: (root) => ({
        tables: collectTables(root),
        batch_size: parseInt(root.querySelector('#opt-batch-size').value) || 100,
        truncate: root.querySelector('#opt-truncate').checked,
        no_quote_identifiers: root.querySelector('#opt-no-quote').checked || null
    }),
});
