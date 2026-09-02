# INSERT 页检测表选择器 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** INSERT 生成页的检测表列表改为"摘要折叠 + 展开筛选 + 点击 toggle 填入目标表输入框"，多表时页面不失控。

**Architecture:** 纯前端。`web/static/ui/views/insert.js` 的 `showDetectedTables` 重构为摘要 + 可展开容器；模块级 `detectedTables` 缓存；`renderPills` 负责筛选与 toggle 渲染，直接读写 `#opt-tables`（`collectTables` 零改动）。CSS 追加 pill/容器样式。

**Tech Stack:** 原生 JS ES modules（无框架）、现有 SPA。

## Global Constraints

- **零 API/后端改动**：`/api/v1/insert/tables` 响应结构不变（`{data_dir, tables:[{schema,name,columns}]}`）。
- **`generator.js` 不动**：`collectTables` 原样读 `#opt-tables` 输入框值。
- toggle 语义：`#opt-tables` 按逗号切分、trim、去空；含则移除、不含则追加；去重按规范化字符串。
- 筛选大小写不敏感，匹配 `schema.table` 子串；计数 `x / N`。
- 展开状态不持久化（每次 render 默认折叠）；滚动容器 `max-height: 240px; overflow-y: auto`。
- XSS：pill 文本用 `textContent`；`escapeHtml` 用于模板串内插值。
- 静态资源 `go:embed`：e2e 需从分支 worktree 构建二进制。
- 验证：`node --check` + 浏览器 e2e（headless Chromium/CDP）。

---

### Task 1: insert.js 检测表选择器

**Files:**
- Modify: `web/static/ui/views/insert.js`

**Interfaces:**
- Produces（模块级）：
  ```js
  let detectedTables = [];            // [{schema, name, columns}]，每次 render 重拉
  function getTargetTables(root) {}   // → string[]（#opt-tables 规范化）
  function setTargetTables(root, list) {}
  function renderPills(root) {}       // 读筛选框 + detectedTables → 渲染 pill 列表 + 计数
  ```

- [ ] **Step 1: 模块级状态**（`import` 之后）

```js
/* 检测表列表缓存（模块级：每次 render 由 showDetectedTables 重拉） */
let detectedTables = [];
```

- [ ] **Step 2: 替换 showDetectedTables 并新增辅助函数**（整段替换现有 `showDetectedTables`）

```js
function getTargetTables(root) {
    const el = root.querySelector('#opt-tables');
    if (!el) return [];
    return (el.value || '').split(',').map(s => s.trim()).filter(Boolean);
}

function setTargetTables(root, list) {
    const el = root.querySelector('#opt-tables');
    if (el) el.value = list.join(',');
}

function renderPills(root) {
    const listEl = root.querySelector('#detected-list');
    const filterEl = root.querySelector('#detected-filter');
    if (!listEl || !filterEl) return;
    const q = (filterEl.value || '').trim().toLowerCase();
    const current = new Set(getTargetTables(root));
    const filtered = detectedTables.filter(t => {
        const label = (t.schema + '.' + t.name).toLowerCase();
        return !q || label.includes(q);
    });
    listEl.innerHTML = '';
    filtered.forEach(t => {
        const label = t.schema + '.' + t.name;
        const on = current.has(label);
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'table-pill' + (on ? ' on' : '');
        btn.textContent = label;
        btn.title = (t.columns != null ? t.columns + ' 列' : '') + (on ? ' · 点击移除' : ' · 点击加入');
        btn.addEventListener('click', () => {
            const list = getTargetTables(root);
            const i = list.indexOf(label);
            if (i >= 0) list.splice(i, 1); else list.push(label);
            setTargetTables(root, list);
            renderPills(root);
        });
        listEl.appendChild(btn);
    });
    const count = root.querySelector('#detected-count');
    if (count) count.textContent = filtered.length + ' / ' + detectedTables.length;
}

async function showDetectedTables(root) {
    const hint = root.querySelector('#detected-hint');
    if (!hint) return;
    try {
        const resp = await window.api.get('/api/v1/insert/tables') || {};
        if (!root.isConnected) return;
        detectedTables = resp.tables || [];
        if (detectedTables.length) {
            hint.innerHTML = '数据目录 <code>' + escapeHtml(resp.data_dir || '') + '</code> 检测到 '
                + '<span id="detected-count">' + detectedTables.length + ' / ' + detectedTables.length + '</span> 张表'
                + ' <button class="btn-ghost btn-sm" id="detected-toggle" type="button">展开</button>'
                + '<div class="detected-box" id="detected-box" style="display:none">'
                +   '<div class="detected-toolbar">'
                +     '<input type="text" id="detected-filter" class="mono" placeholder="筛选表名 / Schema…">'
                +   '</div>'
                +   '<div class="detected-list" id="detected-list"></div>'
                + '</div>';
            const toggle = hint.querySelector('#detected-toggle');
            const box = hint.querySelector('#detected-box');
            const filterEl = hint.querySelector('#detected-filter');
            toggle.addEventListener('click', () => {
                const open = box.style.display === 'none';
                box.style.display = open ? '' : 'none';
                toggle.textContent = open ? '收起' : '展开';
                if (open) renderPills(root);
            });
            filterEl.addEventListener('input', () => renderPills(root));
        } else {
            hint.textContent = '数据目录 ' + (resp.data_dir || '') + ' 暂无 CSV（' + (resp.error || '请先在导出页导出数据') + '）';
        }
    } catch (e) { /* hint is optional */ }
}
```

- [ ] **Step 3: 语法校验**

Run:
```bash
cp web/static/ui/views/insert.js /tmp/insert.mjs && node --check /tmp/insert.mjs && echo OK
```
Expected: OK。

- [ ] **Step 4: 提交**

```bash
git add web/static/ui/views/insert.js
git commit -m "feat(web): insert detected-tables picker with collapse, filter and click-to-fill"
```

---

### Task 2: CSS + 端到端验证

**Files:**
- Modify: `web/static/css/style.css`

- [ ] **Step 1: CSS**（`style.css` 末尾追加）

```css
/* insert page detected tables picker */
.detected-box { margin-top: 10px; }
.detected-toolbar { display: flex; align-items: center; gap: 10px; margin-bottom: 8px; }
.detected-toolbar input { max-width: 280px; }
.detected-list {
    display: flex; flex-wrap: wrap; gap: 6px;
    max-height: 240px; overflow-y: auto; padding: 2px;
}
.table-pill {
    display: inline-block; padding: 4px 10px; font-size: 12px;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    border: 1px solid var(--border, #444); border-radius: 999px;
    background: transparent; color: var(--text, #ddd); cursor: pointer;
}
.table-pill:hover { border-color: var(--accent, #4da3ff); }
.table-pill.on { background: var(--accent, #4da3ff); border-color: var(--accent, #4da3ff); color: #fff; }
```

- [ ] **Step 2: 生成 60 表 csv 数据目录**（e2e 用，`/tmp/insert60/`；与元数据页 e2e 同款格式：`{schema}.{table}.csv`，每文件含 1 行表头即可，如 `SCOTT.T00.csv` 内容 `ID\n`；需 60 个文件）

```bash
mkdir -p /tmp/insert60 && cd /tmp/insert60
for i in $(seq -w 0 59); do printf 'ID\n' > "SCOTT.T$i.csv"; done
ls | wc -l
```
Expected: 60。

- [ ] **Step 3: 构建 + 起临时 serve**（分支 worktree；配置 `import.source_dir=/tmp/insert60`）

```bash
go build -o /tmp/owl-migrate-ins ./cmd/migrate
rm -rf /tmp/owl-ins-e2e && OWL_MIGRATE_HOME=/tmp/owl-ins-e2e /tmp/owl-migrate-ins serve --port 18099 --temp-dir /tmp/owl-ins-e2e/temp >/tmp/owl-ins.log 2>&1 &
```
（e2e 前需把配置的 import.source_dir 指到 /tmp/insert60：可经 `PUT /api/v1/config` 或直接写 `/tmp/owl-ins-e2e/migrate.yaml` 再启动；验证代理选最简路径。）

- [ ] **Step 4: 浏览器端到端断言**（headless Chromium/CDP，打开 `http://127.0.0.1:18099/#/insert`）
  1. 断言摘要：`检测到 60 / 60 张表`，无平铺 pill（列表容器隐藏）。
  2. 点"展开"：`#detected-list` 出现 60 个 pill；计数 `60 / 60`。
  3. 筛选框输入 `T0`：pill 数 = 10；计数 `10 / 60`。
  4. 点 `SCOTT.T00` pill：`#opt-tables` 值为 `SCOTT.T00`，该 pill 有 `.on` 高亮；再点一次 → 输入框清空、高亮消失（toggle）。
  5. 依次点 `SCOTT.T01`、`SCOTT.T02`：输入框值 `SCOTT.T01,SCOTT.T02`；重复点 `SCOTT.T01` → 变为 `SCOTT.T02`。
  6. 清空筛选：计数回 `60 / 60`；点"生成"：请求体 `tables` 为 `SCOTT.T02`（或当前输入框值）；生成成功返回文件列表。
  7. 点"收起"：列表容器隐藏。

- [ ] **Step 5: 清理**

```bash
pkill -f owl-migrate-ins; rm -rf /tmp/owl-ins-e2e /tmp/insert60 /tmp/owl-migrate-ins
```

- [ ] **Step 6: 提交**

```bash
git add web/static/css/style.css
git commit -m "style(web): detected-tables picker styles for insert page"
```
