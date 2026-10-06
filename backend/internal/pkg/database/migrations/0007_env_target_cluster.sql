-- P8-M3.2：k3s 部署目标关联集群（runtime=k3s 时必填，FK k3s_clusters）。
ALTER TABLE project_env_targets ADD COLUMN cluster_id INTEGER NOT NULL DEFAULT 0;
