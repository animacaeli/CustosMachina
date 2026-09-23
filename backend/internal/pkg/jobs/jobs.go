// Package jobs 极薄的后台任务框架（第二阶段 M2 新增，见 docs/plan-phase2-resources.md）。
// 每个任务一个 goroutine + Ticker，context 取消即退出；Group.Stop 等待当前轮次跑完。
// 单实例假设（单镜像部署），不做分布式锁；未来多实例时在任务内补 DB 乐观锁选主。
package jobs

import (
	"context"
	"sync"
	"time"

	"github.com/custos-machina/backend/internal/pkg/logger"
)

// Job 一个周期任务。Fn 应快速返回或自行监听 ctx 取消；报错只记日志不打断调度。
type Job struct {
	Name     string
	Interval time.Duration
	Fn       func(ctx context.Context) error
}

// Group 一组生命周期一致的后台任务。零值不可用，须通过 NewGroup 构造。
type Group struct {
	jobs    []Job
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	started bool
}

// NewGroup 构造任务组（不会自动启动，Start 幂等）。
func NewGroup(js ...Job) *Group {
	ctx, cancel := context.WithCancel(context.Background())
	return &Group{jobs: js, ctx: ctx, cancel: cancel}
}

// Add 在启动前追加任务；启动后追加无效（返回 false）。
func (g *Group) Add(j Job) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.started {
		return false
	}
	g.jobs = append(g.jobs, j)
	return true
}

// Start 启动全部任务（幂等）。
func (g *Group) Start() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.started {
		return
	}
	g.started = true
	for _, j := range g.jobs {
		g.wg.Add(1)
		go g.loop(j)
	}
}

func (g *Group) loop(j Job) {
	defer g.wg.Done()
	t := time.NewTicker(j.Interval)
	defer t.Stop()
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-t.C:
			if err := j.Fn(g.ctx); err != nil {
				logger.Warnf("[jobs:%s] %v", j.Name, err)
			}
		}
	}
}

// Stop 取消并等待当前轮次结束。flush 类任务可依赖 ctx 取消前的最后一次执行约定，
// 如需停机强制落盘，在 Fn 内监听 ctx.Done 自行处理。
func (g *Group) Stop() {
	g.mu.Lock()
	started := g.started
	g.mu.Unlock()
	if !started {
		return
	}
	g.cancel()
	g.wg.Wait()
}
