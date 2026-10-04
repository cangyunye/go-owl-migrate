# owl-migrate 技能语料评测报告（baseline2）

- 日期: 2026-09-21  模型: `deepseek-flash`  参考文本: `context-ref.md`
- 语料: 65 条  判定: MATCH=45 PARTIAL=2 MISMATCH=16 MANUAL=2（人工复核）
- 自动通过率(非 MANUAL 中 MATCH 占比): 71%

| id | 判定 | 预期步骤 | 模型步骤 | 模型 notes |
|---|---|---|---|---|
| s1-01 | MATCH | owl-migrate init -s oracle --source-dsn oracle://scott:tiger@10.1.1.5:1521/XEPDB1 --source-schema SCOTT -t postgres --target-dsn 'host=10.1.1.6 port=5432 user=postgres password=pg123 dbname=app' -o migrate.yaml<br>owl-migrate migrate -c migrate.yaml --tables SCOTT.EMP,SCOTT.DEPT | owl-migrate init -s oracle --source-dsn "oracle://scott:tiger@10.1.1.5:1521/XEPDB1" --source-schema SCOTT -t postgres --target-dsn "host=10.1.1.6 port=5432 user=postgres password=pg123 dbname=app sslmode=disable" --target-schema SCOTT -S migrate -o migrate.yaml<br>owl-migrate migrate -c migrate.yaml --tables SCOTT.EMP,SCOTT.DEPT | 目标 schema 默认=SCOTT；若 PostgreSQL 无 scott schema，需先建或改用 --target-schema public。 |
| s1-02 | MISMATCH | make build/ob（或 build/full，基础二进制不含 oceanbase）<br>owl-migrate-ob init -s mysql --source-dsn 'user:pass@tcp(mysql-host:3306)/shop' -t oceanbase-mysql --target-dsn 'test@tenant:pwd@tcp(10.2.1.9:2881)/shop' -o migrate.yaml<br>owl-migrate-ob migrate -c migrate.yaml | （空） | 缺源 MySQL DSN（host/user/pass/db）；OceanBase 需 owl-migrate-ob（make build/ob）。补全后走 migrate 整库。 |
| s1-03 | MISMATCH | owl-migrate serve（未启动时）<br>POST /api/v1/migrate/preflight 只读检查<br>owl-migrate migrate -c migrate.yaml --tables SCOTT.EMP | （空） | 缺源 Oracle 密码/服务名与目标 PG DSN；预检为 POST /api/v1/migrate/preflight，需先启动 serve 并配置数据源。 |
| s1-04 | MATCH | （空） | （空） | 不支持表级重命名，仅 schema 级映射；可迁移后 RENAME，或目标预建 t2 再 import。 |
| s1-05 | MATCH | owl-migrate migrate -c migrate.yaml --resume | owl-migrate migrate -c <原迁移配置> --resume | 需沿用原配置与 temp-dir；状态文件默认 <temp-dir>/migrate_progress.json。 |
| s1-06 | MATCH | owl-migrate migrate -c migrate.yaml --sql-out ./output/insert/ | owl-migrate migrate -c <源库配置> --tables <表范围> --sql-out ./output/sql/ | 不连目标库，只产 INSERT SQL；请补源库 DSN/表范围，或复用原迁移配置。 |
| s1-07 | MATCH | owl-migrate online init --apply（可加 --require-key 强制有主键的表）<br>owl-migrate online sync | owl-migrate online init --tables <表范围> --require-key<br>owl-migrate online init --apply --tables <表范围> --require-key<br>owl-migrate online sync | CDC 触发器装源库；先全量迁移再 init/sync；需源/目标 DSN 配置。 |
| s1-08 | MATCH | owl-migrate migrate -c migrate.yaml --continue-on-error | owl-migrate migrate -c <配置> --continue-on-error<br>配置 import.batch.error_policy: skip_row（或 log_only），并设 import.batch.max_errors_before_stop | 脏表失败不中断；import 错误策略按容忍度设 skip_row/log_only 与上限。 |
| s1-09 | MATCH | 配置 online.sync.on_error: skip（或 retry），可配 error_table 收集错误行 | 配置 online.sync.on_error: skip（或 retry），并设置 online.sync.error_table<br>owl-migrate online sync | 增量回放遇错不停；skip 跳过，retry 重试，error_table 记录错误。 |
| s2-01 | MATCH | owl-migrate serve（未启动时，默认 127.0.0.1:8080）<br>POST /api/v1/datasources body {name:生产库, type:oracle, dsn:oracle://scott:tiger@10.1.1.5:1521/ORCL} | owl-migrate serve --host 127.0.0.1 --port 8080<br>curl -X POST http://127.0.0.1:8080/api/v1/datasources -H 'Content-Type: application/json' -d '{"name":"生产库","type":"oracle","schema":"SCOTT","dsn":"oracle://scott:tiger@10.1.1.5:1521/ORCL"}' | 数据源仅 Web API 管理；DSN AES 加密存 ~/.owl/migrate/datasources/。 |
| s2-02 | MATCH | GET /api/v1/datasources | owl-migrate serve --port 8080 --host 127.0.0.1<br>GET http://127.0.0.1:8080/api/v1/datasources | 数据源管理仅 Web API；serve 未启动需先启动（默认 127.0.0.1:8080 无需 token） |
| s2-03 | MATCH | PUT /api/v1/datasources/生产库 body {schema:HR} | owl-migrate serve --port 8080 --host 127.0.0.1<br>PUT http://127.0.0.1:8080/api/v1/datasources/生产库 body: {"schema":"HR"} | 仅 Web API 可管理数据源；如 PUT 要求完整字段，请带 name/type/dsn 等原值 |
| s2-04 | MATCH | DELETE /api/v1/datasources/ob-test | owl-migrate serve --port 8080 --host 127.0.0.1<br>DELETE http://127.0.0.1:8080/api/v1/datasources/ob-test | 数据源管理仅 serve API；确认删除后不可恢复 |
| s2-05 | MATCH | POST /api/v1/conn/test body {type:mysql, dsn:user:pwd@tcp(10.3.1.4:3306)/sales} | owl-migrate serve --port 8080 --host 127.0.0.1<br>POST http://127.0.0.1:8080/api/v1/conn/test body: {"type":"mysql","dsn":"user:pwd@tcp(10.3.1.4:3306)/sales"} | DSN 符合 mysql 格式；可选 connect_timeout，schema 可省略 |
| s2-06 | MATCH | 配置 source.dsn: 'datasource:生产库' | owl-migrate serve --port 8080 --host 127.0.0.1<br>POST http://127.0.0.1:8080/api/v1/datasources body: {name,type,schema,dsn,remark} 创建数据源<br>在 migrate.yaml 设置 source.dsn: "datasource:<数据源名>" | 支持；需 serve 端解析数据源引用，密码由 serve 加密存储，不明文落配置 |
| s3-01 | MISMATCH | owl-migrate export-metadata -c migrate.yaml -o ./output/metadata/ --format csv --scope all | （空） | 缺源库 DSN、schema、表范围、对象类型；请补充后再执行 export-metadata |
| s3-02 | MISMATCH | owl-migrate export-metadata -c migrate.yaml --scope schema:USERA | （空） | 需补充源库类型/DSN；确认 userA 为 Oracle schema；输出格式默认 csv |
| s3-03 | MISMATCH | owl-migrate export-metadata -c migrate.yaml --scope schema:SCOTT:table:EMP,DEPT | （空） | 需补充源库类型/DSN；范围可写 --scope table:SCOTT.EMP,SCOTT.DEPT |
| s3-04 | MISMATCH | owl-migrate export-metadata -c migrate.yaml -o ./schema.xlsx --format xlsx | （空） | 需明确源库类型/DSN、schema/表范围；命令加 --format xlsx |
| s3-05 | MISMATCH | owl-migrate export-metadata -c migrate.yaml --objects tables,views | （空） | 需补充源库类型/DSN、schema/表范围；对象参数为 --objects tables,views |
| s3-06 | MATCH | owl-migrate show-query oracle tables | owl-migrate show-query oracle tables | 只读展示元数据抽取 SQL，对象类型为 tables。 |
| s3-07 | MISMATCH | 编写最小配置：metadata.type: csv，csv.path 指向 CSV 目录<br>owl-migrate export ddl -c cfg.yaml | （空） | 需澄清：CSV 目录/文件路径、源方言、目标方言、表范围；参考未给 CSV 路径 flag，配置 metadata_type=csv 后跑 export ddl -c。 |
| s4-01 | MISMATCH | owl-migrate export ddl -c migrate.yaml -o ./output/ddl/ | （空） | 需澄清源库类型、DSN、schema 及表范围；目标已明确 PostgreSQL。补齐后用 init -t postgres -S export-ddl 生成配置，再 export ddl -c。 |
| s4-02 | MATCH | 配置 ddl.include_drop: true 后执行 export ddl | 在配置中设置 ddl.include_drop: true<br>owl-migrate export ddl -c <配置> | 仅影响 DDL 生成；需已有元数据/配置。 |
| s4-03 | MATCH | owl-migrate export ddl --no-quote-identifiers | 在配置中设置 ddl.no_quote_identifiers: true，或运行时加 --no-quote-identifiers<br>owl-migrate export ddl -c <配置> | 生成 CREATE 语句时不引用标识符。 |
| s4-04 | MATCH | 配置 ddl.schema_mapping: {SCOTT: public} 后执行 export ddl | 在配置中设置 ddl.schema_mapping: {SCOTT: public}<br>owl-migrate export ddl -c <配置> | 仅 schema 级映射，无表级重命名；目标表名不变。 |
| s5-01 | MISMATCH | owl-migrate export data -c migrate.yaml --tables USERA.TABLEA -o ./output/data/ | （空） | 需澄清源库类型、DSN/配置；表范围 userA.tableA 已明确。有配置后跑 export data -c <配置> --tables userA.tableA，默认 CSV 到 ./output/data/。 |
| s5-02 | MATCH | owl-migrate export data -c migrate.yaml --format sql | owl-migrate export data -c <配置> --tables userA.tableA --format sql -o <输出目录> | 输出 {schema}.{table}.insert.sql；沿用 s5-01 的源配置与表范围。 |
| s5-03 | MATCH | owl-migrate export data -c migrate.yaml --format xlsx | owl-migrate export data -c <配置> --tables userA.tableA --format xlsx -o <输出目录> | 输出 XLSX；沿用 s5-01 的源配置与表范围。 |
| s5-04 | MATCH | owl-migrate gen-select -c migrate.yaml --batch-method cursor -n 10000 | 在配置中将表范围设为 EMP（export.tables.include 包含 EMP）<br>owl-migrate gen-select -c <配置> -n 10000 --batch-method cursor -o ./output/select/ | 大表建议 cursor 分页；每批 10000 行。 |
| s5-05 | MATCH | owl-migrate export insert -d ./data/ --dialect postgres -o ./output/insert/ | owl-migrate export insert -d <CSV目录> --dialect postgres -o ./output/insert/ | 需将 <CSV目录> 替换为实际目录；零配置可用，默认每批100行、输出 ./output/insert/。 |
| s5-06 | MATCH | owl-migrate export insert -d ./data/ --dialect postgres -n 500 --truncate | owl-migrate export insert -d <CSV目录> --dialect postgres -n 500 --truncate -o ./output/insert/ | 在上一命令基础上加 -n 500 --truncate；<CSV目录> 沿用实际目录。 |
| s5-07 | MATCH | 配置 export.parallel: {enabled: true, max_workers: 4} 后执行 export data | 在 migrate.yaml 增加：export.parallel.enabled: true, export.parallel.max_workers: <并发数><br>owl-migrate export data -c migrate.yaml --tables <表范围> -o ./output/data/ | 仅 export data 支持并行；max_workers 按源库与机器调整，避免压垮源库。 |
| s6-01 | MATCH | owl-migrate import -c migrate.yaml | 确认配置 import.source_dir: ./output/data，并配置 import.target 与目标 DSN<br>owl-migrate import -c migrate.yaml | 缺失表会自动补建；目标类型/DSN 未给时需先补配置。 |
| s6-02 | MATCH | 配置 import.target.truncate_before: true 后执行 import | 在 migrate.yaml 设置 import.target.truncate_before: true<br>owl-migrate import -c migrate.yaml | truncate_before 在导入前清空已有目标表，再写入 CSV 数据。 |
| s6-03 | MATCH | 配置 import.data_transforms.source_encoding: GBK 后执行 import | 在 migrate.yaml 设置 import.data_transforms.source_encoding: GBK<br>owl-migrate import -c migrate.yaml | 如同时有日期等格式问题，可另配 datetime_format 或 fallback。 |
| s6-04 | MATCH | 配置 import.batch.error_policy: skip_row 后执行 import | 在 migrate.yaml 设置 import.batch.error_policy: skip_row<br>owl-migrate import -c migrate.yaml | skip_row 跳过坏行继续；可另设 max_errors_before_stop 控制阈值。 |
| s6-05 | MATCH | 配置 import.batch.use_copy: true 后执行 import | 在 migrate.yaml 设置 import.batch.use_copy: true<br>owl-migrate import -c migrate.yaml | 支持，use_copy 仅适用于 PostgreSQL/兼容目标，可显著提速。 |
| s7-01 | MATCH | owl-migrate serve（未启动时）<br>POST /api/v1/migrate/preflight | owl-migrate serve --host 127.0.0.1 --port 8080<br>POST /api/v1/migrate/preflight（只读，带源/目标/范围参数） | 若 serve 已启动可跳过第一步；预检返回 ok、checks、warnings。 |
| s7-02 | MATCH | owl-migrate validate -c migrate.yaml | owl-migrate validate -c <配置> | 配置需将 -m/--metadata-type 设为 csv 并指向 CSV 元数据目录/文件。 |
| s7-03 | MATCH | 查看 ./output/migration_report.json | cat ./output/migration_report.json | migrate -r 默认 ./output/migration_report.json；Web 可 GET /api/v1/jobs/{id} 查看。 |
| s7-04 | MATCH | owl-migrate serve --port 9090 | owl-migrate serve --port 9090 | 默认监听 127.0.0.1；绑定非环回地址时需加 --token。 |
| s7-05 | MATCH | owl-migrate serve --host 0.0.0.0 --token <强随机token> | owl-migrate serve --host 0.0.0.0 --port 8080 --token <token> | 同事浏览器访问 http://<本机IP>:8080；绑定非环回必须设 --token，放行防火墙端口。 |
| s8-01 | MISMATCH | owl-migrate init -s <源类型> --source-dsn <dsn> --source-schema <schema> -t <目标类型> --target-dsn <dsn> -o migrate.yaml | （空） | 缺少两个连接串内容；需提供源/目标类型、DSN、schema 及 init 场景。 |
| s8-02 | MISMATCH | owl-migrate init -s oracle --source-dsn <dsn> --source-schema USERA --scenario export-metadata -o meta.yaml | （空） | 需提供源库类型、DSN、schema；生成命令为 init --scenario export-metadata。 |
| s8-03 | MANUAL | （空） | export OWL_MIGRATE_CONFIG=~/migrate.yaml  # 或使用 -c ~/migrate.yaml | 顺序：-c > ./migrate.yaml > 环境变量 > ~/.owl/migrate/migrate.yaml；~/migrate.yaml 不自动读。 |
| s8-04 | MISMATCH | owl-migrate migrate -c migrate.yaml --tables 'SCOTT.T1,SCOTT.T2'（或配置 export.tables.include，支持 SCOTT.TABLE、SCOTT.*、*.T、T_* 等 glob）<br>排除没有反向语法：用白名单收敛，列出要迁的表而不是排除不要的 | （空） | 无排除语法；请提供白名单表名，用 --tables 列出要迁的表（log_ 开头不列入即可）。 |
| s9-01 | MISMATCH | make build/sqlite3（基础二进制不含 sqlite3）<br>owl-migrate-sqlite3 init -s sqlite3 --source-dsn /data/app.db -t mysql --target-dsn 'user:pass@tcp(host:3306)/db' -o migrate.yaml<br>owl-migrate-sqlite3 migrate -c migrate.yaml | （空） | 缺少目标 MySQL DSN/库名、表范围；SQLite3 需用 owl-migrate-sqlite3（make build/sqlite3）。 |
| amb-01 | MATCH | （空） | （空） | 缺表名、源库类型/DSN、导出格式（csv/sql/xlsx）及输出目录，需先澄清。 |
| neg-01 | MATCH | （空） | （空） | 不支持过程逻辑改写；functions/packages 仅导出元数据与原始定义。 |
| neg-02 | MATCH | （空） | （空） | 参考无数据比对能力（硬约束4），不支持库间数据差异对比。 |
| neg-03 | MISMATCH | （空） | （空） | Kingbase 不在支持方言列表，unsupported；支持 oracle/postgres/mysql 等。 |
| s1-10 | PARTIAL | owl-migrate online init（不加 --apply，只生成 online_cdc_NNN.sql 脚本）<br>人工审核并执行脚本<br>owl-migrate online sync --once | owl-migrate online init --tables <表名>（不加 --apply，仅生成 online_cdc_NNN.sql）<br>人工审核生成的 online_cdc_NNN.sql 脚本<br>owl-migrate online sync --once | 不加 --apply 不会在源库执行；审核后单轮 sync 回放增量。 |
| s1-11 | MATCH | owl-migrate migrate -c migrate.yaml --skip-ddl --temp-dir /data/tmp | owl-migrate migrate --skip-ddl --temp-dir /data/tmp -c <配置> | --skip-ddl 只导数据；--temp-dir 指定临时目录。 |
| s2-07 | MATCH | owl-migrate serve（未启动时）<br>POST /api/v1/datasources body {name:..., type:oceanbase-oracle, dsn:oceanbase-oracle://sys@tenant1#cluster3:obpass@10.2.1.9:2883/sales} | owl-migrate serve --port 8080（若未启动）<br>POST /api/v1/datasources，body: {"name":"<名称>","type":"oceanbase-oracle","schema":"sales","dsn":"oceanbase-oracle://sys@tenant1#cluster3:obpass@10.2.1.9:2883/sales"} | 数据源仅 Web API 管理；OBProxy DSN 中 #cluster3 紧跟租户。 |
| neg-04 | MATCH | （空） | （空） | CLI 无 datasource add；数据源管理仅 serve 的 REST API，配置文件用 datasource:名字 引用。 |
| s3-08 | MATCH | owl-migrate export-metadata -c migrate.yaml -o ./meta.sql --format sql | owl-migrate export-metadata --format sql -o <输出目录> | --format sql 仅 oracle 家族支持，用于元数据入库留存。 |
| s4-05 | MANUAL | （空） | （空） | export ddl 由元数据生成 CREATE TABLE + INDEX + VIEW，不只是建表语句。 |
| s6-06 | MATCH | 配置 import.target.disable_constraints: true<br>配置 import.batch.error_policy: log_only<br>owl-migrate import -c migrate.yaml | 配置 import.target.disable_constraints: true<br>配置 import.batch.error_policy: log_only<br>owl-migrate import --tables <表名>（如需要） | disable_constraints 禁约束，log_only 坏数据只记日志不中断。 |
| s6-07 | PARTIAL | 配置 import.data_transforms: {null_if: [""], datetime_format: yyyyMMddHHmmss, trim_strings: true}<br>owl-migrate import -c migrate.yaml | 配置 import.data_transforms.null_if: <空串对应值><br>配置 import.data_transforms.datetime_format: yyyyMMddHHmmss<br>配置 import.data_transforms.trim_strings: true | null_if 定义 CSV 空串转 NULL；datetime_format 支持紧凑模板。 |
| s7-06 | MATCH | owl-migrate serve（未启动时）<br>POST /api/v1/migrate 启动任务获得 job id<br>GET /api/v1/jobs/{id} 轮询进度（另有 /jobs/{id}/events 与 /jobs/{id}/ws） | owl-migrate serve --port 8080（绑定非环回地址时加 --token）<br>POST /api/v1/datasources 创建源/目标数据源（若尚未配置）<br>POST /api/v1/migrate 提交迁移任务（body 含源/目标数据源与表范围）<br>GET /api/v1/jobs/{id} 轮询状态；也可用 /jobs/{id}/events 或 /jobs/{id}/ws | 需提供源/目标数据源与表范围；非环回绑定必须设 token；DSN 加密存储。 |
| amb-02 | MATCH | （空） | （空） | 需澄清：源库类型+DSN、目标库类型+DSN、范围（schema/表）、是否停机及产出（迁移到库/导出）。 |
| amb-03 | MATCH | （空） | （空） | 需澄清：源库类型+DSN、范围、产出格式（CSV/INSERT SQL/XLSX）、数仓类型与是否需直接入库。 |
| amb-04 | MISMATCH | （空） | （空） | 需提供：迁移报告 migration_report.json、日志/终端输出、失败任务ID（若 serve）及场景信息。 |
| amb-05 | MATCH | （空） | （空） | 需澄清：源库类型+DSN、目标“新库”类型+DSN、表精确名/owner、是否只建表还是连数据迁移。 |
