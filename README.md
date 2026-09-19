# go-owl-migrate

Database migration tool for cross-database schema & data migration: Oracle, PostgreSQL, MySQL, SQLite3, DuckDB, GoldenDB, OceanBase, PanWeiDB, OpenGaussDB. Part of the **owl** family of database tools.

> 📚 **Full documentation**: See [docs/index.md](docs/index.md) for the complete documentation index.

## 公告 / Announcements

### 2026-09-18 · 安全警示：ZCode（智谱）静默上传用户代码 / Security Alert: ZCode (Zhipu) silently uploads user code

> **核心问题：未经用户允许，私自上传用户代码。**

据 [ferstar 的逆向分析](https://blog.ferstar.org/posts/zcode-silent-workspace-snapshot-upload/)（2026-09-18）披露，智谱官方 AI 编程桌面端 **ZCode** 会在用户登录状态下，于后台**静默**将整个工作区打包加密并直接上传至阿里云 OSS，具体包括：

- **完整的 `.git` 历史**（objects、reflog、未推送分支、`.git/config` 内部仓库地址）
- **LFS 大文件缓存**（历史下载过的全部大文件与二进制资产）
- **全局应用配置**（如 `settings.behavior.json`）
- 上传全程**无任何 UI 开关可关闭**：设置中的"优化体验"/"仓库快照索引"开关只控制训练与索引，快照抓取与上传逻辑在登录后无条件常开；每次发送 Prompt 前与任务结束时都会触发快照捕获（单个会话最多 62 次）
- 加密采用服务端下发的 RSA 公钥做信封加密，**私钥仅存于云端**，本地与客户端均无法解密
- 隐私政策、官方文档与更新日志**均未披露**该行为

**本项目立场**：代码是用户的核心资产，任何未经明确同意即擅自上传代码的行为都不可接受。本项目（go-owl-migrate）坚持 offline-first 设计，**默认不产生任何网络上传**；所有导出/迁移产物默认仅写入本地磁盘。请使用者务必选择可信、开源、可审计的工具链。

来源：<https://blog.ferstar.org/posts/zcode-silent-workspace-snapshot-upload/>

## Features

- **Offline-first**: Generate DDL, SELECT, and INSERT SQL from CSV metadata — no database connection required
- **Live extraction**: Extract schema metadata directly from PostgreSQL, MySQL, Oracle, SQLite3, and DuckDB
- **Cross-dialect DDL generation**: NUMBER↔DECIMAL↔INTEGER, VARCHAR2↔VARCHAR, BOOLEAN↔TINYINT(1), CLOB↔TEXT↔LONGTEXT, etc.
- **Compound dialects**: GoldenDB (MySQL/Oracle mode), OceanBase (MySQL/Oracle mode), PanWeiDB, OpenGaussDB
- **OceanBase dual-driver**: Oracle tenants over go-ora/TNS or obconnector-go MySQL wire; compat mode auto-probed and enforced
- **Embedded dialects**: SQLite3, DuckDB — in-process databases, no external server needed
- **Data migration**: Export source data to CSV with cursor/offset pagination, import with batched transactions
- **PostgreSQL COPY fast path**: `import.batch.use_copy` enables `COPY` bulk loads (auto-fallback to batched INSERT)
- **Automatic target table creation**: cross-dialect type conversion through the logical-type IR (`ddl.source_dialect` for CSV metadata)
- **Checkpoint/Resume**: Per-table state persists to disk — interrupted migrations pick up where they left off
- **Continue on error**: Per-table error isolation — one failing table doesn't abort the whole migration
- **SQL output mode**: Generate INSERT SQL files instead of writing directly to the target database
- **Encoding conversion**: GBK, LATIN1, ISO-8859-*, Windows-1252 to UTF-8 conversion during import
- **Data transforms**: Compact datetime (yyyyMMddHHmmss) auto-formatting, string trimming, boolean mapping, binary hex encoding
- **Error policies**: Per-row error handling — stop, skip_row, or log_only
- **Migration report**: Detailed JSON report with per-table row counts, errors, and duration
- **Parallel export/import**: Concurrent table processing via worker pool

## Quick Start

```bash
# Install
go install github.com/cangyunye/go-owl-migrate/cmd/migrate@latest

# Or build from source
make build
go build -o owl-migrate ./cmd/migrate/main.go

# Generate config from CLI parameters
owl-migrate init --source-type oracle --source-dsn "oracle://u:p@host:1521/service" \
  --source-schema SCOTT --target-type postgres \
  --target-dsn "postgres://u:p@localhost:5432/migrate" --target-schema public \
  -o ./migrate.yaml

# Validate metadata
owl-migrate validate -c ./migrate.yaml

# Generate DDL scripts for the target database
owl-migrate export ddl -c ./migrate.yaml -o ./output/ddl/

# Run end-to-end migration (export + create tables + import)
owl-migrate migrate -c ./migrate.yaml

# Or use SQL output mode (no target database connection needed)
owl-migrate migrate -c ./migrate.yaml --sql-out ./output/insert/
```

## Commands

| Command | Description |
|---------|-------------|
| `init`       | Generate config file from CLI parameters |
| `validate`   | Validate metadata (CSV or database) |
| `export ddl`    | Generate DDL (TABLE/INDEX/VIEW/SEQUENCE/TRIGGER/FUNCTION/PACKAGE) for target dialect |
| `export data`   | Export source database data to CSV/SQL/XLSX files |
| `export insert` | Generate INSERT SQL from CSV data (offline mode) |
| `gen-select`    | Generate paginated SELECT queries for data export |
| `import`        | Import CSV data into target database |
| `migrate`       | End-to-end: export → create tables → import → report |

## Supported Dialects

| Database | Source | Target | DDL | Export | Import |
|---|---|---|---|---|---|
| Oracle | ✓ | ✓ | ✓ | ✓ | ✓ |
| PostgreSQL | ✓ | ✓ | ✓ | ✓ | ✓ |
| MySQL | ✓ | ✓ | ✓ | ✓ | ✓ |
| GoldenDB (MySQL mode)* | ✓ | ✓ | ✓ | ✓ | ✓ |
| GoldenDB (Oracle mode)* | ✓ | ✓ | ✓ | ✓ | ✓ |
| OceanBase (MySQL mode)† | ✓ | ✓ | ✓ | ✓ | ✓ |
| OceanBase (Oracle mode)† | ✓ | ✓ | ✓ | ✓ | ✓ |
| PanWeiDB‡ | ✓ | ✓ | ✓ | ✓ | ✓ |
| OpenGaussDB‡ | ✓ | ✓ | ✓ | ✓ | ✓ |
| SQLite3§ | ✓ | ✓ | ✓ | ✓ | ✓ |
| DuckDB§ | ✓ | ✓ | ✓ | ✓ | ✓ |

Dialect support is selected at build time. Oracle, PostgreSQL and MySQL are
always compiled in; every other dialect is opt-in via a build tag:

| Tag | Adds | database/sql driver |
|---|---|---|
| `ob` | OceanBase (MySQL/Oracle mode)† | obconnector-go (`oboracle`) |
| `og` | PanWeiDB, OpenGaussDB‡ | openGauss-connector-go-pq (`opengauss`) |
| `gdb` | GoldenDB (MySQL/Oracle mode)* | reuses the base mysql driver |
| `sqlite3` | SQLite3§ (CGo) | mattn/go-sqlite3 |
| `duckdb` | DuckDB§ (CGo) | go-duckdb |

Build flavors:

```bash
go build -tags ob ./cmd/migrate/main.go                        # base + OceanBase
go build -tags "og gdb" ./cmd/migrate/main.go                  # base + PanWeiDB/OpenGaussDB + GoldenDB
go build -tags "ob og gdb sqlite3 duckdb" ./cmd/migrate/main.go # everything
make build/ob                                                  # Makefile/Taskfile flavors below
```

A base binary rejects configs for un-compiled dialects with an error naming
the required tag (e.g. `rebuild with -tags ob`).

## Documentation

Full index with a browsable website (GitHub-flavored Markdown, all languages): **[docs/index.md](docs/index.md)** — the docs-site build is served from `docs-site/index.html` (see [deployment](#deployment)).

Key documents:

| Document | Description |
|---|---|
| [Getting Started](docs/getting-started.md) | Installation, quick start, workflows |
| [CLI Commands](docs/cli-commands.md) | Full command reference with flags and examples |
| [Configuration](docs/config.md) | All YAML configuration options |
| [CSV Metadata Format](docs/csv-format.md) | CSV file format for offline schema definition |
| [Migration Pipeline](docs/migration-pipeline.md) | Export/import, checkpoint/resume, encoding, error handling |
| [Dialect & Type Mapping](docs/dialect-mapping.md) | Dialect system, type mapping, feature flags |
| [Database Metadata](docs/database-metadata/index.md) | Live metadata extraction SQL per database |
| [Developer Guide](docs/development.md) | Project structure, testing, adding dialects |

## Deployment (docs-site)

The documentation website is a static single-page app: `docs-site/index.html` renders
`docs/**/*.md` (soft-linked as `docs-site/docs → ../docs`). Serve it in two ways:

```bash
# ① Static server (any of):
python3 -m http.server 8080            # from the project root → /docs-site/
npx serve docs-site

# ② Embedded in the tool:
go run ./cmd/migrate/main.go serve     # serves /docs/ from docs-site/
```

Rebuild/refresh: edit Markdown under `docs/`, then re-sync the symlink
(`ln -sfn ../docs docs-site/docs`) and keep the `DOCS` index in
`docs-site/index.html` in sync with the actual files.

## License

MIT
