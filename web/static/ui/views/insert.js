/* owl-migrate SPA · INSERT generator view (ported from web/templates/insert.html) */
import { buildGeneratorView } from './generator.js';

export const render = buildGeneratorView({
    overline: 'generate · insert',
    title: '生成 INSERT',
    subtitle: '从 CSV 数据文件生成 INSERT SQL（离线，无需数据库）。数据目录取自配置的 import.source_dir',
    endpoint: '/api/v1/insert/generate',
    downloadLabel: 'INSERT',
    formHTML:
        '<div class="field">'
        +   '<label>每批行数 <code>batch_size</code></label>'
        +   '<input type="number" id="opt-batch-size" value="100" min="1" max="10000">'
        + '</div>'
        + '<div class="field">'
        +   '<label class="check"><input type="checkbox" id="opt-truncate"> INSERT 前加 <code>TRUNCATE TABLE</code></label>'
        + '</div>'
        + '<div class="field">'
        +   '<label class="check"><input type="checkbox" id="opt-no-quote"> 不引用标识符 <code>no_quote_identifiers</code></label>'
        + '</div>',
    collectOptions: (root) => ({
        batch_size: parseInt(root.querySelector('#opt-batch-size').value) || 100,
        truncate: root.querySelector('#opt-truncate').checked,
        no_quote_identifiers: root.querySelector('#opt-no-quote').checked || null
    }),
});
