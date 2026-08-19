# 在线增量迁移 (Online Incremental Migration) — 设计稿

> 状态:部分实现。命令面 `owl-migrate online init/sync/status/init-runner/archive`、
> PG/Oracle 触发器 CDCBuilder、cdc 轮询/回放/checkpoint/file-batch/runner/archive、
> 声明式目标适配器均已实现并通过 TDD(e2e 走 live Oracle/PG/MySQL 容器)。
> 未实现/未来项见 §11。

## 1. 概述

为 go-owl-migrate 增加**在线增量数据迁移**能力:程序启动后,通过**触发器 CDC** 捕获源库增量变更(INSERT / UPDATE / DELETE / TRUNCATE),按**时间序**回放到目标库。

### 1.1 范围与边界

| 决策 | 取值 |
|---|---|
| 数据范围 | 只同步程序启动后产生的变更,**不做全量基线** |
| 增量捕获 | 触发器 CDC,**per-table** 粒度 |
| 条件查询增量 | 复用现有 migrate(时间戳/PK 轮询),不在本次范围 |
| 不支持库 | 无触发器/无 DDL 权限的数据库(纯文档库、依赖闪回特性的库)暂不考虑 |
| 变更表 | 每源表一张 `owl_chg_<表>`,本期**不做分区/分片**,结构预留扩展位 |
| 实时性 | native(秒级)/ client(秒~分)/ file-batch(分~小时,保底)三档 |

### 1.2 架构总览

```
源库(触发器 CDC)                        目标库
  owl_chg_<表>  ◄────── 触发器写入 (I/U/D/T)
       │
       ▼ 轮询 (按 chg_id 递增)
  owl-migrate online sync (常驻前台进程)
       │
       ├─ native      → 进程内 Go 驱动回放        (秒级)
       ├─ client      → 进程内调官方 CLI 回放      (秒~分)
       └─ file-batch  → 落盘 SQL 批次文件,外部 runner 执行 (分~小时,保底)
       │
       ▼
  checkpoint (StateStore: 默认 JSON+原子改名, -tags sqlite3 用 SQLite)
```

## 2. 命令面

```
owl-migrate online init           # 生成变更表 DDL + 触发器 DDL(per-table,按源方言)
                                 #   默认 --script 输出到 ./output/online/,用户 DBA 审阅后执行
                                 #   --apply   直连源库执行
                                 #   --tables  指定表,空 = 全部
                                 #   --require-key  无主键/唯一键的表拒绝生成并报错
owl-migrate online sync           # 常驻前台进程:native/client 直接回放;file-batch 落盘批次文件
                                 #   --tables 过滤 --on-error skip|stop|retry
owl-migrate online status         # 各表水位(filed/acked)、pending/done/failed 计数、错误概览
owl-migrate online init-runner    # 按目标适配器 client 模板生成 run_incremental.sh(file-batch)
owl-migrate online archive        # 扫 done/ → tar.gz → 移 archive/ → 删原文件(用户 cron 调用)
```

## 3. 配置

### 3.1 `migrate.yaml` 新增 `online:` 段

```yaml
online:
  cdc:
    changelog_prefix: owl_chg_     # 变更表名 = owl_chg_<表名>
    tables: [EMP, DEPT, BONUS]     # 为空 = 全部表
    apply: false                   # false = 输出脚本给 DBA 执行;true = 直连源库执行(等价 --apply)
    script_dir: ./output/online/   # apply=false 时的 SQL 输出目录
    require_key: false             # true 则无主键/唯一键的表在 init 阶段拒绝生成
  sync:
    poll_interval: 1s              # changelog 轮询间隔
    batch_size: 500                # 单批最大行数
    on_error: skip                 # skip|stop|retry,默认 skip
    error_table: owl_sync_error    # 目标侧错误记录表
  files:                           # 仅 mode=file-batch 使用
    pending: ./online/pending/
    done:    ./online/done/
    failed:  ./online/failed/
  archive:
    enabled: true
    format: tar.gz
    dir: ./online/archive/
  state:
    db: ./output/online/online.db  # 默认 JSON: ./output/online/state.json
```

`source` / `target` 沿用顶层现有配置;`target` 新增 `adapter` 字段引用插件文件:

```yaml
target:
  type: somepg
  dsn: "..."                       # native 模式用
  adapter: ./adapters/somepg.yaml  # mode=client|file-batch 时的插件文件
```

### 3.2 目标适配器插件(声明式 YAML)

```yaml
adapter:
  name: somepg
  mode: file-batch                 # native | client | file-batch
  quote: "\""
  placeholder: "$%d"               # ? / $1 / :name
  identifier_case: lower
  driver: postgres                 # 仅 native 模式:复用内置驱动
  client:                          # 仅 client / file-batch 模式
    command: "sqlplus"
    args_template: "{user}/{pw}@{host}:{port}/{service} -e \"@{file}\""   # {file}=批次文件路径
    transaction:
      begin: "BEGIN"               # 不支持显式 BEGIN 的库配 ""(如 Oracle,靠隐式事务 + 尾部 COMMIT)
      commit: "COMMIT"             # 必配:文件内必须 COMMIT
      wrap: true                   # true 时 runner 在文件前后包裹 begin/commit
  metadata:                        # 可选;仅 native 模式自动发现,client/file-batch 用手工 column_map
    list_tables:  "SELECT table_name FROM information_schema.tables WHERE table_schema=?"
    list_columns: "SELECT column_name, data_type FROM information_schema.columns WHERE table_name=? ORDER BY ordinal_position"
  type_map:
    VARCHAR:    "varchar(%l)"
    CHAR:       "char(%l)"
    INT:        "integer"
    BIGINT:     "bigint"
    SMALLINT:   "smallint"
    NUMERIC:    "numeric(%p,%s)"
    FLOAT:      "real"
    DOUBLE:     "double precision"
    DATE:       "date"
    TIME:       "time"
    DATETIME:   "timestamp"
    TIMESTAMP:  "timestamp"
    TIMESTAMPTZ: "timestamptz"
    INTERVAL:   "interval"
    INTERVALYM: "interval year to month"
    INTERVALDS: "interval day to second"
    BOOLEAN:    "boolean"
    CLOB:       "text"
    BLOB:       "bytea"
    JSON:       "jsonb"
    XML:        "xml"
    ENUM:       "text"
    BINARY:     "bytea"
    VARBINARY:  "bytea"
    ROWID:      "text"
    fallback:   "text"             # 未覆盖的逻辑类型 → 保守兜底
  column_map:                      # 源表列 → 目标列(手工映射;client/file-batch 模式必需)
    EMP:  { EMPNO: "emp_no", ENAME: "emp_name" }
```

要点:

- `type_map` 的 key 是仓库 `LogicalBase` 全集(25 种),源库 raw type 先经源方言 `TypeMapper` 归一化成逻辑类型,再映射到目标类型;`%l`/`%p`/`%s` 复用 `ApplyTypeOverride` 替换语法。
- `raw_aliases`(可选)可在 type_map 之上覆盖个别 exotic raw type。
- **值格式化与 type_map 无关**:JSON 里的值按逻辑类型格式化(字符串加引号、数字裸值、BLOB 按 hex/base64、日期加引号),由目标模板决定;type_map 只在目标要求显式 CAST 或建 DDL 时使用。

## 4. 变更表 (changelog)

每源表一张,`chg_id` 单调递增 = 时序 + checkpoint 游标:

```sql
-- Oracle 版示意
CREATE TABLE owl_chg_emp (
  chg_id   NUMBER GENERATED ALWAYS AS IDENTITY,  -- 或序列;单调递增
  shard_id NUMBER DEFAULT 0,                     -- 预留:本期恒 0,未来 L1/L2 分片
  op_type  VARCHAR2(1),      -- I / U / D / T(truncate)
  old_data CLOB,             -- 定位用:U/D 存旧全列 JSON;I/T 为 NULL
  new_data CLOB,             -- 写入用:I/U 存新全列 JSON;D/T 为 NULL
  chg_time TIMESTAMP         -- 审计
);
```

- 全列值以 JSON 存储,不感知源表结构变化。
- checkpoint 游标 = `SELECT ... WHERE chg_id > :last ORDER BY chg_id LIMIT :n`。

### 4.1 TRUNCATE 捕获矩阵

TRUNCATE 不是 DML,标准 DML 触发器不响应:

| 方言 | 捕获 | 说明 |
|---|---|---|
| PostgreSQL / OpenGauss / PanWei | ✅ | 原生支持 `AFTER TRUNCATE` 触发器 |
| Oracle | ⚠️ | 需 SCHEMA 级 DDL 触发器(`BEFORE/AFTER TRUNCATE ON SCHEMA`) |
| MySQL / GoldenDB / OceanBase(MySQL 模式) | ❌ | 触发器无法捕获 → 文档约定:限制应用 TRUNCATE 权限 + 对账兜底 |

## 5. 触发器生成 (CDCBuilder)

扩展 dialect 组合结构,新增能力接口:

```go
type CDCBuilder interface {
    BuildChangelogTable(t *md.TableDef, opts CDCOptions) (string, error) // 变更表 DDL
    BuildSyncTrigger(t *md.TableDef, opts CDCOptions) (string, error)    // 同步触发器 DDL
}

type CDCOptions struct {
    ChangelogTable string   // 默认 owl_chg_<表>
    ShardKey       []string // 预留:分片键,本期空
    ShardCount     int      // 预留:分片数,本期 1
}
```

各方言实现差异:

| 方言 | 触发器形态 | 定位方式 | 关键点 |
|---|---|---|---|
| Oracle | 1 个 `AFTER INSERT OR UPDATE OR DELETE` 多事件触发器 | `:NEW` / `:OLD` + PK | LOB 列不能在 AFTER ROW 里读,用 BEFORE;无 PK 退化 ROWID |
| MySQL | 单事件限制 → 3 个独立触发器 | `NEW.` / `OLD.` | 一表最多 6 触发器(够用);binlog 下需 `log_bin_trust_function_creators` |
| PG / OpenGauss / PanWei | 1 个 PL/pgSQL 函数 + `AFTER TRIGGER`,`TG_OP` 区分 | `NEW` / `OLD` | 最简,天然多事件 + TRUNCATE |

执行方式:`--apply` 直连源库执行 / `--script` 输出给 DBA。变更表 DDL 在前,触发器在后。

> 捕获起点锚点 = 触发器安装完成那一刻。启动 → 建变更表+触发器(事务提交)→ 之后才是捕获区间。

## 6. 回放执行模型

### 6.1 时序回放

要求**逐条按序回放**,非幂等。每批拉取 N 条(chg_id 升序),目标侧事务 + savepoint:

```
每批(≤ batch_size 行):
  目标侧开事务
  逐条: SAVEPOINT s → 执行目标 INSERT/UPDATE/DELETE/TRUNCATE
        出错 → ROLLBACK TO s → 写 owl_sync_error(源表, chg_id, op, 错误, 时间) → 继续
  批末: COMMIT → checkpoint 推进到本批 max(chg_id)
```

- `--on-error`:`skip`(默认,跳过该条继续)/ `stop`(退出)/ `retry N`。
- **错误粒度**:
  - native/client:行级(savepoint 跳过单条);
  - file-batch:**文件级**(客户端拿不到行级错误,整个文件进 `failed/`,退出码+stderr 记录,人工捞补)。

### 6.2 无主键/唯一键表的检查

不要求目标库有主键,但做分级检查:

- **init 阶段**:逐表检测 PK/唯一键。无键表输出 WARNING 清单(UPDATE/DELETE 将用 old_data 全列匹配,可能多匹配/漏匹配)。`--require-key` 时直接拒绝生成。
- **运行时(native/client 尽力)**:无键表 UPDATE/DELETE 校验目标**影响行数 == 1**,否则按 `--on-error` 处理。
- **file-batch**:拿不到 affected rows,退化为文件级,文档明示局限。

## 7. file-batch 协议(保底模式)

### 7.1 目录布局

```
./online/
  pending/    # 程序写这里(待执行)
  done/       # runner 执行成功 → 移这里(= ack)
  failed/     # 执行失败 → 移这里 + stderr 日志
  archive/    # archive 子命令:按 {表}/{日期}/ 组织 tar.gz
  run_incremental.sh   # init-runner 生成
```

### 7.2 批次文件命名

```
{seq:06d}_{开始时间}_{结束时间}_{表名}_{start_chgid}-{end_chgid}.sql
例:000001_20260818090000-20260818091500_EMP_0000000001234-0000000002000.sql
```

- `seq` 全局有序,runner 按 seq 升序执行;时间范围给人看,`chg_id` 范围给程序推进水位。
- 表名 sanitize 非法文件名字符。
- `op_type=T` 单独成文件(TRUNCATE 会隐式提交,避免破坏文件级原子性)。
- 一个文件可含多条记录(单个 INSERT 多行 `VALUES (...),(...)` 打包,减少客户端启动次数)。

### 7.3 原子性与水位

- **原子落盘**:写 `.tmp` → 完整后 `rename` 成正式名,runner 永远不会看到半截文件。
- **双水位**:
  - `filed_chgid`:文件落盘即推进,重启续拉不重产;
  - `acked_chgid`:从 done/ 推导(最小未 done seq 的前驱区间),用于对账/积压监控。
- 崩溃窗口:落盘与水位持久化之间重启 → 最坏一批重复,PK 冲突走 failed/ 跳过,可接受。

### 7.4 runner 行为(`init-runner` 生成)

```
按 seq 升序扫 pending/
  执行 args_template({file}=文件路径)
  退出码 0   → mv 到 done/
  非 0       → mv 到 failed/ + 记录 stderr,继续下一个(不暂停)
```

## 8. 状态库 (StateStore)

`StateStore` 接口,双实现:

```go
type StateStore interface {
    LoadCheckpoints() (map[string]Checkpoint, error)
    SaveCheckpoint(cp Checkpoint) error
    Stats() (Stats, error)
}
```

- **默认 = JSON + 原子改名**:`./output/online/state.json`,零依赖、跨平台、与 `migrate_progress.json` 风格一致。
- **`-tags sqlite3` = SQLite**:`./output/online/online.db`,原子写 + 并发读(status 与 sync 并存)。

```sql
CREATE TABLE checkpoint (
  table_name  TEXT PRIMARY KEY,
  shard_id    INT  DEFAULT 0,
  filed_chgid INT  NOT NULL DEFAULT 0,
  acked_chgid INT  NOT NULL DEFAULT 0,
  updated_at  TEXT
);
```

> checkpoint 数据量极小(千表也仅 KB 级),"数据库压缩"不是收益点;**原子写 + 并发读**才是选 SQLite 的理由。因 `mattn/go-sqlite3` 是 CGO,默认构建用 JSON,`-tags sqlite3` 才启用 SQLite。

## 9. 归档 (archive)

- `owl-migrate online archive` 由用户 cron 定期调用。
- 行为:扫 `done/` → 按(表,日期)打 `tar.gz` → 移入 `archive/` → 删除原 .sql。
- 清理由用户侧 cron 完成,工具不删除归档:

```bash
# 每 6 小时压缩一次
0 */6 * * *  owl-migrate online archive -c cfg.yaml
# 每天删 30 天前的归档
0 3 * * *    find ./online/archive -name '*.tar.gz' -mtime +30 -delete
```

## 10. 包布局

```
internal/dialect/<dialect>/*.go   # 各方言新增 CDCBuilder 实现(变更表 DDL + 触发器 DDL)
internal/cdc/                     # 变更表/触发器生成编排、changelog 轮询、回放执行、checkpoint
  generator.go                    # init:生成变更表 DDL + 触发器 DDL(--apply / --script)
  poller.go                       # 轮询 changelog,推进 filed/acked
  replacer.go                     # native/client 行级回放(savepoint + on-error)
  batch_writer.go                 # file-batch 落盘(原子 rename + 双水位)
  state.go                        # StateStore 接口 + JSON 实现
  state_sqlite.go                 # StateStore SQLite 实现(//go:build sqlite3)
internal/adapter/                 # 插件适配器:YAML 解析 → TargetAdapter 接口
  adapter.go                      # TargetAdapter 接口 + 加载
  native.go / client.go / filebatch.go
internal/cmd/online_*.go          # online init/sync/status/init-runner/archive 子命令
```

## 11. 未来扩展位(本期不实现)

- **变更表分区/分片**:`shard_id` 字段已预留;`CDCOptions.ShardKey/ShardCount` 接口位已留;checkpoint 表含 `shard_id`。未来按 PK/唯一键/指定列取模分表,多实例静态分片 + 源库 advisory lock 防撞。
- **跨分片事务/外键顺序**:按表分组分片 / 目标侧 DEFERRABLE / 对账兜底(方案见设计讨论,未实现)。
- **client 模式元数据发现**:解析 CLI 输出自动建列映射(当前用手工 `column_map`)。
- **幂等回放开关**:INSERT ... ON CONFLICT / MERGE 的"最终一致"模式(当前是严格时序回放)。

## 12. 任务拆解(建议实施顺序)

1. `config`:`OnlineConfig` 结构 + `target.adapter` 字段 + 校验 + `init` 模板。
2. `dialect`:新增 `CDCBuilder` 接口;Oracle/PG/MySQL 三方言实现变更表 + 触发器生成(含 T 捕获矩阵)。
3. `cmd online init`:生成逻辑 + `--apply`/`--script`/`--tables`/`--require-key` + 无键 WARNING。
4. `adapter`:YAML 加载 + `TargetAdapter` 接口 + 三档 runner 骨架 + 完整 type_map。
5. `cdc`:`StateStore`(JSON + SQLite 双实现)、poller、checkpoint、错误表。
6. `cmd online sync` + `status`:native/client 行级回放(savepoint + on-error)。
7. `cmd online init-runner` + `batch_writer`:file-batch 落盘协议 + runner 脚本生成。
8. `cmd online archive` + 压缩。
9. 文档:CLI 参考、配置参考、e2e 手工测试。
