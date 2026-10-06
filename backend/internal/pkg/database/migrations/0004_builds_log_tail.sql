-- P7-M2：builds 表加 log_tail（Poller 终态存 consoleText 尾部，AI 分析兜底）。
ALTER TABLE builds ADD COLUMN log_tail TEXT;
