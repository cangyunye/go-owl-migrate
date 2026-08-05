1. 使用web-testi界面功能
2. ✅ 数据迁移的用例确认完整，后端测试已补齐（2026-07-30）
   - migrate 全链路 e2e（Oracle→PG、MySQL→PG、PG→MySQL、PG→Oracle、MySQL→Oracle、--sql-out）
   - export data 在线导出真实库（Oracle/MySQL/PG 行数、NULL、datetime、分页）
   - export-metadata 真实库（csv/sql/xlsx 三格式 + scope 过滤）
   - extractor 真实库抽取（Oracle/MySQL/PG — tables/columns/pk/indexes/fk/views/sequences）
   - migrate state/report/resume 纯逻辑单测
   - 修复 buildCreateTableSQL 方言判定 bug（丢失 oracle/mysql 基础方言识别）
3. ✅ GoNavi 经验落地：驱动层与迁移管线五阶段改造（2026-08-05）
   - ① P0：无主键表导出死循环→OFFSET 分页；gen-select 复合主键行值比较+方言分页；复合方言（goldendb/oceanbase-mysql）引号修正；目标建表接线 dialect 体系（LogicalType 跨方言转换、type_overrides 优先、Oracle 存在性检查修复）
   - ② Oracle：统一 internal/dbconn；DSN 注入 PREFETCH_ROWS=25 + LOB FETCH=POST；11g 自动回退 ROWNUM 分页
   - ③ OceanBase：compat_mode 配置+连接探测+误配报错；新增 helingjun/obconnector-go，Oracle 租户支持 MySQL 线协议直连（oboracle）与 TNS 双路径；oceanbase-oracle-wire 提取器与占位符覆盖
   - ④ 导入引擎：多行 VALUES（PG $N 跨行连续编号）、占位符超限二分重试、savepoint 逐行抢救（修复 skip_row 丢弃已插入行）、Oracle 预编译复用、PG COPY 快速通道（use_copy）
   - ⑤ 抽取补全：Oracle/PG 表注释、分区 PARTITION BY 重建、identity 起始/步长、序列 START WITH=last_number、函数/物化视图/包在线抽取、MySQL 触发器语言修正、PG search_path 入 DSN
   - 验证：单测 24 包 + e2e 24 包全绿（本地 MySQL/PostgreSQL 15/Oracle 23ai 实测）
