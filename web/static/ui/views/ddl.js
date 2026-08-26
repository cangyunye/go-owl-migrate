/* owl-migrate SPA · DDL generator view (ported from web/templates/ddl.html) */
import { buildGeneratorView } from './generator.js';

export const render = buildGeneratorView({
    overline: 'generate · ddl',
    title: '生成 DDL',
    subtitle: '从已加载的元数据生成目标库建表语句 — 需先在「元数据」页加载',
    endpoint: '/api/v1/ddl/generate',
    downloadLabel: 'DDL',
    formHTML:
        '<div class="field">'
        +   '<label class="check"><input type="checkbox" id="opt-no-quote"> 不引用标识符 <code>no_quote_identifiers</code></label>'
        + '</div>',
    collectOptions: (root) => ({
        no_quote_identifiers: root.querySelector('#opt-no-quote').checked ? true : null
    }),
});
