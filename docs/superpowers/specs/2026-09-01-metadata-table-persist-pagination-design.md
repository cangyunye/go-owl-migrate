# 元数据页：表列表跨页保留 + 客户端筛选/分页

日期：2026-09-01
状态：已批准（用户 2026-09-01 确认方案与三个决策点）

## 背景

`web/static/ui/views/metadata.js` 的 `render()` 每次进入页面整页重建：表格面板 `display:none`、表列表清空，只有点击"加载元数据"才渲染。切换页面再回来 = 全部丢失，而元数据页是最重的页面（DSN 拼装、预填、加载）。

服务端 `s.schemaModel` 在 `POST /api/v1/metadata/load` 后一直保留，`GET /api/v1/metadata/tables` 无需重连、毫秒级返回——前端只是没有在挂载时调用。`GET /api/v1/config/status` 返回 `metadata_loaded` 布尔信号。

表列表当前是纯 `<tbody>` 全量渲染，无分页无筛选；真实 schema 常超 50 张表。

## 决策（用户已确认）

1. 表列表保留：**挂载自动恢复**——进页面时若服务端已加载元数据，自动拉表并显示。
2. 分页/筛选：**客户端筛选 + 分页**——筛选框（表名/Schema，大小写不敏感）+ 50/页翻页，纯前端。
3. 筛选维度：**表名 + Schema 单框**。

## 设计

范围：`web/static/ui/views/metadata.js` + `web/static/css/style.css`（分页条样式）。**零后端/API 改动**。

### 1. 挂载自动恢复（约 15 行）

- render() 末尾（config prefill 之后）：`GET /api/v1/config/status` → `metadata_loaded` 为真 → `GET /api/v1/metadata/tables` → 填充模块级缓存 `tablesCache`、显示表格面板 + 总数 badge、渲染列表。
- 任一环节失败（未加载/网络）→ 静默保持现状空白。
- 与"加载元数据"（loadMeta → renderTables）复用同一条渲染路径。

### 2. 客户端筛选 + 分页（约 60 行）

模块级状态（ES module 单例，跨路由存活）：

```js
let tablesCache = [];   // GET /api/v1/metadata/tables 全量
let page = 1;
const pageSize = 50;
```

UI（tables-panel 内）：

- 筛选输入框：`placeholder="筛选表名 / Schema…"`，`input` 事件即时过滤（大小写不敏感，匹配任一字段），过滤后重置 `page = 1`。
- badge：`匹配 x / 共 y 张`。
- 分页条（footer）：`共 N 张 · 第 i/y 页 · [上一页] [下一页]`，边界按钮禁用；`N ≤ 50` 时不显示。
- 筛选/翻页纯内存操作，不发请求。
- 详情链接沿用现有 `data-schema/data-table` + tbody 委托（重渲染后监听器仍在）。

渲染路径：`renderTables()` 拉取/写入 `tablesCache` → `applyFilterAndPage()` 计算当前页行 → DOM 构建（沿用现有 DOM API 构建，XSS 安全）。

### 边界与行为

- 未加载元数据时挂载恢复静默。
- 恢复的列表来自服务端当前模型；用户重新加载（loadMeta）覆盖缓存。
- badge 语义从"table_count 张表"变为"匹配/总数"。
- DDL/SELECT/INSERT 页的表选择是独立逗号分隔输入框，不受影响。

## 测试与验证

- 无后端改动 → 无 Go 测试。
- `node --check` 语法校验。
- 端到端（browser 工具 + 临时 OWL_MIGRATE_HOME serve + csv 元数据，无需数据库）：
  - 生成 60 张表的 csv 元数据目录（`tables.csv`/`columns.csv` 等）→ 加载 → 断言 60 张、第 1 页 50 行、下一页 10 行；
  - 筛选输入收窄列表、重置页码；
  - 切到其它页面再回元数据页 → 列表仍在。

## 明确不做（YAGNI）

- 服务端分页 API（page/limit/q 参数）——数据集是单 schema 表元数据，客户端过滤无压力。
- 多维度筛选（列数等）。
- 缓存持久化（sessionStorage/localStorage）——模块级缓存 + 挂载恢复已覆盖会话内与刷新场景（刷新走服务端模型恢复）。
- 恢复时骨架屏/加载态——一次 GET 毫秒级，无需。
