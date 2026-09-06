# Agent 通道验收套件

对每个可测数据库，用**同一份配置**以 `--channel` 切换 native / agent 跑同样的
命令，产物必须一致。详细决策规则见 [docs/agent-channel-testing-guide.md](../../docs/agent-channel-testing-guide.md)。

## 0. 准备

```bash
make build                                   # 基础二进制（native: mysql/pg/oracle）
make build/ob                                # OB 二进制（oceanbase-oracle native 需要）
bash owljdbc/jvm/owl-agent/build.sh          # sidecar jar
bash owljdbc/scripts/fetch-jars.sh           # 驱动 jar（仓库根已有则跳过）
bash testdata/acceptance/gen.sh              # 生成 gen/*.yaml（注入 .local-dev.env 的 DSN，gitignored）
```

## 1. 灌种子

| 库 | 命令 |
|---|---|
| MySQL | `mysql -h<host> -u<user> -p < testdata/acceptance/seed_mysql.sql`（先 `CREATE DATABASE owl_accept CHARACTER SET utf8mb4;`） |
| OB-MySQL | `obclient -u root@obmysql -p < testdata/acceptance/seed_mysql.sql`（先建库 owl_accept） |
| PostgreSQL | `psql '<OWL_E2E_PG_DSN>' -f testdata/acceptance/seed_pg.sql` |
| openGauss | `gsql -d postgres -U ogadmin -W *** -f testdata/acceptance/seed_pg.sql` |
| OB-Oracle | `obclient -u MIGSRC@oratest -p < testdata/acceptance/seed_ob_oracle.sql` |

## 2. 双通道对拍（每库两条命令 + diff）

```bash
# MySQL 示例（其余库把 -c 换成对应 gen/source.*.yaml；OB-Oracle native 用 make build/ob 产物）
owl-migrate export-metadata -c testdata/acceptance/gen/source.mysql.yaml --format csv -o /tmp/acc/meta_native
owl-migrate export-metadata -c testdata/acceptance/gen/source.mysql.yaml --channel agent --format csv -o /tmp/acc/meta_agent
diff -r /tmp/acc/meta_native /tmp/acc/meta_agent          # 期望：无差异

owl-migrate export data -c testdata/acceptance/gen/source.mysql.yaml -o /tmp/acc/data_native
owl-migrate export data -c testdata/acceptance/gen/source.mysql.yaml --channel agent -o /tmp/acc/data_agent
diff -r /tmp/acc/data_native /tmp/acc/data_agent          # 期望：无差异
```

openGauss 的 agent 通道 jar 走 `jdbcdrivers/openGauss-JDBC-6.0.6/`（模板已指好）；
native 通道需 `-tags og` 构建的二进制。

## 3. migrate 双通道（MySQL → PG / OB-Oracle）

```bash
owl-migrate migrate -c testdata/acceptance/gen/migrate.mysql2pg.yaml      --temp-dir /tmp/acc/tmp_n -r /tmp/acc/r_n.json
owl-migrate migrate -c testdata/acceptance/gen/migrate.mysql2pg.yaml      --channel agent --temp-dir /tmp/acc/tmp_a -r /tmp/acc/r_a.json
# 两侧目标表行数一致、内容一致（README 末尾抽查 SQL）。
```

`migrate.mysql2oboracle.yaml` 同理（native 侧需 ob tag 二进制；目标 MIGSRC 下的
owl_acc_dept/emp 由 migrate 自动建表，`truncate_before: true` 支持重复执行）。

## 4. auto 回退验证

```bash
# 用不带 -tags ob 的基础二进制 + --channel auto：
owl-migrate export-metadata -c gen/source.oboracle.yaml --channel auto --format csv -o /tmp/acc/auto
# 期望 stderr 出现：[channel] oceanbase-oracle: native driver not compiled into this binary, using owljdbc agent
# 且命令成功（走 agent）。
```

## 5. 故障注入

agent 通道跑长任务时 `kill -9 <java pid>` → 命令立即报 `agent process closed`，重跑自动重建 sidecar。

## 6. 清理

gen/*.yaml 含真实口令（已 gitignore）；目标库 owl_accept_tgt、migsrc 下的
owl_acc_* 表用完可删。
