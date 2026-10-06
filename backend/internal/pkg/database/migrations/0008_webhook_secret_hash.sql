-- v0.12.0 审计修复：gitee webhook 密码改哈希存储（sha256 hex）。
-- 哈希化由 Go 钩子执行（SQL 无可移植的 sha256；钩子幂等：非 64-hex 才改写）。
SELECT 1;
