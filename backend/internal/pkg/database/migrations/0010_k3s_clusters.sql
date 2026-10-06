-- v0.12.0 k3s 集群管理表。同 0009 根因：0007 只给 project_env_targets 加了
-- cluster_id 列，k3s_clusters 表本体无迁移——存量库升级后集群页整块不可用，
-- 且 k3s 部署目标保存因缺表报 no such table。建表由 Go 钩子执行。
SELECT 1;
