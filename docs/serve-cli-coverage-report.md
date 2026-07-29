# owl-migrate serve 功能覆盖报告

> 评审日期：2026-07-29
> 目的：对比 `owl-migrate serve` Web 服务与命令行基础功能的覆盖情况

---

## 一、总体结论

serve 模式已覆盖 CLI 全部 **10/10** 个命令，包括辅助诊断类命令。
Web 端额外提供了 CLI 不具备的任务管理、实时进度、配置库等能力。

| 维度 | 状态 |
|------|------|
| 核心迁移链路（DDL/SELECT/INSERT/Export/Import/Migrate） | ✅ 全部覆盖 |
| 配置管理（init 等价） | ✅ 场景化表单替代 |
| 元数据校验（validate） | ✅ 覆盖 |
| 辅助诊断（show-query / export-metadata） | ✅ 已覆盖（2026-07-29 补充） |
| CLI 细粒度参数（flags） | ✅ 已覆盖（请求级 override） |

---

## 二、逐命令对比

### 2.1 init — 配置生成

| CLI | serve |
|-----|-------|
| 交互式问答 + `--scenario` + flags 非交互 | `GET /api/v1/scenarios` + `POST /api/v1/scenarios/{name}/build` |
| 支持 6 种 scenario（migrate/export-ddl/export-insert/export/import/export-metadata） | 同 6 种场景 schema |
| 输出 YAML 文件 | 返回 YAML + 可选保存到磁盘 |

**结论：✅ 已覆盖**，UX 从终端问答变为 Web 表单，功能等价。

**差异：**
- CLI 支持 `--source-type`/`--target-type` 等 flags 直接生成（CI 场景），Web 通过表单提交等价实现
- 配置库（`GET/POST/DELETE /api/v1/configs`）是 Web 独有增强

---

### 2.2 validate — 元数据校验

| CLI | serve |
|-----|-------|
| `owl-migrate validate -c config.yaml` | `GET /api/v1/metadata/validate` |
| 输出 error/warning 列表 | 返回 JSON `{count, errors: [{severity, message}]}` |

**结论：✅ 已覆盖**，功能完全等价。

---

### 2.3 export ddl / gen-ddl — DDL 生成

| CLI | serve |
|-----|-------|
| `owl-migrate export ddl -c config.yaml -o ./output/ddl/` | `POST /api/v1/ddl/generate` + `GET /api/v1/ddl/download` |
| 生成 tables/indexes/views/sequences/synonyms/mviews/triggers/functions/packages | 同，调用相同 DDLGenerator |
| `--no-quote-identifiers` flag | 通过 config 中 `ddl.no_quote_identifiers` 控制 |
| `--output` 自定义目录 | 输出到 temp 目录，通过 download 接口获取 zip |

**结论：✅ 已覆盖**

**差异：**
- CLI 可指定任意输出目录；Web 固定输出到 temp 后提供 zip 下载
- `--no-quote-identifiers` 在 Web 端只能通过配置设置，不支持单次请求覆盖

---

### 2.4 gen-select — SELECT 生成

| CLI | serve |
|-----|-------|
| `owl-migrate gen-select -c config.yaml -o ./output/select/` | `POST /api/v1/select/generate` + `GET /api/v1/select/download` |
| `--batch-method cursor/offset` | 读取 config `select_gen.batch.method` |
| `--page-size 5000` | 读取 config `select_gen.batch.page_size` |
| `--no-quote-identifiers` | 通过 config 控制 |

**结论：✅ 已覆盖**

**差异：**
- CLI 支持 `--batch-method`/`--page-size` 单次覆盖；Web 只能用配置值
- 实际影响较小，因为这些参数通常在配置中固定

---

### 2.5 export insert / gen-insert — INSERT SQL 生成

| CLI | serve |
|-----|-------|
| `owl-migrate export insert -d ./data/ -o ./output/insert/` | `POST /api/v1/insert/generate` + `GET /api/v1/insert/download` |
| `--dialect postgres` | 读取 config `ddl.target_dialect` |
| `--batch-size 100` | 硬编码 100 |
| `--truncate` | ❌ 未暴露 |
| `--data` 自定义数据目录 | 读取 config `import.source_dir`，默认 `./output/data/` |

**结论：⚠️ 基本覆盖，缺少 truncate 选项**

**差异：**
- `--truncate`（INSERT 前加 TRUNCATE TABLE）未在 Web 端暴露
- `--batch-size` 硬编码为 100，不可调
- 数据目录不可在请求中指定，依赖配置

---

### 2.6 export data — 数据导出

| CLI | serve |
|-----|-------|
| 在线模式：`owl-migrate export data -c config.yaml` | `POST /api/v1/export`（spawn worker） |
| 离线 CSV：`export data -d ./data/ --format sql` | ❌ 未暴露 |
| 离线 XLSX：`export data --xlsx ./data.xlsx` | ❌ 未暴露 |
| `--format csv/sql/xlsx` | worker 仅做 CSV 导出 |
| 并行 worker、游标分页 | ✅ worker 继承完整 exporter 能力 |
| 进度上报 | ✅ 通过 progress-db 写入 SQLite |

**结论：⚠️ 在线 DB 导出已覆盖，离线模式未覆盖**

**差异：**
- 离线 CSV 模式（`-d`）和离线 XLSX 模式（`--xlsx`）无 Web 入口
- 输出格式固定为 CSV，不支持 sql/xlsx 格式输出
- 这些离线模式使用频率较低，优先级可后置

---

### 2.7 import — 数据导入

| CLI | serve |
|-----|-------|
| `owl-migrate import -c config.yaml` | `POST /api/v1/import`（spawn worker） |
| 自动建表（ensureTables） | ✅ worker 继承 |
| 批量事务、编码转换、数据变换 | ✅ worker 继承完整 importer 能力 |
| `--no-quote-identifiers` | 通过 config 控制 |
| 进度上报 | ✅ 通过 progress-db |

**结论：✅ 已覆盖**

---

### 2.8 migrate — 端到端迁移

| CLI | serve |
|-----|-------|
| `owl-migrate migrate -c config.yaml` | `POST /api/v1/migrate`（spawn worker） |
| `--sql-out ./output/insert/` | ✅ 支持 `{"mode": "sql-out"}` |
| `--resume` | ✅ 支持 `?resume_from=<jobID>` |
| `--skip-ddl` | ❌ 未暴露 |
| `--continue-on-error` | ❌ 未暴露 |
| `--no-quote-identifiers` | 通过 config 控制 |
| `--temp-dir` | ✅ 由 master 自动管理 |
| checkpoint/resume | ✅ 完整支持 |
| 进度上报 + WebSocket | ✅ 实时推送 |
| 迁移报告 JSON | ✅ worker 生成 |

**结论：⚠️ 核心流程已覆盖，缺少 2 个控制 flag**

**差异：**
- ~~`--skip-ddl`（仅导数据不建表）未暴露~~ → ✅ 已实现（请求体 `skip_ddl`）
- ~~`--continue-on-error`（部分表失败不中断）未暴露~~ → ✅ 已实现（请求体 `continue_on_error`）

---

### 2.9 show-query — 查看元数据提取 SQL

| CLI | serve |
|-----|-------|
| `owl-migrate show-query oracle tables` | `GET /api/v1/show-query?dialect=oracle&object_type=tables` |

**结论：✅ 已覆盖（2026-07-29 补充）**

---

### 2.10 export-metadata — 元数据导出

| CLI | serve |
|-----|-------|
| `owl-migrate export-metadata -c config.yaml --format csv/xlsx/sql` | `POST /api/v1/metadata/export` |
| 连接源库提取元数据 → 输出 CSV/XLSX/SQL | 请求体指定 source/format/scope |
| `--scope all/schema:NAME/table:T1,T2` | 请求体 `scope` 字段 |

**结论：✅ 已覆盖（2026-07-29 补充）**

新增"元数据导出"页面（`/export-metadata`），支持 CSV 和 SQL 格式导出 + ZIP 下载。

---

## 三、serve 独有增强（CLI 不具备）

| 功能 | 说明 |
|------|------|
| 任务持久化 | SQLite 存储 job 历史，重启不丢失 |
| WebSocket 实时进度 | 浏览器实时展示逐表迁移进度 |
| 任务取消 | `DELETE /api/v1/jobs/{id}` 终止 worker |
| 配置库 | 命名配置的 CRUD（保存/加载/删除） |
| 场景化配置构建 | 表单驱动，自动检测 scenario |
| SQL 输出下载 | tar.gz / zip / raw 多格式 |
| Checkpoint 查看 | `GET /api/v1/jobs/{id}/checkpoints` |
| 孤儿进程检测 | heartbeat + parent-pid 监控 |
| 启动恢复 | 自动标记上次 running 任务为 interrupted |
| Web UI 页面 | 11 个页面覆盖完整操作流程 |

---

## 四、未实现/待补充清单（按优先级）

> **更新于 2026-07-29：以下所有项目已实现。**

### P1 — 生产常用 ✅ 已完成

| # | 功能 | 来源命令 | 实现方式 |
|---|------|----------|----------|
| 1 | `--skip-ddl` 选项 | migrate | `POST /api/v1/migrate` 请求体 `skip_ddl` 字段，透传至 worker CLI |
| 2 | `--continue-on-error` 选项 | migrate | `POST /api/v1/migrate` 请求体 `continue_on_error` 字段，透传至 worker CLI |
| 3 | `--truncate` 选项 | export insert | `POST /api/v1/insert/generate` 请求体 `truncate` 字段 |

### P2 — 功能完整性 ✅ 已完成

| # | 功能 | 来源命令 | 实现方式 |
|---|------|----------|----------|
| 4 | 离线 CSV 导出模式 | export data -d | `POST /api/v1/export/offline` 请求体 `data_dir` 字段 |
| 5 | 离线 XLSX 导出模式 | export data --xlsx | `POST /api/v1/export/offline` 请求体 `xlsx_path` 字段 |
| 6 | 导出格式选择（sql/xlsx） | export data --format | `POST /api/v1/export/offline` 请求体 `format` 字段（csv/sql/xlsx） |
| 7 | INSERT batch-size 可调 | export insert --batch-size | `POST /api/v1/insert/generate` 请求体 `batch_size` 字段 |
| 8 | export-metadata 全流程 | export-metadata | `POST /api/v1/metadata/export` + 下载接口 + "元数据导出"页面 |

### P3 — 辅助/诊断 ✅ 已完成

| # | 功能 | 来源命令 | 实现方式 |
|---|------|----------|----------|
| 9 | show-query 查看 | show-query | `GET /api/v1/show-query?dialect=oracle&object_type=tables` |
| 10 | gen-select 参数单次覆盖 | gen-select --batch-method/--page-size | `POST /api/v1/select/generate` 请求体 `batch_method`/`page_size` 字段 |
| 11 | --no-quote-identifiers 单次覆盖 | 多个命令 | DDL/SELECT/INSERT 生成接口均支持请求体 `no_quote_identifiers` 字段 |

---

## 五、架构说明

```
┌─────────────────────────────────────────────────────┐
│  owl-migrate serve                                  │
│                                                     │
│  ┌───────────────┐       ┌──────────────────────┐  │
│  │  serve.Server │──IPC──│  master.Master       │  │
│  │  (HTTP :8080) │       │  (HTTP 127.0.0.1:254xx)│ │
│  │               │       │                      │  │
│  │  - REST API   │       │  - POST /jobs (spawn)│  │
│  │  - WebSocket  │       │  - DELETE /jobs/{id} │  │
│  │  - Web UI     │       │  - monitor worker    │  │
│  └───────────────┘       └──────────┬───────────┘  │
│                                     │ exec          │
│                          ┌──────────▼───────────┐  │
│                          │  Worker Process      │  │
│                          │  (owl-migrate migrate│  │
│                          │   / export data      │  │
│                          │   / import)          │  │
│                          │  --progress-db       │  │
│                          │  --job-id            │  │
│                          └──────────────────────┘  │
└─────────────────────────────────────────────────────┘
```

Worker 通过 `--progress-db` + `--job-id` 将进度事件写入共享 SQLite，
serve.Server 通过轮询 SQLite + WebSocket 推送给浏览器。

---

## 六、文件索引

| 模块 | 路径 |
|------|------|
| serve 入口 | `internal/cmd/serve.go` |
| HTTP API + 页面路由 | `internal/server/serve/server.go` |
| DDL/SELECT/INSERT 生成 | `internal/server/serve/generate.go` |
| 元数据导出（export-metadata） | `internal/server/serve/exportmetadata.go` |
| 离线数据导出（CSV/XLSX） | `internal/server/serve/exportoffline.go` |
| show-query 诊断接口 | `internal/server/serve/showquery.go` |
| 任务启动/取消 | `internal/server/serve/jobs.go` |
| WebSocket 进度推送 | `internal/server/serve/websocket.go` |
| 配置管理 | `internal/server/serve/config.go` + `configs.go` |
| 场景构建 | `internal/server/serve/scenarios.go` |
| SQL 输出下载 | `internal/server/serve/output.go` |
| Master IPC（进程管理） | `internal/server/master/master.go` |
| JobStore（SQLite） | `internal/service/job.go` |
| ProgressWriter（worker 端） | `internal/service/worker.go` |
| Web UI 模板 | `web/templates/` |
