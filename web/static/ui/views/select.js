/* owl-migrate SPA · SELECT generator view (ported from web/templates/select.html) */
import { buildGeneratorView, tablesFieldHTML, collectTables } from './generator.js';

export const render = buildGeneratorView({
    overline: 'generate · select',
    title: '生成 SELECT',
    subtitle: '生成分页查询语句（游标 / 偏移），用于手动导出数据',
    endpoint: '/api/v1/select/generate',
    downloadLabel: 'SELECT',
    formHTML:
        tablesFieldHTML('逗号分隔表名，支持 schema.table 与通配符，如 EMP,DEPT,T_*；留空 = 配置的表清单，均为空则全部表')
        + '<div class="field">'
        +   '<label>分页方式</label>'
        +   '<select id="opt-method">'
        +     '<option value="cursor">cursor（游标分页）</option>'
        +     '<option value="offset">offset（偏移分页）</option>'
        +   '</select>'
        + '</div>'
        + '<div class="field">'
        +   '<label>每页行数 <code>page_size</code></label>'
        +   '<input type="number" id="opt-page-size" value="5000" min="100" max="100000">'
        + '</div>'
        + '<div class="field">'
        +   '<label class="check"><input type="checkbox" id="opt-no-quote"> 不引用标识符 <code>no_quote_identifiers</code></label>'
        + '</div>',
    collectOptions: (root) => ({
        tables: collectTables(root),
        batch_method: root.querySelector('#opt-method').value,
        page_size: parseInt(root.querySelector('#opt-page-size').value) || 5000,
        no_quote_identifiers: root.querySelector('#opt-no-quote').checked ? true : null
    }),
});
