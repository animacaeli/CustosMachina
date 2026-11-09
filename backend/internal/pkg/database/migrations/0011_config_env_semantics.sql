-- 0011 配置文件环境语义返工（2026-11-09 用户定档）：
-- rel_path 首段 prod/canary/test → prod/devN（槽位）；canary 目录全面引用
-- prod 不再独立存在；文件级 Sensitive 开关废除（脱敏改字段级自动识别）。
-- 数据重写涉及逐行条件判断与跨行对比，SQLite/MySQL 双方言写不了纯 SQL，
-- 全部逻辑在 app 层 Go 钩子（见 internal/app/app.go RegisterGoHook("0011")）。
-- 本文件仅作版本链占位，无 DDL。
SELECT 1;
