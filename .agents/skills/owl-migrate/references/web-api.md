# serve Web API 参考

CLI 与 Web API 的分工：**数据源管理、连接测试、迁移预检只有 Web API**；其余两端都有（Web 走后台任务 + WebSocket 进度）。

## 启动

```bash
owl-migrate serve                  # 默认 127.0.0.1:8080，本机用无需 token
owl-migrate serve --port 9090 --host 0.0.0.0 --token <强随机串>   # 对外暴露必须 --token
```

其他 flags：`--master-ipc-port`（0=自动）、`--temp-dir`、`--db`（任务 SQLite，默认 ~/.owl/migrate/owl-migrate.db）、`--config-out`、`--config-dir`（配置库，默认 ~/.owl/migrate/configs/library/）。

API 前缀 `/api/v1`，以下省略。完整冻结契约见仓库 `docs/api-contract.md`。

## 数据源（仅 Web API）

存储：`~/.owl/migrate/datasources/<name>.yaml`，DSN 以 AES-256-GCM 加密（密钥 `~/.owl/migrate/.ds_key` 或环境变量 `OWL_MIGRATE_DS_KEY`）。

| 操作 | 请求 |
|---|---|
| 列出 | `GET /datasources` |
| 创建 | `POST /datasources` body `{"name":"生产库","type":"oracle","schema":"SCOTT","dsn":"oracle://...","remark":"..."}` |
| 更新 | `PUT /datasources/{name}`（部分字段更新） |
| 删除 | `DELETE /datasources/{name}` |
| 选用 | `POST /datasources/{name}/pick` |

## 连接测试（仅 Web API）

```
POST /conn/test
{"type":"mysql","dsn":"user:pass@tcp(10.3.1.4:3306)/sales","schema":"","connect_timeout":"10s","channel":""}
```

`channel`：`""`/`native` | `agent` | `auto`（v0.5.1+，缺省随配置）。agent 通道走 owljdbc JVM sidecar，见 references/dsn-and-config.md「连接通道」。

## 迁移预检（仅 Web API，只读）

```
POST /migrate/preflight
```

只读检查链：配置存在 → 元数据可读/源库可达 → 表清单命中 → （非 sql-out 模式）目标库可达。返回 `{ok, checks[], warnings[]}`。**预检通过后应继续执行迁移，预检不是终点。**

## 任务（迁移/导出/导入）

| 操作 | 请求 |
|---|---|
| 启动迁移 | `POST /migrate`（body 与 Web 前端一致，见 docs/api-contract.md） |
| 启动导出/导入 | `POST /export`、`POST /import`、`POST /export/offline` |
| 任务列表/详情 | `GET /jobs`、`GET /jobs/{id}`、`DELETE /jobs/{id}` |
| 进度 | `GET /jobs/{id}/events`、`GET /jobs/{id}/checkpoints`、`GET /jobs/{id}/output[/download]`、实时 `WS /jobs/{id}/ws` |

serve 内部派生 `migrate` / `export data` / `import` worker 子进程执行实际任务。

## 配置与元数据

| 操作 | 请求 |
|---|---|
| 当前配置 | `GET/PUT /config`、`GET /config/current|download|status`、`POST /config/upload` |
| 配置库 | `GET/POST /configs`、`GET/DELETE /configs/{name}`、`POST /configs/{name}/load` |
| 元数据 | `POST /metadata/load`、`GET /metadata/tables[/{schema}/{table}]`、`GET /metadata/validate`、`POST /metadata/export`(+`/download`) |
| 生成器 | `POST /ddl|select|insert/generate`(+`/download`)、`GET /insert/tables` |
| 能力探测 | `GET /capabilities` —— 逐类型 native/agent 可用性、owl-agent.jar 与驱动 jar 探测（含 owljdbc.profiles 注册类型） |
| 其他 | `GET/POST /scenarios...`、`GET /generations[/{id}/files]`、`GET /dialects`、`GET /show-query`、`GET /health` |

## 数据源在配置中的引用

```yaml
source:
  type: oracle
  dsn: "datasource:生产库"   # serve 端解析并替换真实 DSN（解密在服务端，浏览器不接触明文）
```

## Web 端对 agent 通道的呈现（v0.7.0 现状）

- **配置页**类型下拉 = 内置方言 + `owljdbc.profiles` 注册的外部类型（`/scenarios` 返回时合并）；自定义类型的结构化弹窗字段不可靠，**直接把 DSN 粘到 DSN 框**。测试连接会带上表单的通道值。
- **通道选择目前只能在 YAML**（`source.channel`/`target.channel`）：Web 表单尚未渲染通道下拉（前端预留了 `source_channel`/`target_channel` 字段读取逻辑）。
- **数据源页**弹窗的类型下拉走 `/api/v1/dialects`，**仅内置方言**，注册类型与 dm/kingbase/timesten 不在其中。
