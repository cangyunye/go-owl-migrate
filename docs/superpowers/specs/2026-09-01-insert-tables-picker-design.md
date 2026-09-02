# INSERT 页检测表列表：摘要折叠 + 筛选 + 点击填入

日期：2026-09-01
状态：已批准（用户 2026-09-01 确认 1+3 组合方案）

## 背景

`web/static/ui/views/insert.js` 的 `showDetectedTables` 把 `GET /api/v1/insert/tables` 返回的表列表（扫描 `import.source_dir` CSV 目录推断）全量平铺进 `#detected-hint` 一段内（`<code>` 空格分隔）。表多时（>50）页面被拉长、不可读；旁边的"目标表"输入框需手填逗号分隔表名，列表本身不可交互。

## 决策（用户已确认）

1. **摘要折叠**：默认只显示"检测到 N 张表"，点"展开"才显示列表；滚动容器防长页。
2. **筛选**：展开后单框即时过滤（表名/Schema 子串，大小写不敏感）。
3. **点击填入（toggle）**：表名渲染为可点击 pill，已在目标表输入框中的高亮；点击加入/移除。

## 设计

范围：`web/static/ui/views/insert.js` + `web/static/css/style.css`。**零 API/后端改动**（`/api/v1/insert/tables` 响应结构不变）；`generator.js` 不动（`collectTables` 本就读 `#opt-tables` 输入框值）。

### 1. 摘要折叠

默认渲染：

```
数据目录 <code>xxx</code> 检测到 <span>N / N</span> 张表 · [展开]
```

点"展开"→ 显示 `detected-box`（筛选框 + 滚动列表容器 `max-height: 240px; overflow-y: auto`），按钮变"收起"；再点收起。展开状态不跨页面保留（每次进入默认折叠）。

### 2. 筛选

`detected-filter` 单框，输入即时 `renderPills`；匹配 `schema.table` 子串，大小写不敏感；计数显示 `x / N`（筛选后/总数）。

### 3. 点击填入（toggle）

- 每个表一个 `<button class="table-pill">`，文本 `schema.table`。
- 选中态（已在 `#opt-tables`，按规范化字符串集合判断）加 `.on` 类高亮。
- 点击：读 `#opt-tables` 值 → 逗号切分、trim、去空 → 含则移除、不含则追加 → 写回 → 重渲染 pills。
- `collectTables` 原样读输入框，生成请求自动携带所选表。

### 边界

- 只动 INSERT 页；DDL/SELECT 页只有输入框，不受影响。
- 空数据目录：保持现有"暂无 CSV"提示。
- 不做"全选/全不选"按钮（YAGNI）。
- 模块级 `detectedTables` 缓存每次 render 由 `afterRender: showDetectedTables` 重新拉取。

## 测试与验证

- `node --check`。
- 浏览器 e2e（headless Chromium/CDP，沿用既有方式）：60 表数据目录 → 摘要显示 60 张 → 展开 → 筛选收窄 → 点击 pill 填入 `#opt-tables`（再次点击移除）→ 点"生成"验证请求 tables 正确 → 收起/折叠正常。

## 明确不做（YAGNI）

- 全选/全不选。
- 服务端分页/筛选 API。
- 展开状态持久化。
