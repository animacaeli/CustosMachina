-- P7-M2：builds 表加 log_tail（Poller 终态存 consoleText 尾部，AI 分析兜底）。
-- 原 ALTER 语句改 Go 钩子执行：跳级升级时 AutoMigrate 先行会补上该列，
-- 裸 ALTER 将报 duplicate column（v0.12.6 生产事故修复）。
SELECT 1;
