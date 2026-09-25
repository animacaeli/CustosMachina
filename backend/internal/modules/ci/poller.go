package ci

import (
	"time"

	"github.com/custos-machina/backend/internal/pkg/jobs"
)

// pollInterval 构建状态轮询间隔（commit status 由 act_runner 写入，30s 足够灵敏）。
const pollInterval = 30 * time.Second

// Poller 占位主类型：wire 对"返回值仅 cleanup"的 provider 不会生成调用
// （ci:poll 曾因此从未启动），哨兵类型让 app 组装层显式依赖以拉起任务。
type Poller struct{}

// NewPoller 常驻轮询未终态构建；cleanup 由 wire 聚合为停机钩子。
func NewPoller(svc *Service) (*Poller, func(), error) {
	g := jobs.NewGroup(
		jobs.Job{Name: "ci:poll", Interval: pollInterval, Fn: svc.PollPending},
	)
	g.Start()
	return &Poller{}, g.Stop, nil
}
