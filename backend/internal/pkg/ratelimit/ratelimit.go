// Package ratelimit 单实例内存限速（P5 M1 安全欠账）。
// 单实例假设（roadmap 原则 1）：内存态即可；平台转多实例时再落 Redis。
// 两类语义：
//   - Window：滑动窗口请求限速（防公开接口高频刷）；
//   - Lockout：失败计数锁定（防登录暴力试密码——成功即清零）。
package ratelimit

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
)

// Window 滑动窗口请求限速（按 key，通常为 IP）。
type Window struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	max    int
	window time.Duration
}

var (
	sweepMu    sync.Mutex
	sweepStops []func()
)

// registerSweeper 注册停止函数，StopAll 统一回收（进程关停时调用一次）。
func registerSweeper(stop func()) {
	sweepMu.Lock()
	sweepStops = append(sweepStops, stop)
	sweepMu.Unlock()
}

// StopAll 停止全部限速器清扫 goroutine（app cleanup 调用）。
func StopAll() {
	sweepMu.Lock()
	defer sweepMu.Unlock()
	for _, stop := range sweepStops {
		stop()
	}
	sweepStops = nil
}

func NewWindow(max int, window time.Duration) *Window {
	w := &Window{hits: map[string][]time.Time{}, max: max, window: window}
	registerSweeper(w.StartSweeper())
	return w
}

// sweepEvery 机会式全局清扫间隔：清理只在各自 key 访问时发生，长期不再
// 访问的 key 会驻留（独立审核 T6）——按固定频率整体扫一遍过期 key。
const sweepEvery = 10 * time.Minute

func (w *Window) sweep(now time.Time) {
	for k, hits := range w.hits {
		alive := false
		for _, t := range hits {
			if now.Sub(t) <= w.window {
				alive = true
				break
			}
		}
		if !alive {
			delete(w.hits, k)
		}
	}
}

// StartSweeper 启动清扫 goroutine（返回停止函数，随宿主 cleanup 调用）。
func (w *Window) StartSweeper() func() {
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(sweepEvery)
		defer t.Stop()
		for {
			select {
			case now := <-t.C:
				w.mu.Lock()
				w.sweep(now)
				w.mu.Unlock()
			case <-stop:
				return
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop) }) }
}

// Allow 记录一次命中并判定是否放行（false = 已超窗内上限）。
func (w *Window) Allow(key string) bool {
	now := time.Now()
	w.mu.Lock()
	defer w.mu.Unlock()
	hits := w.hits[key]
	keep := hits[:0]
	for _, t := range hits {
		if now.Sub(t) <= w.window {
			keep = append(keep, t)
		}
	}
	if len(keep) >= w.max {
		w.hits[key] = keep
		return false
	}
	w.hits[key] = append(keep, now)
	return true
}

// Lockout 失败计数锁定：窗口内连续失败达阈值后锁定一段时间，成功清零。
type Lockout struct {
	mu       sync.Mutex
	fails    map[string][]time.Time
	blocked  map[string]time.Time
	maxFails int
	window   time.Duration
	lockFor  time.Duration
}

func NewLockout(maxFails int, window, lockFor time.Duration) *Lockout {
	l := &Lockout{fails: map[string][]time.Time{}, blocked: map[string]time.Time{},
		maxFails: maxFails, window: window, lockFor: lockFor}
	registerSweeper(l.StartSweeper())
	return l
}

// Blocked 该 key 当前是否处于锁定期。
func (l *Lockout) Blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	until, ok := l.blocked[key]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(l.blocked, key)
		delete(l.fails, key)
		return false
	}
	return true
}

// ReportFail 记一次失败；达阈值即锁定并清计数。
func (l *Lockout) ReportFail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	fails := l.fails[key][:0]
	for _, t := range l.fails[key] {
		if now.Sub(t) <= l.window {
			fails = append(fails, t)
		}
	}
	fails = append(fails, now)
	l.fails[key] = fails
	if len(fails) >= l.maxFails {
		l.blocked[key] = now.Add(l.lockFor)
		delete(l.fails, key)
	}
}

// Reset 成功后清零失败计数。
func (l *Lockout) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}

// Gin 中间件形态：请求级滑动窗限速（超限 429）。
func (w *Window) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !w.Allow(c.ClientIP()) {
			httpx.Fail(c, http.StatusTooManyRequests, 429, "请求过于频繁，请稍后再试")
			c.Abort()
			return
		}
		c.Next()
	}
}

// Gin 中间件形态：失败锁定（Blocked 即 429；由业务在失败时调 ReportFail）。
// StartSweeper 同 Window：周期清理过期 fails/blocked（独立审核 T6）。
func (l *Lockout) StartSweeper() func() {
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(sweepEvery)
		defer t.Stop()
		for {
			select {
			case now := <-t.C:
				l.mu.Lock()
				for k, fails := range l.fails {
					alive := false
					for _, ts := range fails {
						if now.Sub(ts) <= l.window {
							alive = true
							break
						}
					}
					if !alive {
						delete(l.fails, k)
					}
				}
				for k, until := range l.blocked {
					if now.After(until) {
						delete(l.blocked, k)
					}
				}
				l.mu.Unlock()
			case <-stop:
				return
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop) }) }
}

func (l *Lockout) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if l.Blocked(c.ClientIP()) {
			httpx.Fail(c, http.StatusTooManyRequests, 429, "失败次数过多，账号已临时锁定，请稍后再试")
			c.Abort()
			return
		}
		c.Next()
	}
}
