# OpenGaussDB E2E 测试方案

## 方言信息

| 属性 | 值 |
|------|-----|
| 注册名 | `opengaussdb` |
| 父方言 | PostgreSQL |
| 数据库驱动 | openGauss-connector-go-pq (lib/pq 分支；SHA256/SM3 认证；注册名 `opengauss`) |
| 元数据提取器 | PostgreSQL (`normalizeDBType` → `postgres`) |
| 端口 | 5432 (容器映射 5433) |
| 连接串示例 | `host=127.0.0.1 port=5433 user=gaussdb password=OpenGauss@123 dbname=postgres sslmode=disable`（PG 协议；默认用户为 `gaussdb`） |

## 已覆盖的 Override

| 组件 | 状态 | 说明 |
|------|------|------|
| TypeMapper | ✅ `ogTypeMapper` | 继承 PG，仅改 Name |
| IdentifierQuoter | ✅ 继承 PG | 双引号引用，case-preserving |
| Features | ✅ 继承 PG 100% | PostgreSQL feature flags |
| DDLBuilder | ✅ 继承 PG 100% | 所有 DDL 方法继承 PG |
| DMLHelper | ✅ 继承 PG | LIMIT...OFFSET 分页 |

## 差异点

OpenGaussDB 基于 PostgreSQL 内核，当前 100% 继承 PG 方言。

| 差异 | OpenGaussDB | 标准 PostgreSQL | 状态 |
|------|------------|----------------|------|
| 元数据提取 | 可能使用不同系统表 | information_schema | ❌ 待实测确认 |
| 序列语法 | 可能使用 `NOCYCLE` | `NO CYCLE` | ❌ 待实测确认 |
| MOT 引擎 | 支持内存优化表 | 无 | ❌ 未实现 |
| 特定 PG 扩展 | 部分不支持 | 完整 | ❌ 待实测 |
| 类型系统 | 可能有差异 | 标准 | ❌ 待实测 |

## 容器配置（已在 docker-compose.yaml 中）

```yaml
opengauss:
  image: opengauss/opengauss-server:latest
  pull_policy: never
  container_name: opengauss
  privileged: true
  environment:
    GS_PASSWORD: OpenGauss@123
  volumes:
    - opengauss-data:/var/lib/opengauss
  ports:
    - "5433:5432"
```

## 测试用例矩阵

| 对象类型 | 预期 | 说明 |
|---------|------|------|
| TABLE | ✅ 继承 PG | 双引号引用 |
| INDEX | ✅ 继承 PG | CREATE [UNIQUE] INDEX "name" |
| VIEW | ✅ 继承 PG | CREATE VIEW "name" AS |
| SEQUENCE | ✅ 继承 PG | CREATE SEQUENCE — 需验证 `NOCYCLE` vs `NO CYCLE` |
| TRIGGER | ✅ 继承 PG | EXECUTE FUNCTION |
| FUNCTION | ✅ 继承 PG | $$ LANGUAGE plpgsql |
| MVIEW | ✅ 继承 PG | CREATE MATERIALIZED VIEW |

## 验证步骤

> 仅 Linux 可执行：opengauss 在 compose 中属于 `opengauss` profile（macOS 下官方镜像不可用）。

```bash
# 1. 启动容器（--profile 启用，或显式指定服务名）
docker compose -f testdata/db/docker-compose.yaml --profile opengauss up -d opengauss

# 2. 等待就绪
sleep 30
docker exec opengauss gsql -U gaussdb -d postgres -c "SELECT 1"

# 3. 创建测试数据（先建表，再建附属对象）
docker exec -i opengauss gsql -U gaussdb -d postgres < testdata/db/postgres/seed_tables.sql
docker exec -i opengauss gsql -U gaussdb -d postgres < testdata/db/postgres/setup.sql

# 4. 生成 DDL
mkdir -p /tmp/e2e/opengaussdb/
go run ./cmd/migrate/main.go export ddl \
  -c <config-with-opengaussdb> -o /tmp/e2e/opengaussdb/

# 5. 验证输出
ls /tmp/e2e/opengaussdb/*.sql
```

### 注意

- 连接 OpenGaussDB 需要使用 PG 协议驱动，DSN 格式与 PG 相同
- OpenGaussDB 默认用户为 `gaussdb`（非 `postgres`）
- 容器启动可能较慢（MOT 引擎初始化）
- 某些 `information_schema` 查询可能与 PG 有差异，需实际验证提取器是否工作

---

## 多兼容模式 E2E 实测结论（2026-09-05）

同一 openGauss 6.0.6 实例配了三个兼容模式库（ogadmin 建库，dolphin 见 B 模式）：

| 库名 | sql_compatibility | dolphin | 用途 |
|------|-------------------|---------|------|
| `og_pg` | `PG` | 无 | PostgreSQL 模式 |
| `og_ora` | `A` | 无 | Oracle 模式（空串= NULL） |
| `og_mysql` | `B` | 已装 | MySQL 模式 + dolphin 4.5 |

> 默认 `postgres` 库也是 `sql_compatibility=A`（与 `og_ora` 相同）。

### 已修复的提取器缺陷（openGauss 与标准 PG 的差异）

1. **`pg_sequences` 视图不存在**（openGauss 6.x 无此 PG10+ 视图）。`QuerySequences` 增加
   `information_schema.sequences` 回退（两者都存在时优先 `pg_sequences`，保留 cache_size/last_value）。
2. **`pg_matviews` 视图不存在**。`QueryMViews` 改用 `pg_class relkind='m'` + `pg_get_viewdef`。
3. **A 模式空串即 NULL**：`'' IS NULL` 为真，导致 `COALESCE(x,'')` 返回 NULL，
   `QueryTables/QueryColumns/...` 扫描报 `converting NULL to string`。
   已将各可空注释/描述列扫描改为 `sql.NullString`（PG/B 模式返回 `''` 非空，A 模式返回 NULL，
   `.String` 均为空串，跨模式安全）。

### 元数据抽取（export-metadata，source=opengaussdb + schema=src）

| 模式 | tables | columns | fk | 结果 |
|------|--------|---------|----|------|
| og_pg | ✓ | ✓ | ✓ | 通过 |
| og_ora | ✓ | ✓ | ✓ | 通过 |
| og_mysql | ✓ | ✓ | ✓ | 通过 |

- `information_schema` 对**非属主用户**隐藏 FK 的 `constraint_column_usage`；测试改为让抽取用户
  （miguser）作为源表属主后 FK 正常导出（实际环境需授 `REFERENCES`/属主权限）。

### 双向迁移矩阵（dept/emp，dept 4 行 + emp 8 行）

每个模式跑 4 条方向：`opengauss→mysql`、`mysql→opengauss`、`opengauss→postgres`、`postgres→opengauss`。

| 模式 | og→my | my→og | og→pg | pg→og |
|------|-------|-------|-------|-------|
| og_pg (PG) | ✅ | ✅ | ✅ | ✅ |
| og_ora (A) | ✅ | ✅ | ✅ | ✅ |
| og_mysql (B) | ✅ | ✅ | ✅(见下) | ✅ |

**B 模式特殊点**：dolphin 把 `DECIMAL(9,2)` 在 information_schema 中上报为 `number(9,2)`。
因 `opengaussdb` 被归为 **postgres family**，`opengaussdb→postgres` 走同族路径，`BuildCreateTableViaDialect`
不触发跨族类型转换，DDL 直接渲染 `number`，PG 报 `类型 "number" 不存在`。
**临时绕过**：在目标 config 配 `ddl.type_overrides: {NUMBER: "NUMERIC(%p,%s)"}`（已实测通过）。

### 迁移命令的两个配置缺口（非 openGauss 特有，但 migrate 默认不一致）

1. `export.csv.header` 默认为 false（不写表头），而 `importer` 总是把 CSV 首行当表头读取 →
   migrate 需显式 `export.csv.header: true`。
2. exporter 把 `time.Time` 硬编码为 `20060102150405`（`yyyyMMddHHmmss`），而 importer 只有配了
   `import.data_transforms.datetime_format: "yyyyMMddHHmmss"` 才会转回 `date` →
   migrate 需显式配该值（`mysql_to_pg.yaml` 已如此）。

### 多模式注册名设计（对应"不同元数据查询途径"）

当前仅有 `opengaussdb`（PG 方言，100% 继承 PG）。三种兼容模式类型/元数据不一致，建议对齐 PanWeiDB：

| 注册名 | 模式 | 方言/家族 | 元数据提取 |
|--------|------|-----------|-----------|
| `opengaussdb` | PG | postgres | PG 提取器 + openGauss 修正（`pg_sequences`/`pg_matviews` 回退） |
| `opengaussdb-oracle` | A | oracle | Oracle 提取器（`all_tables`/`all_sequences`）+ 空串=NULL 语义 |
| `opengaussdb-mysql` | B | mysql | MySQL 提取器 + dolphin 类型归一（`number`→`numeric` 等） |

驱动统一为 `opengauss-connector-go-pq`（注册名 `opengauss`，PG 线协议），仅 DSN 的 `dbname` 区分库。

### 多模式注册名已实现（2026-09-05）

- `internal/dialect/opengaussdb/opengaussdb.go`：新增 `NewOracle()`（A 模式，Oracle 类型映射/引用/DDL + PG features）与
  `NewMySQL()`（B 模式，MySQL 类型映射/反引号/DDL + PG features，去除 `ENGINE=` 子句）。
- `internal/registry/registry.go`：注册 `opengaussdb-oracle`、`opengaussdb-mysql`。
- 元数据抽取：`extractor.normalizeDBType` 将 `opengaussdb*` 全部归为 `postgres`（openGauss 各模式均用 PG 目录 + `$N` 占位符）。
- 线协议：`dbconn.Family`/`service.TargetTypeFamily`/`isPostgres` 将 `opengaussdb*` 全部归为 `postgres`；
  `isMySQL`/`isOracle` 对 `opengaussdb-mysql`/`opengaussdb-oracle` 返回 false（用 `$N`）。
- 驱动名：`driverName` 对 `opengaussdb*` 返回 `opengauss`。

**实测**：新类型下 `export-metadata` 三库均通过；`mysql→og_ora`（opengaussdb-oracle 目标）、`mysql→og_mysql`（opengaussdb-mysql 目标）、
`og_ora→mysql`、`og_mysql→mysql` 均 ✅ dept/emp/special 全量通过。openGauss A 模式接受 Oracle 式 DDL（NUMBER/VARCHAR2），B 模式接受 MySQL 式 DDL。

**顺带修复**：`oracle.FromLogicalType` 增加 `LBDate → "DATE"`（原 mysql `DATE` 映射跨方言到 Oracle 时落为默认 `VARCHAR2(4000)`）。

### 特殊字符 + 空值跨库保真实测

`special` 表覆盖 `"`、`'`、`#`、`@`、`!`、`%`、`,`、`;`、`:`、`\`、换行、制表符、中文/emoji，以及 NULL、空串、字面 `"NULL"`、字面 `"\N"`。

| 项 | 结果 |
|----|------|
| 特殊字符（引号/#/@/!/%/逗号/分号/反斜杠/换行/制表符/unicode） | ✅ 全保真（Go csv.Writer 对含逗号/引号/换行的字段加引号，Reader 还原） |
| SQL NULL | ✅ 导出为 `\N`，导入为 NULL |
| 空串 `''` | ✅ 保持空串（A 模式源库本身 `''` 即 NULL，为 openGauss 语义） |
| 字面 `"NULL"` 字符串 | ✅ 保真（未配 `null_if` 时） |
| 字面 `"\N"` 字符串 | ⚠️ **丢失** → 目标变 NULL（与默认 `\N` 空值标记冲突；导出器不引号 `\N`，导入器按 NULL 读） |

**空值形态**：导出统一为 `\N`（`export.csv.null_representation`）；导入接受 `import.csv.null_marker`（默认 `\N`）、
`null_if` 列表、空串（`empty_string_to_null`）。目标库接受 NULL 即 `\N`/空串映射后的值。
> `\N` 字面冲突为已知限制：若源数据含字面 `\N` 字符串，需改空值标记或改用不冲突的 `null_representation`。

### 逗号 / 换行符边界用例（补充）

在 `special` 表追加 4 行（id 3–6），覆盖逗号与换行的极端形态，og_pg/og_ora/og_mysql ↔ mysql/postgres 双向迁移均 6/6 行保真：

| id | 逗号（c_col） | 换行（n_col） | 结果 |
|----|--------------|--------------|------|
| 3 | `,a,,b,`（首尾+连续） | `l1\nl2\nl3`（多个换行） | ✅ 保真 |
| 4 | `,`（纯逗号） | `\nlead\n`（首尾换行） | ✅ 保真 |
| 5 | `a,b,c,d`（多逗号） | `trail\n`（尾换行） | ✅ 保真 |
| 6 | `,`（纯逗号） | `\n`（单独换行） | ✅ 保真 |

**CSV 表现**：含逗号/换行的字段一律被 `csv.Writer` 加引号（如 `",a,,b,"`、`"l1\nl2\nl3"`），`Reader` 还原，故首尾/连续逗号、多行/首尾/单独换行均无损。

### OB 租户特殊符号 + openGauss→OB 迁移（2026-09-05，OceanBase 4.4.2.2 已启动）

同一可复用数据集（`tools/ogtest` seedob，见 [e2e-dataset.md](e2e-dataset.md)）播种到 OB 租户并双向验证：

| 项 | 结果 |
|----|------|
| OB Oracle 租户（oratest/MIGSRC）存特殊字符（含逗号/换行/unicode） | ✅ 6/6 保真 |
| OB MySQL 租户（ogtest）存特殊字符 | ✅ 6/6 保真 |
| OB Oracle → mysql（OB 作源） | ✅ dept/emp/special 全过 |
| OB MySQL → mysql（OB 作源） | ✅ 全过 |
| openGauss(og_pg) → oceanbase-mysql | ✅ 全过 |
| openGauss(og_pg) → oceanbase-oracle | ✅ 全过 |

注意点（详见 e2e-dataset.md）：
- OB Oracle 会话 DML 需显式 `COMMIT`；DDL 自动提交。
- OB Oracle 目标建表为引号小写 `"dept"`；目标 schema 若有历史大写表（`DEPT`）需先清理再迁移，
  否则 `tableExists` 判存在跳过建表、导入按小写引用失败。
- OB Oracle 空串即 NULL（Oracle 语义）：源空串迁入后为 NULL（符合预期）。
