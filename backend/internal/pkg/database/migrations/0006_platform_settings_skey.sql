-- P8-M1 双方言硬化：platform_settings.key 是 MySQL 保留字（原生 SQL 全线语法错），
-- 列改名 skey 根治（API 的 json "key" 契约不变）。旧列名同为保留字，
-- RENAME 无法写双方言 SQL——实际改名由 Go 钩子（gorm Migrator 按方言转义）执行。
SELECT 1;
