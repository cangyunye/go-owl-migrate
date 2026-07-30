1. 使用web-testi界面功能
2. ✅ 数据迁移的用例确认完整，后端测试已补齐（2026-07-30）
   - migrate 全链路 e2e（Oracle→PG、MySQL→PG、PG→MySQL、PG→Oracle、MySQL→Oracle、--sql-out）
   - export data 在线导出真实库（Oracle/MySQL/PG 行数、NULL、datetime、分页）
   - export-metadata 真实库（csv/sql/xlsx 三格式 + scope 过滤）
   - extractor 真实库抽取（Oracle/MySQL/PG — tables/columns/pk/indexes/fk/views/sequences）
   - migrate state/report/resume 纯逻辑单测
   - 修复 buildCreateTableSQL 方言判定 bug（丢失 oracle/mysql 基础方言识别）
