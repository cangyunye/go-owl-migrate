# go-owl-migrate E2E 测试用例手册

> **数据库容器状态（已启动）：**
> - **Oracle** (XEPDB1) — 已启动 2 天，SCOTT 用户下有 EMP(14行)、DEPT(4行) 等表
> - **PostgreSQL** (postgres_db) — 已启动 2 天，public 下有 EMP(14行)、DEPT(4行) 表（用引号创建）
> - **MySQL** (default_db) — 已启动 36 小时，**空数据库**（无表）
> - **OpenGauss** — 重启中，暂时不可用

---

## 前置准备

### 1. 确认容器运行正常

```bash
docker ps --format "table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}"
```

确认 oracle、postgres、mysql 均为 Up 状态。

### 2. 确认源数据库有数据

```bash
# Oracle
docker exec oracle bash -c "echo 'SELECT count(*) FROM emp;' | sqlplus -s scott/tiger@XEPDB1"

# PostgreSQL
docker exec postgres psql -U postgres -d postgres_db -c "SELECT count(*) FROM \"EMP\";"

# MySQL
mysql -h 127.0.0.1 -P 3306 -u root -proot123456 -e "SELECT 1;"
```

### 3. 清理之前的残留

```bash
rm -rf ./output/
mkdir -p ./output/data/ ./output/insert/ ./output/select/ ./output/ddl/ ./output/metadata/ ./output/temp/
```

### 4. 准备测试数据文件

上一轮已创建的 CSV 数据文件在 `./output/data/` 下（SCOTT.EMP.csv、SCOTT.DEPT.csv、SCOTT.BONUS.csv），如果被删了重新执行：

<details>
<summary>创建 EMP 数据</summary>

```bash
cat > ./output/data/SCOTT.EMP.csv << 'CSVEOF'
EMPNO,ENAME,JOB,MGR,HIREDATE,SAL,COMM,DEPTNO
7369,SMITH,CLERK,7902,1980-12-17,800,,20
7499,ALLEN,SALESMAN,7698,1981-02-20,1600,300,30
7521,WARD,SALESMAN,7698,1981-02-22,1250,500,30
7566,JONES,MANAGER,7839,1981-04-02,2975,,20
7654,MARTIN,SALESMAN,7698,1981-09-28,1250,1400,30
7698,BLAKE,MANAGER,7839,1981-05-01,2850,,30
7782,CLARK,MANAGER,7839,1981-06-09,2450,,10
7788,SCOTT,ANALYST,7566,1987-04-19,3000,,20
7839,KING,PRESIDENT,,1981-11-17,5000,,10
7844,TURNER,SALESMAN,7698,1981-09-08,1500,0,30
7876,ADAMS,CLERK,7788,1987-05-23,1100,,20
7900,JAMES,CLERK,7698,1981-12-03,950,,30
7902,FORD,ANALYST,7566,1981-12-03,3000,,20
7934,MILLER,CLERK,7782,1982-01-23,1300,,10
CSVEOF
```
</details>

<details>
<summary>创建 DEPT 数据</summary>

```bash
cat > ./output/data/SCOTT.DEPT.csv << 'CSVEOF'
DEPTNO,DNAME,LOC
10,ACCOUNTING,NEW YORK
20,RESEARCH,DALLAS
30,SALES,CHICAGO
40,OPERATIONS,BOSTON
CSVEOF
```
</details>

<details>
<summary>创建 BONUS 数据</summary>

```bash
cat > ./output/data/SCOTT.BONUS.csv << 'CSVEOF'
ENAME,JOB,SAL,COMM
SMITH,CLERK,800,
ALLEN,SALESMAN,1600,300
WARD,SALESMAN,1250,500
CSVEOF
```
</details>

---

## 第一部分：离线命令测试（不需要数据库连接）

### 测试 1：`init` — 生成配置文件

#### 1.1 非交互式初始化（Oracle → PostgreSQL）

```bash
go run ./cmd/migrate/main.go init \
  -m database \
  -s oracle \
  --source-dsn "oracle://scott:tiger@127.0.0.1:1521/XEPDB1" \
  --source-schema SCOTT \
  -t postgres \
  --target-dsn "host=127.0.0.1 port=5432 user=postgres password=postgres123 dbname=postgres_db sslmode=disable" \
  -o ./migrate.yaml
```

**验证：** `cat ./migrate.yaml` 应包含 source.type=oracle、target.type=postgres。

#### 1.2 交互式初始化

```bash
go run ./cmd/migrate/main.go init -o ./migrate_interactive.yaml
```

按提示依次回答：
1. 元数据来源类型 → 输入 `csv`
2. CSV 路径 → 输入 `./testdata/csv/`
3. 目标数据库类型 → 输入 `postgres`
4. 输出文件名 → 回车默认

---

### 测试 2：`validate` — 验证元数据

#### 2.1 验证 CSV 元数据

```bash
go run ./cmd/migrate/main.go validate -c ./migrate.yaml
```

**预期：** 验证通过，列出 SCOTT.EMP、SCOTT.DEPT、SCOTT.BONUS 表。

#### 2.2 验证 Oracle 实时库元数据

```bash
go run ./cmd/migrate/main.go validate -c ./testdata/db/oracle_export.yaml
```

**预期：** 连接 Oracle 成功，验证 SCOTT 模式下的表。

---

### 测试 3：`show-query` — 查看元数据提取 SQL

```bash
# 查看所有对象类型的查询
go run ./cmd/migrate/main.go show-query postgres

# 只看 Oracle 的列提取
go run ./cmd/migrate/main.go show-query oracle columns

# 只看 MySQL 的表提取
go run ./cmd/migrate/main.go show-query mysql tables
```

**预期：** 输出对应的 SQL 查询语句。

---

### 测试 4：`gen-ddl` — 生成 DDL

```bash
# 4.1 默认 PostgreSQL 方言
go run ./cmd/migrate/main.go gen-ddl -c ./migrate.yaml
echo "=== 验证 ==="
ls ./output/ddl/
cat ./output/ddl/*.sql

# 4.2 Oracle 方言
go run ./cmd/migrate/main.go gen-ddl -c ./migrate.yaml -o ./output/ddl/oracle/
ls ./output/ddl/oracle/

# 4.3 不引用标识符
go run ./cmd/migrate/main.go gen-ddl -c ./migrate.yaml --no-quote-identifiers -o ./output/ddl/noquote/
cat ./output/ddl/noquote/*.sql | head -5
```

**预期：** 检查 `noquote` 版 DDL 表名列名没有双引号。

---

### 测试 5：`gen-select` — 生成 SELECT 分页语句

```bash
# 5.1 游标分页（默认）
go run ./cmd/migrate/main.go gen-select -c ./migrate.yaml
echo "=== 验证 ==="
ls ./output/select/
head -10 ./output/select/scott.emp.select.sql

# 5.2 OFFSET 分页
go run ./cmd/migrate/main.go gen-select -c ./migrate.yaml --batch-method offset -n 1000 -o ./output/select/offset/
cat ./output/select/offset/scott.emp.select.sql

# 5.3 不引用标识符
go run ./cmd/migrate/main.go gen-select -c ./migrate.yaml --no-quote-identifiers -o ./output/select/noquote/
cat ./output/select/noquote/scott.emp.select.sql
```

---

### 测试 6：`gen-insert` — 生成 INSERT SQL（离线模式）

```bash
# 6.1 PostgreSQL 方言（默认）
go run ./cmd/migrate/main.go gen-insert -d ./output/data/ -o ./output/insert/ --dialect postgres
echo "=== 验证 ==="
ls ./output/insert/
cat ./output/insert/scott.emp.insert.sql

# 6.2 Oracle 方言
go run ./cmd/migrate/main.go gen-insert -d ./output/data/ -o ./output/insert/oracle/ --dialect oracle
cat ./output/insert/oracle/scott.emp.insert.sql

# 6.3 MySQL 方言
go run ./cmd/migrate/main.go gen-insert -d ./output/data/ -o ./output/insert/mysql/ --dialect mysql
cat ./output/insert/mysql/scott.emp.insert.sql

# 6.4 TRUNCATE + no-quote
go run ./cmd/migrate/main.go gen-insert -d ./output/data/ -o ./output/insert/truncate/ --dialect postgres --truncate --no-quote-identifiers
cat ./output/insert/truncate/scott.emp.insert.sql

# 6.5 使用配置文件（精确类型映射）
go run ./cmd/migrate/main.go gen-insert -c ./migrate.yaml
cat ./output/insert/scott.emp.insert.sql
```

**预期（6.5 vs 6.1）：** 用配置文件时 NUMBER 列（如 SAL=800）不带引号，而 6.1 中所有值都被当作文本（800 带引号）。

---

## 第二部分：数据库命令测试（需要容器运行）

### 测试 7：`export` — 从 Oracle 导出数据到 CSV

```bash
# 先清理可能残留
rm -rf ./output/data/oracle/

# 执行导出
go run ./cmd/migrate/main.go export -c ./testdata/db/oracle_export.yaml
```

**验证：**
```bash
ls -la ./output/data/oracle/
wc -l ./output/data/oracle/*.csv
cat ./output/data/oracle/SCOTT.EMP.csv | head -5
```

**预期：** EMP.csv 应有 15 行（1 头 + 14 数据），DEPT.csv 应有 5 行（1 头 + 4 数据）。

---

### 测试 8：`export-metadata` — 导出元数据

```bash
# 8.1 导出为 CSV 格式
go run ./cmd/migrate/main.go export-metadata -c ./testdata/db/oracle_export.yaml -o ./output/metadata/csv/ --format csv
ls -la ./output/metadata/csv/

# 8.2 导出为 SQL 格式
go run ./cmd/migrate/main.go export-metadata -c ./testdata/db/oracle_export.yaml -o ./output/metadata/meta.sql --format sql
cat ./output/metadata/meta.sql | head -20

# 8.3 指定表范围导出
go run ./cmd/migrate/main.go export-metadata -c ./testdata/db/oracle_export.yaml -o ./output/metadata/emp_only/ --scope table:EMP
cat ./output/metadata/emp_only/columns.csv
```

---

### 测试 9：`import` — 导入数据到 PostgreSQL

```bash
# 步骤 1：先用 CSV 元数据在 PG 中创建表
go run ./cmd/migrate/main.go gen-ddl -c ./migrate.yaml -o ./output/ddl/
cat ./output/ddl/*.sql | docker exec -i postgres psql -U postgres -d postgres_db

# 步骤 2：导入 CSV 数据
go run ./cmd/migrate/main.go import -c ./testdata/db/oracle_to_pg_import.yaml
```

**验证：**
```bash
docker exec postgres psql -U postgres -d postgres_db -c "SELECT count(*) FROM \"EMP\";"
docker exec postgres psql -U postgres -d postgres_db -c "SELECT count(*) FROM \"DEPT\";"
docker exec postgres psql -U postgres -d postgres_db -c "SELECT * FROM \"EMP\" ORDER BY empno;"
```

**预期：** EMP 14 行，DEPT 4 行。

---

### 测试 10：`migrate` — 端到端一键迁移

#### 10.1 Oracle → PostgreSQL 完整迁移

```bash
go run ./cmd/migrate/main.go migrate -c ./testdata/db/oracle_to_pg.yaml
```

**验证：**
```bash
# 检查迁移报告
cat ./output/migration_report.json

# 验证 PG 数据
docker exec postgres psql -U postgres -d postgres_db -c "SELECT count(*) FROM \"EMP\";"
docker exec postgres psql -U postgres -d postgres_db -c "SELECT e.ename, d.dname FROM \"EMP\" e JOIN \"DEPT\" d ON e.deptno = d.deptno ORDER BY e.empno;"
```

#### 10.2 仅生成 SQL 文件（不写数据库）

```bash
go run ./cmd/migrate/main.go migrate -c ./testdata/db/oracle_to_pg.yaml --sql-out ./output/insert/migrate/
ls -la ./output/insert/migrate/
```

**验证：** 只生成 INSERT SQL 文件，PostgreSQL 中数据不变。

#### 10.3 断点续传

```bash
# 第一次执行（可能会中断）
go run ./cmd/migrate/main.go migrate -c ./testdata/db/oracle_to_pg.yaml

# 第二次带 --resume
go run ./cmd/migrate/main.go migrate -c ./testdata/db/oracle_to_pg.yaml --resume
```

**预期：** 第二次跳过已完成的表，只处理未完成的。

#### 10.4 跳过 DDL（仅数据迁移）

```bash
go run ./cmd/migrate/main.go migrate -c ./testdata/db/oracle_to_pg.yaml --skip-ddl
```

#### 10.5 出错继续

```bash
go run ./cmd/migrate/main.go migrate -c ./testdata/db/oracle_to_pg.yaml --continue-on-error
```

---

## 第三部分：组合场景测试

### 场景 A：离线 CSV → DDL → INSERT SQL → 导入 PG

完整的**离线工作流**，不需要源数据库：

```bash
# Step 1: 从 CSV 元数据生成 DDL
go run ./cmd/migrate/main.go gen-ddl -c ./migrate.yaml -o ./output/ddl/

# Step 2: 在 PG 中创建表
cat ./output/ddl/*.sql | docker exec -i postgres psql -U postgres -d postgres_db

# Step 3: 从 CSV 数据生成 INSERT SQL
go run ./cmd/migrate/main.go gen-insert -d ./output/data/ -o ./output/insert/ --dialect postgres --truncate

# Step 4: 执行 INSERT SQL 导入 PG
cat ./output/insert/*.insert.sql | docker exec -i postgres psql -U postgres -d postgres_db

# Step 5: 验证
docker exec postgres psql -U postgres -d postgres_db -c "SELECT count(*) FROM \"EMP\";"
docker exec postgres psql -U postgres -d postgres_db -c "SELECT * FROM \"EMP\" ORDER BY empno;"
```

### 场景 B：Oracle → PG 一步到位

```bash
go run ./cmd/migrate/main.go migrate -c ./testdata/db/oracle_to_pg.yaml
```

### 场景 C：审计模式（只生成 SQL 不写库）

```bash
go run ./cmd/migrate/main.go migrate -c ./testdata/db/oracle_to_pg.yaml --sql-out ./output/insert/audit/
ls -la ./output/insert/audit/
```

---

## 附录 A：快速验证脚本

将全部离线命令一键执行：

```bash
rm -rf ./output/
mkdir -p ./output/data/ ./output/insert/ ./output/select/ ./output/ddl/

# 准备数据文件
cat > ./output/data/SCOTT.EMP.csv << 'CSVEOF'
EMPNO,ENAME,JOB,MGR,HIREDATE,SAL,COMM,DEPTNO
7369,SMITH,CLERK,7902,1980-12-17,800,,20
7499,ALLEN,SALESMAN,7698,1981-02-20,1600,300,30
7521,WARD,SALESMAN,7698,1981-02-22,1250,500,30
7566,JONES,MANAGER,7839,1981-04-02,2975,,20
7654,MARTIN,SALESMAN,7698,1981-09-28,1250,1400,30
7698,BLAKE,MANAGER,7839,1981-05-01,2850,,30
7782,CLARK,MANAGER,7839,1981-06-09,2450,,10
7788,SCOTT,ANALYST,7566,1987-04-19,3000,,20
7839,KING,PRESIDENT,,1981-11-17,5000,,10
7844,TURNER,SALESMAN,7698,1981-09-08,1500,0,30
7876,ADAMS,CLERK,7788,1987-05-23,1100,,20
7900,JAMES,CLERK,7698,1981-12-03,950,,30
7902,FORD,ANALYST,7566,1981-12-03,3000,,20
7934,MILLER,CLERK,7782,1982-01-23,1300,,10
CSVEOF

cat > ./output/data/SCOTT.DEPT.csv << 'CSVEOF'
DEPTNO,DNAME,LOC
10,ACCOUNTING,NEW YORK
20,RESEARCH,DALLAS
30,SALES,CHICAGO
40,OPERATIONS,BOSTON
CSVEOF

cat > ./output/data/SCOTT.BONUS.csv << 'CSVEOF'
ENAME,JOB,SAL,COMM
SMITH,CLERK,800,
ALLEN,SALESMAN,1600,300
WARD,SALESMAN,1250,500
CSVEOF

# 执行各命令
echo "=== validate ==="
go run ./cmd/migrate/main.go validate -c ./migrate.yaml

echo "=== show-query ==="
go run ./cmd/migrate/main.go show-query postgres tables

echo "=== gen-ddl ==="
go run ./cmd/migrate/main.go gen-ddl -c ./migrate.yaml
ls ./output/ddl/

echo "=== gen-select ==="
go run ./cmd/migrate/main.go gen-select -c ./migrate.yaml
ls ./output/select/

echo "=== gen-insert ==="
go run ./cmd/migrate/main.go gen-insert -d ./output/data/ -o ./output/insert/ --dialect postgres
ls ./output/insert/
head -5 ./output/insert/scott.emp.insert.sql

echo "=== ALL DONE ==="
```

## 附录 B：排查指南

| 问题 | 原因 | 解决 |
|------|------|------|
| `connect: connection refused` | 容器未启动 | `docker start postgres mysql oracle` |
| `ORA-12154` | Oracle DSN 错误 | 检查 `oracle://scott:tiger@127.0.0.1:1521/XEPDB1` |
| `relation "EMP" does not exist` | PG 表名大小写 | 用 `\"EMP\"` 引号访问 |
| `mysql 无表` | 空数据库 | 先在 MySQL 中创建测试表 |
| `gen-insert` 列全部加引号 | 没加 `-c` 配置 | 用 `-c ./migrate.yaml` 加载精确类型 |
