# 配置说明

Web UI 的「配置」页以 JSON 编辑当前配置，与 CLI 的 YAML 配置等价。

## 主要配置段

| 段 | 说明 |
|---|---|
| `general` | 日志级别、格式 |
| `metadata` | 元数据来源：`csv` / `xlsx` / `database` |
| `source` | 源数据库连接（type / dsn / schema） |
| `target` | 目标数据库连接 |
| `ddl` | DDL 生成：目标方言、schema 映射、类型覆盖 |
| `select_gen` | SELECT 分页生成 |
| `export` | 数据导出：输出目录、格式、批量、并发 |
| `import` | 数据导入：提交间隔、错误策略、数据转换 |

## 元数据来源

- **csv**：离线模式，从 CSV 元数据目录读取表结构
- **xlsx**：离线模式，从 Excel 工作簿读取
- **database**：在线模式，从源库实时抽取元数据

## 方言支持

`ddl.target_dialect` 可选值：
`oracle` `postgres` `mysql` `sqlite3` `duckdb`
`goldendb` `goldendb-mysql` `goldendb-oracle`
`oceanbase` `oceanbase-mysql` `oceanbase-oracle`
`panweidb` `panweidb-mysql` `panweidb-oracle` `opengaussdb`

## schema 映射

```json
"ddl": {
  "schema_mapping": { "SCOTT": "public" }
}
```

将源 schema `SCOTT` 的对象生成到目标 schema `public`。

## 与 CLI 配置互通

Web 保存的配置可通过 API 下载为 YAML，直接用于 CLI：

```bash
curl http://127.0.0.1:8080/api/v1/config
```
