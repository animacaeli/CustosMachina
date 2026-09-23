package ci

import (
	"time"

	"github.com/custos-machina/backend/internal/pkg/jobs"
)

// pollInterval 构建状态轮询间隔（commit status 由 act_runner 写入，30s 足够灵敏）。
const pollInterval = 30 * time.Second

// NewPoller 常驻轮询未终态构建；wire 聚合返回的 cleanup 在停机时调用 Stop。
func NewPoller(svc *Service) (func(), error) {
	g := jobs.NewGroup(
		jobs.Job{Name: "ci:poll", Interval: pollInterval, Fn: svc.PollPending},
	)
	g.Start()
	return g.Stop, nil
}
