# 可复用 E2E 数据集（dept / emp / special）

跨库迁移测试的统一数据集，用同一份数据在任意数据库播种，避免每次为不同库重新造数。

## 数据集

三张表，逻辑定义一致（各库播种时用其方言 DDL，数据相同）：

| 表 | 内容 | 数据量 |
|----|------|--------|
| `dept` | 部门（PK，`deptno`） | 4 行 |
| `emp` | 员工（PK + FK→`dept.deptno`，含 `DATE`/`DECIMAL` 类型） | 8 行 |
| `special` | 特殊字符 + 空值矩阵（详见下） | 6 行 |

### special 表覆盖矩阵（id 1–6）

| id | 覆盖点 |
|----|--------|
| 1 | 双引号 `"quoted" 'single'`、`#hash @at !excl %pct`、`comma,semi;colon:`、反斜杠、换行+制表符、中文/emoji、NULL 数值/日期 |
| 2 | NULL、字面 `"NULL"`、字面 `"\N"`、空串 |
| 3 | `,a,,b,`（首尾+连续逗号）、`l1\nl2\nl3`（多换行） |
| 4 | `,`（纯逗号）、`\nlead\n`（首尾换行） |
| 5 | `a,b,c,d`（多逗号）、`trail\n`（尾换行） |
| 6 | `,`（纯逗号）、`\n`（单独换行） |

## 播种方式

Go 工具 `tools/ogtest`（已提交；DSN 读 `testdata/db/.local-dev.env`，OS 环境变量优先）：

| 命令 | 播种目标 |
|------|---------|
| `go run ./tools/ogtest seed` | openGauss 默认库(src_og) + MySQL(src_my) + PostgreSQL(src_pg)，含各自 tgt 空 schema |
| `go run ./tools/ogtest seeddb <db>` | openGauss 兼容库 `og_pg`/`og_ora`/`og_mysql` 的 `src`/`tgt` schema |
| `go run ./tools/ogtest seedob` | OB Oracle 租户 `oratest`（用户 `MIGSRC`，属主=schema）+ OB MySQL 租户（库 `ogtest`） |

> 各库播种同一份逻辑数据：openGauss/PostgreSQL 用 PG DDL，MySQL/OB-MySQL 用 MySQL DDL，
> OB-Oracle 用 Oracle DDL（`NUMBER`/`VARCHAR2`/`TO_DATE`），特殊表换行用 `chr(10)`/`CHAR(10)`/`CHR(10)` 拼。

## 验证命令

`go run ./tools/ogtest verify <type> <dsn> <schema>` 打印表行数；
`go run ./tools/ogtest verifyspecial <type> <dsn> <schema>` 打印 special 6 行原文（NULL/引号/逗号/换行可视化）。

## 已测迁移方案矩阵（迁移前先跑对应 seed 播种源）

| 方向 | 源 → 目标 | 结果 |
|------|----------|------|
| openGauss(PG/A/B) ↔ MySQL | dept/emp/special 全保真 | ✅ 12 条全过（B 模式 `number` 需 type_override 见下） |
| openGauss ↔ PostgreSQL | 同上 | ✅ 全过 |
| openGauss → OB MySQL | 同上 | ✅ 全过 |
| openGauss → OB Oracle | 同上 | ✅ 全过（**先清空目标 schema 遗留表**，防大小写残留） |
| OB Oracle → MySQL | 同上（OB 作源） | ✅ 全过 |
| OB MySQL → MySQL | 同上 | ✅ 全过 |
| OB Oracle → openGauss(og_pg) | 同上（OB 作源） | ✅ 全过 |
| OB MySQL → openGauss(og_pg) | 同上 | ✅ 全过 |

> openGauss B 模式（dolphin）把 `DECIMAL` 上报为 `number`，作为源迁往 PG/OB 目标时需
> `ddl.type_overrides: {NUMBER: "NUMERIC(%p,%s)"}`（A/PG 模式不需）。

## 已知限制

- 源数据含**字面 `"\N"`** → 导入为 NULL（与默认 `\N` 空值标记冲突）。
- OB Oracle 目标表名/列名由迁移 DDL 以**引号小写**创建（`"dept"`）；若目标 schema 有
  历史**大写**表（如旧测试的 `DEPT`），`tableExists` 判存在而跳过建表，导入按小写引用会失败
  → 先 `DROP` 清理目标 schema 再迁移。
- OB Oracle 会话 DML 需显式 `COMMIT`（Oracle 语义）；DDL 自动提交。
