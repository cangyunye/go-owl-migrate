# xlsx 数据源测试用例

> 测试 `gen-insert` 和 `gen-ddl` 命令使用 xlsx 文件作为元数据/数据源。

## 测试数据

测试数据已经准备好：**`./testdata/xlsx/scott.xlsx`** —— 包含完整的 SCOTT 模式（EMP/DEPT/BONUS）。

文件结构：

| Sheet 名 | 类型 | 内容 |
|----------|------|------|
| `tables` | 元数据 | 3 个表的定义 |
| `columns` | 元数据 | 15 个列定义（NUMBER, VARCHAR2, DATE 等 Oracle 类型） |
| `primary_keys` | 元数据 | EMP/DEPT 的主键 |
| `foreign_keys` | 元数据 | EMP→DEPT 外键 + EMP→EMP 自引用外键 |
| `indexes` | 元数据 | 2 个非唯一索引 |
| `@EMP` | 数据 | 14 行员工数据（KING、SMITH、SCOTT 等） |
| `@DEPT` | 数据 | 4 行部门数据 |
| `@BONUS` | 数据 | 3 行奖金数据 |

> `@TableName` 是数据 sheet 的命名约定 —— xlsx 加载器会把它们抽取为 CSV 文件用于 `gen-insert`。

如果 `scott.xlsx` 不存在或需要重新生成，运行：

```bash
go run ./testdata/xlsx/gen_test_xlsx.go
```

## 准备工作

```bash
# 清理旧输出
rm -rf /tmp/owl-xlsx-test
mkdir -p /tmp/owl-xlsx-test
```

---

## 测试用例 1：交互模式生成 xlsx 配置（gen-insert）

```bash
go run ./cmd/migrate/main.go init -o /tmp/owl-xlsx-test/migrate.yaml
```

依次回答：
| 提问 | 输入 |
|------|------|
| What do you want to do? | `gen-insert` |
| Data source type | `xlsx` |
| Target database dialect | `postgres` |
| xlsx file path | `./testdata/xlsx/scott.xlsx` |
| Directory for extracted CSV data files | `/tmp/owl-xlsx-test/data/` |

**期望生成的 yaml**（约 13 行）：

```yaml
general:
    log_level: info
metadata:
    type: xlsx
    xlsx:
        path: ./testdata/xlsx/scott.xlsx
        data_output_dir: /tmp/owl-xlsx-test/data/
ddl:
    target_dialect: postgres
    include_comments: false
    include_if_not_exists: false
    no_quote_identifiers: false
import:
    source_dir: /tmp/owl-xlsx-test/data/
```

**验证：**

```bash
cat /tmp/owl-xlsx-test/migrate.yaml
```

---

## 测试用例 2：命令行模式生成 xlsx 配置

```bash
go run ./cmd/migrate/main.go init \
  -t postgres \
  -m xlsx \
  --scenario gen-insert \
  -o /tmp/owl-xlsx-test/migrate-cli.yaml

# 编辑 xlsx 路径为实际路径
sed -i '' 's|./metadata/schema.xlsx|./testdata/xlsx/scott.xlsx|' /tmp/owl-xlsx-test/migrate-cli.yaml
sed -i '' 's|./output/data/|/tmp/owl-xlsx-test/data/|g' /tmp/owl-xlsx-test/migrate-cli.yaml

cat /tmp/owl-xlsx-test/migrate-cli.yaml
```

---

## 测试用例 3：用 xlsx 配置运行 gen-insert（核心 E2E）

```bash
# 用方案 1 或 2 生成的配置文件
go run ./cmd/migrate/main.go gen-insert \
  -c /tmp/owl-xlsx-test/migrate.yaml \
  -o /tmp/owl-xlsx-test/insert/ \
  --dialect postgres
```

**期望输出：**

```
  @EMP → /tmp/owl-xlsx-test/data/scott.emp.csv (14 rows)
  @DEPT → /tmp/owl-xlsx-test/data/scott.dept.csv (4 rows)
  @BONUS → /tmp/owl-xlsx-test/data/scott.bonus.csv (3 rows)
Loaded 3 tables from xlsx
  /tmp/owl-xlsx-test/insert/scott.bonus.insert.sql
  /tmp/owl-xlsx-test/insert/scott.dept.insert.sql
  /tmp/owl-xlsx-test/insert/scott.emp.insert.sql
Generated 3 INSERT SQL files
```

**验证 1：抽取出来的 CSV 数据文件**

```bash
ls -la /tmp/owl-xlsx-test/data/
wc -l /tmp/owl-xlsx-test/data/*.csv
# 期望：emp 15 行(1 头+14 数据), dept 5 行, bonus 4 行
```

**验证 2：生成的 INSERT SQL 类型映射正确**

```bash
head -10 /tmp/owl-xlsx-test/insert/scott.emp.insert.sql
```

期望前 6 行类似（注意 NUMBER 列没有引号、VARCHAR2 列有引号、DATE 列有引号）：

```sql
BEGIN;

INSERT INTO "SCOTT"."EMP" ("EMPNO", "ENAME", "JOB", "MGR", "HIREDATE", "SAL", "COMM", "DEPTNO")
VALUES
  (7369, 'SMITH', 'CLERK', 7902, '1980-12-17', 800, '', 20),
  (7499, 'ALLEN', 'SALESMAN', 7698, '1981-02-20', 1600, 300, 30),
```

---

## 测试用例 4：用 xlsx 配置运行 gen-ddl

把 yaml 里的 `target_dialect: postgres` 保持不变，改用 gen-ddl：

```bash
go run ./cmd/migrate/main.go gen-ddl \
  -c /tmp/owl-xlsx-test/migrate.yaml \
  -o /tmp/owl-xlsx-test/ddl/
```

**期望输出：**

```
  /tmp/owl-xlsx-test/ddl/scott.emp.sql
  /tmp/owl-xlsx-test/ddl/scott.dept.sql
  /tmp/owl-xlsx-test/ddl/scott.bonus.sql
  /tmp/owl-xlsx-test/ddl/scott.idx_emp_ename.sql
  /tmp/owl-xlsx-test/ddl/scott.idx_emp_deptno.sql
Generated 5 DDL files
```

**验证：**

```bash
cat /tmp/owl-xlsx-test/ddl/scott.emp.sql
```

应包含 PostgreSQL 方言的 `CREATE TABLE` 语句，列类型已从 `NUMBER` → `NUMERIC`、`VARCHAR2` → `VARCHAR` 等。

---

## 测试用例 5：xlsx 用作 gen-ddl 场景（命令行）

不同于 gen-insert，gen-ddl 不需要数据 sheet：

```bash
go run ./cmd/migrate/main.go init \
  -t oracle \
  -m xlsx \
  --scenario gen-ddl \
  -o /tmp/owl-xlsx-test/ddl-cfg.yaml

sed -i '' 's|./metadata/schema.xlsx|./testdata/xlsx/scott.xlsx|' /tmp/owl-xlsx-test/ddl-cfg.yaml

# 生成 Oracle 方言 DDL（保持原 schema 名）
go run ./cmd/migrate/main.go gen-ddl \
  -c /tmp/owl-xlsx-test/ddl-cfg.yaml \
  -o /tmp/owl-xlsx-test/ddl-oracle/

cat /tmp/owl-xlsx-test/ddl-oracle/scott.emp.sql
```

---

## 一键全测试脚本

```bash
#!/bin/bash
set -e
cd /Volumes/ORICO2T/Users/sinvigil/Programming/owl/go-owl-migrate

# 0. 清理 + 准备
rm -rf /tmp/owl-xlsx-test
mkdir -p /tmp/owl-xlsx-test

# 1. 重新生成测试 xlsx（如果不存在）
[ -f ./testdata/xlsx/scott.xlsx ] || go run ./testdata/xlsx/gen_test_xlsx.go

# 2. 命令行模式生成 gen-insert 配置
echo "=== Test 1: gen-insert config (xlsx) ==="
go run ./cmd/migrate/main.go init \
  -t postgres -m xlsx --scenario gen-insert \
  -o /tmp/owl-xlsx-test/migrate.yaml
sed -i '' 's|./metadata/schema.xlsx|./testdata/xlsx/scott.xlsx|' /tmp/owl-xlsx-test/migrate.yaml
sed -i '' 's|./output/data/|/tmp/owl-xlsx-test/data/|g' /tmp/owl-xlsx-test/migrate.yaml
cat /tmp/owl-xlsx-test/migrate.yaml
echo ""

# 3. gen-insert 端到端
echo "=== Test 2: gen-insert E2E ==="
go run ./cmd/migrate/main.go gen-insert \
  -c /tmp/owl-xlsx-test/migrate.yaml \
  -o /tmp/owl-xlsx-test/insert/
echo ""
echo "--- Extracted CSV files ---"
wc -l /tmp/owl-xlsx-test/data/*.csv
echo ""
echo "--- Generated INSERT preview ---"
head -10 /tmp/owl-xlsx-test/insert/scott.emp.insert.sql
echo ""

# 4. gen-ddl 端到端
echo "=== Test 3: gen-ddl E2E ==="
go run ./cmd/migrate/main.go init \
  -t postgres -m xlsx --scenario gen-ddl \
  -o /tmp/owl-xlsx-test/ddl-cfg.yaml
sed -i '' 's|./metadata/schema.xlsx|./testdata/xlsx/scott.xlsx|' /tmp/owl-xlsx-test/ddl-cfg.yaml

go run ./cmd/migrate/main.go gen-ddl \
  -c /tmp/owl-xlsx-test/ddl-cfg.yaml \
  -o /tmp/owl-xlsx-test/ddl/
echo ""
echo "--- Generated DDL preview ---"
cat /tmp/owl-xlsx-test/ddl/scott.emp.sql

echo ""
echo "=== ALL TESTS PASSED ==="
```

保存到 `/tmp/test_xlsx.sh` 后执行：

```bash
bash /tmp/test_xlsx.sh
```

---

## 期望结果总览

| 测试 | 期望结果 | 自动验证 |
|------|---------|---------|
| 1. 交互生成 xlsx 配置 | yaml 含 `metadata.type: xlsx` 和 `xlsx.path` | 文件内容 |
| 2. 命令行生成 xlsx 配置 | 同上，需 sed 改路径 | 文件内容 |
| 3. gen-insert E2E | 抽出 3 个 CSV，生成 3 个 INSERT SQL | wc -l 匹配 |
| 4. gen-ddl E2E (PG) | 5 个 DDL 文件（3 表 + 2 索引） | 文件存在 |
| 5. gen-ddl E2E (Oracle) | Oracle 方言 DDL | DDL 中 `NUMBER`、`VARCHAR2` 类型保留 |

## 故障排查

| 错误 | 原因 | 解决 |
|------|------|------|
| `xlsx file path is required` | yaml 里 `xlsx.path` 为空 | 编辑 yaml 填路径 |
| `tables sheet not found` | xlsx 缺少 `tables` 元数据 sheet | 重新生成测试 xlsx |
| `data sheet @X has no matching table definition` | `@X` 在 columns 表里没有定义 | 检查 columns sheet 是否有 X 表 |
| 生成的 SQL 里 NUMBER 列被加引号 | columns 元数据没正确解析 | 重跑 gen_test_xlsx.go |
