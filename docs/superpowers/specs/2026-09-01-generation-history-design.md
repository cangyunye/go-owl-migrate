# 生成历史记录：页面重载恢复 + 按次浏览 + 双限制保留

日期：2026-09-01
状态：已批准（用户 2026-09-01 确认方案）

## 背景与问题

1. `owl-migrate serve` 导出元数据后，刷新/重进页面结果面板消失——前端只在导出成功后才渲染，页面加载时不查询历史输出。文件与 DB 记录（`generation_outputs`）实际仍在。
2. 开了 `--token` 时，所有"下载 ZIP"链接是普通 `<a href>` 跳转，不带 `Authorization` 头 → 401。影响元数据导出、离线导出、配置下载、DDL/SELECT/INSERT 生成器、任务 SQL 输出全部下载端点。

## 已实施（本 spec 一部分，先落地）

### 下载 401 修复
- 后端 `withAuth`：路径以 `/download` 结尾的 API 路由额外接受 `?token=` 查询参数认证（与 WebSocket handshake 先例一致）；非下载路由、错误 token 仍 401。
- 前端 `web/static/js/app.js` 新增 `window.api.downloadURL(path)`：有 token 时追加 `?token=`（已有 `?` 则 `&`）。
- 更新全部 5 处下载入口：`exportMetadata.js`（元数据导出）、`export.js`（离线导出）、`config.js`（配置下载）、`generator.js`（DDL/SELECT/INSERT，`window.location`）、`migrate.js`（任务 SQL 输出）。
- 测试：`TestAuth_RequiresToken` 增补 download `?token=` 通过 / 错误 token 401 / 非下载路由带 `?token=` 仍 401。

## 目标（本 spec 主体）

元数据导出等生成结果：
- 页面重载后可见并可按次浏览、按次下载。
- 记录来源（哪个数据库 / DSN / 数据源）、导出时间。
- 条数 + 天数双限制保留，定时清理。

## 数据模型

`generation_outputs` 已有 `created_at TEXT DEFAULT (datetime('now'))`（UTC）。迁移补 3 列（沿用 `addNodeIDColumns` 的 `pragma_table_info` + `ALTER TABLE` 模式）：

```sql
ALTER TABLE generation_outputs ADD COLUMN source_label    TEXT NOT NULL DEFAULT '';
ALTER TABLE generation_outputs ADD COLUMN datasource_name TEXT NOT NULL DEFAULT '';
ALTER TABLE generation_outputs ADD COLUMN detail          TEXT NOT NULL DEFAULT '{}';
```

- `source_label`：`mysql@127.0.0.1:3306/SCOTT`。从 DSN 尽力解析 host:port + schema，密码必须剥掉；解析失败回退 `type + schema`。helper 放在 serve 包，配套单测（URL 式 / `user/pass@host:port/db` / 剥密码 / 回退）。
- `datasource_name`：导出走 `datasource:NAME` 时才填。
- `detail`：JSON，调用方自填 `{format, scope, table_count, file_count, size_bytes}`。
- 旧记录 `source_label` 为空 → UI 显示"未知来源"，不删。

## Store 层（internal/service/job.go）

- `RecordGeneration(kind, dir, meta, keep)`：签名扩展（meta = {SourceLabel, DatasourceName, Detail map}），插入写 3 新列；插入后顺带 prune。调用方：`internal/server/serve/generate.go` 的 `recordGenOutput`（集中一处）、测试两处直呼点。
- `PruneGenerations(kind, keep, maxAge) ([]string, error)`：双限制——条数超 `keep` 删最旧；`created_at` 早于 `now-maxAge` 删。沿用现有 SELECT→DELETE 模式（不用 RETURNING）。
- `ListGenerations(kind)`：按 id DESC。
- `GetGeneration(id)`：不存在 → `ErrNoGeneration`。
- 常量：`genOutputKeep = 10`（现有），`genOutputMaxAge = 7 * 24 * time.Hour`，kinds 集合 `{metadata, ddl, select, insert, export-offline}`。

## 清理时机

1. 记录时：`recordGenOutput` 现有路径（双限制）。
2. 启动时：`NewServer` 跑一次全 kinds prune，错误仅 stderr。
3. 每小时：`srv.CleanupLoop(ctx)`（ticker + `ctx.Done`），cmd/serve.go `go srv.CleanupLoop(ctx)`，与现有 heartbeat ticker 同模式，不改 Server 生命周期。

## API

```
GET /api/v1/generations?kind=metadata
    → {kind, items:[{id, created_at, source_label, datasource_name, detail,
                     file_count, size_bytes}]}
GET /api/v1/generations/{id}/files
    → {id, created_at, source_label, files:[{name, content}]}
GET /api/v1/{kind}/download?id=N        # 5 个 handleDownloadGen 端点；缺省 id = 最新（向后兼容）
```

- 下载 handler `handleDownloadGen(kind)` 加 `?id=`：有 id → `GetGeneration`（校验 kind 匹配）；无 → `LatestGeneration`。覆盖 metadata/export、export/offline、ddl、select、insert 5 个下载端点；config 下载（`handleGetConfigDownload`）与任务输出下载（`handleJobOutputDownload`）语义不同，不参与。
- `size_bytes` / `file_count` 列表时实时扫目录计算，不做冗余存储。
- 未知 id → 404；目录已被清理 → 对应错误消息。

## 前端

- **元数据导出页**（`exportMetadata.js`）：render 时 `GET /api/v1/generations?kind=metadata` → "历史导出"面板，每行 `时间(本地化) | 来源 | 格式 | 表/文件数 | 大小 | 浏览 | 下载`；浏览 = fetch files 渲染进现有 file-tabs 样式；下载 = `downloadURL('/api/v1/metadata/export/download?id=N')`。
- **generator.js（ddl/select/insert 共用）** + **export.js 离线导出**：同样加历史行（浏览复用文件列表渲染）；kind 从 endpoint 路径推导。

## 测试与验证

- store：Prune 双限制（条数 / 年龄各自触发）、List 排序、Get、meta 持久化。
- serve handler：列表空/有记录、files、`download?id=`（指定 vs 缺省最新）、未知 id → 404。
- `sourceLabel` 单测。
- 验证：`go test ./...`；重建二进制 + 临时 `OWL_MIGRATE_HOME` 冒烟（导出 → 列表 → 按 id 下载）；前端 `node --check`；浏览器手动走查元数据导出页。

## 明确不做（YAGNI）

- 目录内 manifest.json（DB 丢了文件可恢复）——无恢复逻辑，不做。
- 专用 `metadata_exports` 表——统一走 `generation_outputs`，覆盖全部 kind。
- 保留参数可配置化——先常量，需要时再加配置项。
