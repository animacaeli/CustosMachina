// Package alerting 告警事件管道（FR7，批次 4 实现）：
// OpenObserve webhook 接收 → 入库 / dedup_key 去重 / 静默窗口 → 交给 ai 模块诊断。
package alerting
