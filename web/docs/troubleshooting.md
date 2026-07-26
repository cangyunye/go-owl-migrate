# 常见问题排查

## 端口被占用

```
listen tcp 127.0.0.1:8080: address already in use
```

换一个端口：`owl-migrate serve --port 9090`，或先结束占用端口的进程。

## Master IPC 连接失败

页面提示任务无法启动、`master unreachable`：

- 确认 `owl-migrate serve` 进程仍在运行
- 查看启动日志中 `Master IPC listening on 127.0.0.1:<port>` 的实际端口
- 若 IPC 端口全部被占，用 `--master-ipc-port <port>` 手动指定

## SQLite 锁等待

任务进度写入缓慢或报 `database is locked`：

- 已启用 WAL 模式与 5 秒 busy_timeout，通常无需处理
- 检查是否有其他进程长时间独占该 SQLite 文件
- 状态库路径可用 `--db` 更换到更快的磁盘

## Worker 无进度

任务一直 `running` 但没有进度事件：

- 确认 Worker 子进程存在（任务详情中的 PID）
- 检查源库连接是否正常、表是否有数据
- 查看服务终端输出中 Worker 的日志

## 任务卡在 running（服务崩溃后）

服务异常退出后，重新 `owl-migrate serve`：

- 启动时会自动把残留的 `running` 任务标记为 `interrupted`
- 若 Worker 仍在后台运行，它会检测到主进程心跳消失，完成当前表后自行退出
- 之后可对 `interrupted` 任务点击「恢复」续跑

## 浏览器页面空白

- 确认访问的是 `http://`（不是 `https://`）
- 静态资源内嵌于二进制，无需外部文件；若仍空白，检查浏览器控制台报错

## 心跳文件

Master 每 5 秒写入心跳文件（默认 `/tmp/owl-migrate-master.heartbeat`）。
Worker 每 10 秒检查一次，超过 20 秒未更新则判定主进程已退出。
若 `/tmp` 无写权限，任务仍可运行，但失去主进程死亡检测能力。
