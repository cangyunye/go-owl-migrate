# owl-migrate Web 服务文档

浏览器端数据库迁移工具的使用指南。

## 文档列表

| 文档 | 说明 |
|---|---|
| [installation](/docs/installation) | 安装、启动、端口配置 |
| [quickstart](/docs/quickstart) | 快速开始：第一次迁移 |
| [config](/docs/config) | 配置说明 |
| [migration](/docs/migration) | 完整迁移流程 |
| [jobs](/docs/jobs) | 任务管理：历史、恢复、取消 |
| [troubleshooting](/docs/troubleshooting) | 常见问题排查 |

## 架构概览

`owl-migrate serve` 启动一个本地 Web 服务，包含三部分：

- **Web UI**：浏览器界面，默认 `http://127.0.0.1:8080`
- **Master IPC**：内部进程管理接口，自动选择 `25430-25439` 端口
- **Worker**：每个迁移/导出/导入任务由独立子进程执行，崩溃不影响 Web 服务

无需登录鉴权，仅供本机或受信任网络使用。
