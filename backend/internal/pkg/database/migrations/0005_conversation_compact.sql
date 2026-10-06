-- P7-M4 上下文管理：会话压缩摘要（前情提要 + 压缩分界）。
ALTER TABLE ai_conversations ADD COLUMN compact_text TEXT;
ALTER TABLE ai_conversations ADD COLUMN compact_after_id INTEGER NOT NULL DEFAULT 0;
