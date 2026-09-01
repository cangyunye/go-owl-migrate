# 元数据页表列表保留 + 客户端筛选/分页 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 元数据页（`#/metadata`）表列表跨页面切换自动恢复；超过 50 张表时支持客户端筛选（表名/Schema）与分页。

**Architecture:** 纯前端单文件改动。`web/static/ui/views/metadata.js` 引入模块级 `tablesCache`/`page` 状态，`renderTables` 改为"拉取→缓存→筛选→分页→DOM 构建"一条渲染路径；render() 末尾经 `/api/v1/config/status` 探测服务端模型后自动恢复列表。CSS 追加筛选工具条与分页条样式。

**Tech Stack:** 原生 JS ES modules（无框架）、现有 SPA（hash 路由，视图每次整页重建，模块级变量跨路由存活）。

## Global Constraints

- **零后端/API 改动**：不得改 Go 代码、不得改 API 契约。`/api/v1/metadata/tables` 返回结构不变（`{schema, name, columns[], primary_key[]}`）。
- `pageSize = 50`；筛选大小写不敏感，匹配表名或 Schema；筛选/翻页纯内存操作，不发请求。
- badge 语义：`匹配 x / 共 y 张`。
- 分页条 `filtered.length > pageSize` 时显示，否则隐藏。
- XSS：表行沿用现有 DOM API 构建（textContent，不插值）；不得引入 innerHTML 拼接用户/服务端数据。
- 静态资源 `go:embed`：前端改动需重建二进制后生效（e2e 从分支 worktree 构建）。
- 验证：`node --check` + browser 端到端（csv 元数据，无需数据库）。

---

### Task 1+2（合并）：metadata.js 状态/筛选/分页/自动恢复

**Files:**
- Modify: `web/static/ui/views/metadata.js`

**Interfaces:**
- Produces（模块级）：
  ```js
  let tablesCache = [];   // GET /api/v1/metadata/tables 全量
  let page = 1;
  const pageSize = 50;
  function applyFilterAndPage() {}   // 读 filterInput.value + page → 渲染 tbody/badge/pager
  async function autoRestoreTables() {}
  ```
  注：Task 1（筛选/分页）与 Task 2（自动恢复）共享同一条 `renderTables → applyFilterAndPage` 渲染路径且同文件紧密耦合，合并为一个实施/审查单元。

- [ ] **Step 1: 模块级状态**（`let dsnExamples = {};` 之后）

```js
let dsnExamples = {};

/* 表列表缓存与分页状态（模块级：跨路由切换存活） */
let tablesCache = [];
let page = 1;
const pageSize = 50;
```

- [ ] **Step 2: HTML 加筛选框与分页条**（替换现有 `tables-panel` 块，`+ '<div class="panel reveal" style="--i:2;display:none" id="tables-panel">'` 起至 `+ '</div>'` 止的整块）

```js
        + '<div class="panel reveal" style="--i:2;display:none" id="tables-panel">'
        +   '<div class="panel-head">'
        +     '<span class="panel-title">表列表 <span class="badge badge-accent" id="table-count"></span></span>'
        +   '</div>'
        +   '<div class="meta-toolbar">'
        +     '<input type="text" id="tables-filter" class="mono" placeholder="筛选表名 / Schema…">'
        +   '</div>'
        +   '<table class="data-table">'
        +     '<thead><tr><th>Schema</th><th>表名</th><th>列数</th><th>主键</th><th></th></tr></thead>'
        +     '<tbody id="tables-body"></tbody>'
        +   '</table>'
        +   '<div class="pager" id="tables-pager" style="display:none">'
        +     '<span class="field-help" id="pager-info" style="margin:0"></span>'
        +     '<button class="btn-ghost btn-sm" id="pager-prev" type="button">上一页</button>'
        +     '<button class="btn-ghost btn-sm" id="pager-next" type="button">下一页</button>'
        +   '</div>'
        + '</div>'
```

- [ ] **Step 3: 元素引用**（`const btnValidate = root.querySelector('#btn-validate');` 之后）

```js
    const filterInput = root.querySelector('#tables-filter');
    const pagerEl = root.querySelector('#tables-pager');
    const pagerInfo = root.querySelector('#pager-info');
    const pagerPrev = root.querySelector('#pager-prev');
    const pagerNext = root.querySelector('#pager-next');
```

- [ ] **Step 4: loadMeta 走统一渲染路径**（删除 `tableCount.textContent = resp.table_count + ' 张表';`，badge 由 applyFilterAndPage 设置）

```js
            const resp = await window.api.post('/api/v1/metadata/load', buildMetaPayload());
            tablesPanel.style.display = 'block';
            await renderTables();
```

- [ ] **Step 5: renderTables 重构 + applyFilterAndPage**（替换现有 renderTables 函数体；表行构建逻辑保持 DOM API 不变）

```js
    /* ── table list (DOM-built, XSS-safe) ────────────────────── */
    async function renderTables() {
        tablesCache = await window.api.get('/api/v1/metadata/tables') || [];
        page = 1;
        applyFilterAndPage();
    }

    function applyFilterAndPage() {
        const q = (filterInput.value || '').trim().toLowerCase();
        const filtered = q
            ? tablesCache.filter(t =>
                (t.schema || '').toLowerCase().includes(q) ||
                (t.name || '').toLowerCase().includes(q))
            : tablesCache.slice();

        tableCount.textContent = filtered.length + ' / ' + tablesCache.length;
        const totalPages = Math.max(1, Math.ceil(filtered.length / pageSize));
        if (page < 1) page = 1;
        if (page > totalPages) page = totalPages;
        const start = (page - 1) * pageSize;
        const rows = filtered.slice(start, start + pageSize);

        tbody.innerHTML = '';
        rows.forEach(t => {
            const tr = document.createElement('tr');

            const tSchema = document.createElement('td');
            tSchema.className = 'mono';
            tSchema.textContent = t.schema;
            tr.appendChild(tSchema);

            const tName = document.createElement('td');
            tName.className = 'mono';
            tName.style.color = 'var(--text)';
            tName.style.fontWeight = '600';
            tName.textContent = t.name;
            tr.appendChild(tName);

            const tCols = document.createElement('td');
            tCols.className = 'mono';
            tCols.textContent = (t.columns && t.columns.length) || 0;
            tr.appendChild(tCols);

            const tPk = document.createElement('td');
            tPk.className = 'mono';
            tPk.style.color = 'var(--text-2)';
            tPk.textContent = ((t.primary_key || []).join(', ')) || '—';
            tr.appendChild(tPk);

            const tAction = document.createElement('td');
            const link = document.createElement('a');
            link.href = '#';
            link.dataset.schema = t.schema;
            link.dataset.table = t.name;
            link.textContent = '详情';
            tAction.appendChild(link);
            tr.appendChild(tAction);

            tbody.appendChild(tr);
        });

        pagerInfo.textContent = '共 ' + filtered.length + ' 张 · 第 ' + page + '/' + totalPages + ' 页';
        pagerPrev.disabled = page <= 1;
        pagerNext.disabled = page >= totalPages;
        pagerEl.style.display = filtered.length > pageSize ? '' : 'none';
    }
```

- [ ] **Step 6: 事件接线**（`tbody.addEventListener('click', …)` 之后）

```js
    filterInput.addEventListener('input', () => { page = 1; applyFilterAndPage(); });
    pagerPrev.addEventListener('click', () => { page--; applyFilterAndPage(); });
    pagerNext.addEventListener('click', () => { page++; applyFilterAndPage(); });
```

- [ ] **Step 7: 挂载自动恢复**（`updateDsnHint();` 之后追加函数定义与调用）

```js
    /* ── auto-restore table list if metadata already loaded ──── */
    async function autoRestoreTables() {
        try {
            const st = await window.api.get('/api/v1/config/status');
            if (!st || !st.metadata_loaded) return;
            tablesPanel.style.display = 'block';
            await renderTables();
        } catch (e) { /* best-effort: leave the page blank */ }
    }

    toggleMetaFields();
    updateDsnHint();
    autoRestoreTables();
}
```

- [ ] **Step 8: 语法校验**

Run:
```bash
cp web/static/ui/views/metadata.js /tmp/metadata.mjs && node --check /tmp/metadata.mjs && echo OK
```
Expected: OK。

- [ ] **Step 9: 提交**

```bash
git add web/static/ui/views/metadata.js
git commit -m "feat(web): persist metadata table list across pages, client-side filter and pagination"
```

---

### Task 3: CSS + 端到端验证

**Files:**
- Modify: `web/static/css/style.css`

- [ ] **Step 1: CSS**（`style.css` 末尾追加）

```css
/* metadata table filter + pager */
.meta-toolbar { display: flex; align-items: center; gap: 10px; margin-bottom: 12px; }
.meta-toolbar input { max-width: 320px; }
.pager { display: flex; align-items: center; gap: 12px; margin-top: 12px; }
.pager .field-help { margin: 0; flex: 1; }
.pager button { min-width: 72px; }
```

- [ ] **Step 2: 生成 60 表 csv 元数据**（e2e 用，生成到 /tmp/meta60/）

```bash
mkdir -p /tmp/meta60 && cd /tmp/meta60
# 12 个文件仅表头（表头取自 testdata/csv）
for f in foreign_keys functions indexes mviews package_bodies packages primary_keys sequences synonyms triggers views; do :; done
printf 'TABLE_SCHEMA,TABLE_NAME,TABLE_TYPE,TABLE_COMMENT\n' > tables.csv
printf 'TABLE_SCHEMA,TABLE_NAME,COLUMN_NAME,ORDINAL_POSITION,DATA_TYPE,DATA_LENGTH,DATA_PRECISION,DATA_SCALE,NULLABLE,DEFAULT_VALUE,COLUMN_COMMENT\n' > columns.csv
for i in $(seq -w 0 59); do
  echo "SCOTT,T$i,TABLE," >> tables.csv
  echo "SCOTT,T$i,ID,1,NUMBER,22,4,0,NO,," >> columns.csv
done
for f in foreign_keys functions indexes mviews package_bodies packages primary_keys sequences synonyms triggers views; do
  printf 'TABLE_SCHEMA,TABLE_NAME\n' > "$f.csv"
done
wc -l tables.csv
```
Expected: tables.csv 61 行（1 表头 + 60 表）。

- [ ] **Step 3: 构建 + 起临时 serve**（分支 worktree）

```bash
go build -o /tmp/owl-migrate-meta ./cmd/migrate
rm -rf /tmp/owl-meta-e2e && OWL_MIGRATE_HOME=/tmp/owl-meta-e2e /tmp/owl-migrate-meta serve --port 18099 --temp-dir /tmp/owl-meta-e2e/temp >/tmp/owl-meta.log 2>&1 &
```
（无 token，浏览器访问免认证。）

- [ ] **Step 4: 浏览器端到端断言**（browser 工具，headless 打开 http://127.0.0.1:18099/#/metadata）
  1. 选择"CSV 目录"类型（默认即 csv），路径输入框填 `/tmp/meta60`，点"加载元数据"。
  2. 断言：badge 显示 `60 / 60`；tbody 行数 = 50；pager-info 含 `共 60 张 · 第 1/2 页`。
  3. 点"下一页"：行数 = 10；pager-info 含 `第 2/2 页`；"下一页"按钮 disabled。
  4. 筛选框输入 `T0`：badge `10 / 60`；行数 = 10（T00–T09）；pager 隐藏（10 ≤ 50）；清空筛选恢复 50 行第 1 页。
  5. 切到首页（`#/`）再切回 `#/metadata`：badge `60 / 60`，列表已自动恢复（无需点加载）。
  6. 点某行"详情"：详情面板出现。

- [ ] **Step 5: 清理**

```bash
pkill -f owl-migrate-meta; rm -rf /tmp/owl-meta-e2e /tmp/meta60 /tmp/owl-migrate-meta
```

- [ ] **Step 6: 提交**

```bash
git add web/static/css/style.css
git commit -m "style(web): toolbar and pager styles for metadata table list"
```
