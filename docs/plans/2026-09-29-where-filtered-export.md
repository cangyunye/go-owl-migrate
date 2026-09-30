# 方案：export data / migrate 的 WHERE 条件导出（2026-09-29）

状态：**已落地**（2026-09-30，P1-P4 提交于 feat/ai-chat-router-research；e2e：`scripts/e2e_where_filter.sh` ALL PASS——oracle 同库双用户列序/改名/行数断言、mysql 列裁剪、门禁负路径、online 拒绝）。前置事实（已对代码核实）：

1. `migrate_cmd.go` Step 5 与 `export data` 走**同一个** `exporter.ExportTables`——WHERE 做在 exporter 层，两个命令一处受益。
2. `buildBatchQuery` 有四条分页路径需注入：无 PK OFFSET、ORDER BY+LIMIT、keyset 游标、Oracle legacy ROWNUM（`buildOracleLegacyQuery`）。
3. agent 通道也走 `ExportTables`（SQL 层注入对 native/agent 天然同权）。
4. import 读 CSV，无需改动；`gen-select` 本就支持任意 SELECT，不动。
5. `TableResult.Rows` 已如实计数，条件导出后 expected/actual 自动一致。

## 1. 配置 schema（推荐：glob 键 map，向后兼容）

```yaml
export:
  tables:
    include: ["SCOTT.EMP", "SCOTT.*"]        # 现有形态不动
  filters:                                   # 新增：表选择 → WHERE 片段
    "SCOTT.EMP": "deptno = 20 AND sal > 1000"     # 精确点名
    "*.LOG_*":   "created >= DATE '2026-01-01'"   # glob
```

- key 复用 `metadata.ObjectSelector` 的 glob 语义（`S.T`/`S.*`/`*.T`/`T_*`，大小写不敏感）——与表选择同一套心智。
- **优先级与歧义**：精确点名 > glob；同表命中多条 filter → `config.validate` 直接报错（不做隐式 AND 合并，避免重叠 glob 拼出意外语义）。
- CLI：`export data --where 'SCOTT.EMP: deptno=20'`（逗号分隔多条；flag 优先于配置，与 `--tables` 同级）。`migrate --where` 同位覆盖。
- serve：`/export`、`/migrate` 任务 body 增 `filters: {...}` 透传。
- 被否掉的备选：把 `tables.include` 改造成结构化对象列表 `[{name, where}]`——破坏现有配置形态与全部既有文档/语料，迁移成本不成比例。

## 2. exporter 注入点（一处改动，export data + migrate 同时生效）

`exporter.Config` 增 `Filters map[string]string`；`ExportTables` 对每张表 resolve 一次（精确 > glob，无命中=无过滤）：

- 四条分页路径的注入形态：
  - 无 PK：`SELECT … FROM t WHERE <user> <limit>`；
  - ORDER BY+LIMIT：`… WHERE <user> ORDER BY …`；
  - keyset 游标：`WHERE <user> AND <cursor> ORDER BY …`；
  - Oracle legacy：内层 ROWNUM 包装查询注入 `WHERE <user>`。
- **绑定参数冲突**：keyset 游标占用 `placeholder(0..n)`，用户片段**禁止含 `?` / `:n` 占位符**——WHERE 是字面 SQL 片段，不参数化（参数化需类型推断，超出范围；信任级别同 `ddl.*` 等运维配置项）。
- **消毒**（`config.validate` + exporter 双层）：含 `;`（多语句）或 `--` / `/*`（截断注释）→ 报错；含易变函数（`NOW()/CURRENT_DATE/SYSDATE/SYSDATE()/GETDATE()/RANDOM()`）→ warning（不阻断）。
- **确定性要求**（文档 + warning）：keyset 分页要求 WHERE 对同一行永远同值。`created > now()-interval '1' hour` 这类条件会造成批间漂移（漏行/重行）——只允许确定性谓词。

## 3. 语义边界（文档必须写明 + 代码守卫）

| 边界 | 处理 |
|---|---|
| **migrate + WHERE** | 目标表 = 源表子集。报告加 `filtered: true` 与所用条件标注，避免"迁移完成"被误读为全量；`resume` 检测 filters 变更 → 判 stale 提示重跑（progress 按 tableKey 存导出状态，不存游标值，条件变更后旧快照不可信）。 |
| **online CDC 互斥** | 触发器回放按行不按条件：初始装载是子集，但 changelog 会回放该表**所有**变更行（含条件外），目标会逐渐"长全"。`online init` 检测 `export.filters` 非空 → **硬报错拒绝**，错误信息指向：条件装载 + 增量回放语义不成立，请用全量或放弃增量。 |
| **无 PK 表** | OFFSET 分页 + 非确定性 WHERE 风险叠加 → 校验 warning。 |
| **列不存在/语法错** | 数据库报错自然 surfaced（带表名），不预解析 WHERE。 |

## 4. AI 层联动

- 路由：硬约束 #7 删除；r22 的 sub 由 `unsupported-filter` 翻转为 `where`；新增多轮用例「继续导出 Where 条件 2」（会话续接：filter 槽位覆盖，`continuity.mode=continued`）。
- `prompt_plan.md`：export 段补 `filters` 字段参考 + "条件必须确定性"提示。
- `/ai/plan` 生成的 `filters` 由 `config.Load` 消毒规则兜底幻觉；会话槽位增 `filter`（每轮覆盖语义，天然支持"条件 1 → 条件 2"续问）。

## 5. 测试与验收

- 单测：四条分页路径 × WHERE 注入的 SQL 文本断言（sqlmock）；消毒规则表驱动；多 filter 歧义报错；`--where` flag 覆盖优先级。
- e2e（三容器）：pg `owl_users WHERE id <= 3` → CSV 2 行；migrate+where 全链路 → 目标表 2 行、报告 `filtered: true`；`--format sql/xlsx` × where；oracle OWL_USERS 条件导出。
- 语料回归：r22 翻转 + 2 条新用例过 `run_router_eval.py`。

## 6. 分阶段

| 阶段 | 内容 | 量级 |
|---|---|---|
| P1 | exporter 注入（四路径+消毒）+ `export data --where` + 单测/e2e | 主体，一天级切片 |
| P2 | migrate 接入（`--where`、报告标注、resume stale 检测） | 小 |
| P3 | online 硬拒绝 + serve 任务 body 透传 | 小 |
| P4 | AI 联动（语料翻转/新用例、prompt_plan、会话 filter 槽位）+ 文档 | 小 |
