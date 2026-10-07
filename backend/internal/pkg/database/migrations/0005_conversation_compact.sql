-- P7-M4 上下文管理：会话压缩摘要（前情提要 + 压缩分界）。
-- 原 ALTER 语句改 Go 钩子执行：跳级升级（≤v0.9.x 老库无 ai_conversations
-- 表）裸 ALTER 报 no such table——2026-10-07 生产事故根因，v0.12.6 修复。
SELECT 1;
