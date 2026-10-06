-- v0.12.1 告警事件留痕表。存量库增量路径不跑 AutoMigrate（migrate.go 只认
-- SQL 文件+Go 钩子），此前只注册进 Models() 导致升级安装上该表永不建出、
-- 告警历史页 500（v0.12.2 复核 N7）。自增主键 CREATE TABLE 写不了
-- SQLite/MySQL 双方言 DDL，建表由 Go 钩子执行（app.go 注册）。
SELECT 1;
