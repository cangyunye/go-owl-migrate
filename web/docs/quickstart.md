# 快速开始

以 Oracle → PostgreSQL 迁移为例。

## 1. 启动服务

```bash
owl-migrate serve
```

浏览器打开 `http://127.0.0.1:8080`。

## 2. 配置

进入「配置」页，填入源库与目标库信息（JSON 格式）：

```json
{
  "metadata": { "type": "database" },
  "source": {
    "type": "oracle",
    "dsn": "oracle://user:pass@host:1521/service",
    "schema": "SCOTT"
  },
  "target": {
    "type": "postgres",
    "dsn": "host=127.0.0.1 port=5432 user=postgres password=secret dbname=mydb sslmode=disable",
    "schema": "public"
  },
  "ddl": {
    "target_dialect": "postgres",
    "schema_mapping": { "SCOTT": "public" },
    "include_if_not_exists": true
  }
}
```

点击「保存」。

也可以离线使用：将 `metadata.type` 设为 `csv` 并指定元数据目录。

## 3. 启动迁移

进入「迁移」页，选择模式：

- **直接迁移**：源库 → 导出 CSV → 目标库
- **SQL 输出**：源库 → 导出 CSV → 生成 INSERT SQL 文件（不连目标库）

点击「开始迁移」，页面通过 WebSocket 实时显示每张表的进度。

## 4. 查看结果

进入「任务」页查看历史任务、状态与报告。
