# 0001 — 会话历史检索：关键词列 + 分词 SQL 匹配（无向量、无进程外工具）

日期：2026-10-05  状态：已接受

## 背景

AI 会话历史需要"找到那个最终跑成了的会话"。用户期望可用 grep/rg 检索，但会话存在
单个 SQLite 二进制文件里（internal/ai，modernc 纯 Go 驱动以支持 CGO_ENABLED=0），
文本工具无法可靠检索。

## 决策

- ai_sessions 表增加 title/keywords 列；keywords 在首轮计划成功时从 SlotRequest
  **确定性**拆出（scenario/方言/档案/schema/格式/filters 键），不经 LLM。
- ListSessions(q) 分词 AND、大小写不敏感 LIKE，匹配域 = title+keywords+intent+sub。
- 检索对象默认排除 discarded 会话；effective 会话（任务启动即永久标记）是核心目标。

## 备选与理由

- rg/grep：需要每会话导出文本文件并维护双写一致性——为检索引入第二存储不值。
- SQLite FTS5：能力更强但 modernc 驱动的 FTS 支持与迁移成本高；当前关键词是
  结构化槽位（天然分好词），LIKE 足够且零新依赖。
- 向量检索：无嵌入基础设施，且关键词是确定性数据，语义检索是伪需求。

## 后果

+ 零新依赖、零第二存储；检索词可解释（历史列表直接显示 keywords）。
- 只能字面匹配（无同义词）；keywords 覆盖面由槽位字段决定，表级检索需等
  SlotRequest 增加 tables 字段。
