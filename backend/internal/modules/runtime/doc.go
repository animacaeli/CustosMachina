// Package runtime 运行时适配层（FR5，批次 2 实现）。
//
// 核心抽象 RuntimeAdapter（固定操作集）：
//
//	ListServices / GetStatus / Logs / Events / Deploy / Rollback / Restart / Scale
//
// 实现：compose（gitops-lite：读 Gitea compose 仓库 + SSH 执行器）→ swarm → k3s。
// 数据模型 runtime_targets / services 自第一天支持多目标、多形态并存（D11）。
// 部署后写入 timeline 模块的事件流。
package runtime
