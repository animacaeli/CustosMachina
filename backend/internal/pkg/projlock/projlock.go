// Package projlock 项目级互斥锁（第四阶段审核修复 #42）：
// 蓝绿发布（release）与灰度发布（canary）会先后写同一份 nginx conf，
// 单写者只收敛了渲染，写入时序也须互斥。进程内 map（平台单实例假设，
// 与 pkg/jobs 一致）；跨模块共享放独立包避免 release↔canary 循环依赖。
package projlock

import "sync"

var (
	mu    sync.Mutex
	locks = map[uint]*sync.Mutex{}
)

// Lock 取项目互斥锁并加锁；返回的 unlock 用于释放。
func Lock(projectID uint) func() {
	mu.Lock()
	l, ok := locks[projectID]
	if !ok {
		l = &sync.Mutex{}
		locks[projectID] = l
	}
	mu.Unlock()
	l.Lock()
	return l.Unlock
}
