# 安装与启动

## 启动服务

```bash
owl-migrate serve
```

默认监听 `http://127.0.0.1:8080`，浏览器自动访问即可。

## 常用参数

```bash
owl-migrate serve --port 9090            # 指定 Web 端口
owl-migrate serve --host 0.0.0.0         # 允许局域网访问（谨慎使用，无鉴权）
owl-migrate serve --master-ipc-port 25430 # 指定 Master IPC 端口
owl-migrate serve --temp-dir ./work/     # 任务临时目录
owl-migrate serve --db ./state.db        # SQLite 状态库路径
```

## 端口配置优先级

Web 端口与 IPC 端口均按以下顺序取值：

1. 命令行参数（`--port` / `--master-ipc-port`）
2. `.env` 文件（`OWL_MIGRATE_SERVE_PORT` / `OWL_MIGRATE_MASTER_IPC_PORT`）
3. 环境变量（同上）
4. 默认值（Web: `8080`；IPC: 自动从 `25430-25439` 选取）

IPC 端口自动选取顺序：`25430-25439` → `25400-25499` → `25000-25999` → `26000+`。
全部占用时报错，请用 `--master-ipc-port` 手动指定。

## .env 文件示例

在工作目录创建 `.env`：

```env
OWL_MIGRATE_SERVE_HOST=127.0.0.1
OWL_MIGRATE_SERVE_PORT=8080
OWL_MIGRATE_MASTER_IPC_PORT=25430
OWL_MIGRATE_TEMP_DIR=./output/temp/
OWL_MIGRATE_LOG_LEVEL=info
```

## 停止服务

按 `Ctrl-C` 或发送 `SIGTERM`。服务会优雅关闭：等待进行中的请求完成，
已启动的 Worker 子进程继续运行（可重新打开页面查看进度）。
