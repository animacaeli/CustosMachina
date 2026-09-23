// Package resources 资源管理（第二阶段 M1）：服务器与服务器分组。
// SSH 凭据（密码/私钥）以 AES-256-GCM 加密落库（复用 pkg/crypto），
// 任何 API 不回传明文或密文，只回传 has_credential 布尔。
// 采集架构为 agentless：全部能力（指标、Docker、终端）复用同一 SSH 凭据（见 docs/plan-phase2-resources.md）。
package resources
