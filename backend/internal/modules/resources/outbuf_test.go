package resources

import (
	"strings"
	"sync"
	"testing"
)

// 并发写回归（v0.12.0 审计严重项 5）：x/crypto/ssh 的 session 用两个独立
// goroutine 分别拷贝 stdout/stderr，同一 writer 的 Write 必然并发。
// 跑法：go test -race ./internal/modules/resources/ -run TestOutBufConcurrent
func TestOutBufConcurrent(t *testing.T) {
	o := &outBuf{}
	var writers sync.WaitGroup
	for w := 0; w < 8; w++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for i := 0; i < 500; i++ {
				_, _ = o.Write([]byte("stdout|stderr chunk "))
			}
		}()
	}
	// 读侧并发（超时路径 SIGKILL 后不等 copy goroutine 就读 String()）
	stop := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
				_ = o.String()
			}
		}
	}()
	writers.Wait()
	close(stop)
	<-readerDone
	if n := len(o.String()); n != 8*500*len("stdout|stderr chunk ") {
		t.Errorf("并发写丢失数据: got %d bytes", n)
	}
}

// streamBuf 回调在锁外触发但缓冲写入必须串行——回调自身的并发由调用方
// （cron onChunk）持锁串行化。
func TestStreamBufConcurrent(t *testing.T) {
	var cbCount int
	var cbMu sync.Mutex
	o := &streamBuf{onChunk: func(string) {
		cbMu.Lock()
		cbCount++
		cbMu.Unlock()
	}}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				_, _ = o.Write([]byte("x"))
			}
		}()
	}
	wg.Wait()
	if cbCount != 4*300 {
		t.Errorf("回调次数不符: %d", cbCount)
	}
	if !strings.Contains(o.String(), "x") || len(o.String()) != 4*300 {
		t.Errorf("缓冲内容不符: %d bytes", len(o.String()))
	}
}
