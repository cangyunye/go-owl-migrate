# Web UI 完整测试配置

本目录提供一套可直接通过 Web UI「配置」页**上传**的配置，用于完整测试导出 / 导入 / 迁移功能（对应测试库 `testdata/db/docker-compose.yaml`）。

## 数据库账号速查

| 库 | 端口 | 账号 | 库名 | 状态 |
|---|---|---|---|---|
| Oracle | 1521 | `scott/tiger` | XEPDB1 | 有 EMP(14)/DEPT(4) 数据 |
| PostgreSQL | 5432 | `postgres/postgres123` | postgres_db | 作为目标库 |
| MySQL | 3306 | `root/root123456` | default_db | **默认空库，需先灌数据** |

---

## 第 0 步：准备环境

```bash
# 1. 启动测试数据库
cd testdata/db && docker compose up -d && cd ../..

# 2. 确认 Oracle 源库有数据（迁移/导出的数据来源）
docker exec oracle bash -c "echo 'SELECT count(*) FROM emp;' | sqlplus -s scott/tiger@XEPDB1"
# 预期返回 14

# 3.（可选）给 MySQL 灌测试数据（仅测 MySQL→PG 时需要）
docker exec -i mysql mysql -uroot -proot123456 default_db < testdata/db/mysql/setup.sql

# 4. 清理旧输出
rm -rf ./output && mkdir -p ./output/data
```

## 第 1 步：启动 Web 服务

> ⚠️ **必须在项目根目录启动**（配置里的元数据 CSV 用相对路径 `./testdata/db/oracle/`）

```bash
go run ./cmd/migrate/main.go serve
# 浏览器打开 http://127.0.0.1:8080
```

---

## 测试 A：完整迁移 Oracle → PostgreSQL

**配置文件：** `testdata/web/migrate_oracle_to_pg.yaml`

1. 「配置」页 → 上传 `migrate_oracle_to_pg.yaml`（右上「已有配置？」区）
   - 上传后顶部状态栏应显示目标方言 `postgres`、元数据 `csv`
2. 「迁移」页 → 确认是**标签页**（直接迁移 / SQL 输出），不是下拉框
3. 选「**直接迁移**」标签 → 点「开始迁移」
4. ✅ **预期**：进度框通过 WebSocket 实时滚动：
   ```
   [1] export_complete SCOTT.DEPT → 4 rows
   [2] export_complete SCOTT.EMP → 14 rows
   [3] import_complete SCOTT.DEPT → 4 rows
   [4] import_complete SCOTT.EMP → 14 rows
   ```
5. 「任务」页 → 点任务 ID 进详情 → 检查点表显示 DEPT/EMP 均 `SUCCESS`

**验证数据：**
```bash
docker exec postgres psql -U postgres -d postgres_db -c 'SELECT count(*) FROM "EMP";'   # 14
docker exec postgres psql -U postgres -d postgres_db -c 'SELECT count(*) FROM "DEPT";'  # 4
```

### 测试 A2：SQL 输出模式
1. 同一配置，「迁移」页切「**SQL 输出**」标签 → 开始迁移
2. ✅ 预期：只导出 + 生成 INSERT SQL，**不写目标库**
3. 生成的 SQL 在 `<temp-dir>/<job-id>/insert/` 下（看服务终端输出）

---

## 测试 B：导出 + 导入（分步）

### B1 导出
**配置文件：** `testdata/web/export_oracle.yaml`
1. 上传配置 → 「导出」页 → 开始导出
2. ✅ 预期：进度滚动 `export_complete`，CSV 写入 `./output/data/`（SCOTT.EMP.csv、SCOTT.DEPT.csv）
3. 「任务」详情可见每表导出行数

```bash
ls ./output/data/        # 应有 SCOTT.EMP.csv SCOTT.DEPT.csv
wc -l ./output/data/SCOTT.EMP.csv   # 15（1 表头 + 14 行）
```

### B2 导入
**配置文件：** `testdata/web/import_to_pg.yaml`（从 `./output/data/` 读取）
1. 上传配置 → 「导入」页 → 开始导入
2. ✅ 预期：进度滚动 `import_complete`，自动建表并导入
3. 验证同测试 A

---

## 测试 C：MySQL → PostgreSQL（可选）

**配置文件：** `testdata/web/migrate_mysql_to_pg.yaml`（需先完成第 0 步的 MySQL 灌数据）

流程同测试 A。验证：
```bash
docker exec postgres psql -U postgres -d postgres_db -c 'SELECT count(*) FROM "EMP";'
```

---

## 任务生命周期测试

| 操作 | 步骤 | 预期 |
|---|---|---|
| **取消** | 迁移进行中点「取消」 | 状态 `cancelling` → `cancelled`，Worker 完成当前表后退出 |
| **恢复** | 对 interrupted/cancelled 任务，详情页点「恢复」 | 新建任务，跳过已完成表 |
| **崩溃恢复** | 迁移中 `Ctrl-C` 杀服务 → 重新 `serve` | 启动日志提示把 running 标记为 interrupted |

---

## 排查

| 现象 | 原因 | 解决 |
|---|---|---|
| 任务一直 `running` 无进度 | 旧版未给 export/import 接进度 | 已修复，重新 `go run` 启动 |
| `unknown flag: --temp-dir` | 旧版 spawner bug | 已修复 |
| `tables.csv not found` | 没在项目根目录启动 serve | `cd` 到项目根再启动 |
| Oracle `ORA-12154` | DSN 错 | 确认 `oracle://scott:tiger@127.0.0.1:1521/XEPDB1` |
| PG `relation "EMP" does not exist` | 表名大小写 | 查询用 `"EMP"` 带引号 |
| MySQL 无表 | 空库 | 先执行第 0 步的 setup.sql |
