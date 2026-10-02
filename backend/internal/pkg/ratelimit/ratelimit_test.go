package ratelimit

import (
	"testing"
	"time"
)

func TestWindowAllow(t *testing.T) {
	w := NewWindow(3, 50*time.Millisecond)
	for i := 0; i < 3; i++ {
		if !w.Allow("ip") {
			t.Fatalf("第 %d 次应放行", i+1)
		}
	}
	if w.Allow("ip") {
		t.Error("超限后应拒绝")
	}
	if !w.Allow("other") {
		t.Error("不同 key 互不影响")
	}
	time.Sleep(60 * time.Millisecond)
	if !w.Allow("ip") {
		t.Error("窗口滑出后应恢复放行")
	}
}

func TestLockout(t *testing.T) {
	l := NewLockout(3, time.Minute, 20*time.Millisecond)
	for i := 0; i < 3; i++ {
		l.ReportFail("ip")
	}
	if !l.Blocked("ip") {
		t.Fatal("达阈值应锁定")
	}
	if !l.Blocked("ip") {
		t.Fatal("锁定期内应持续拦截")
	}
	time.Sleep(25 * time.Millisecond)
	if l.Blocked("ip") {
		t.Error("锁定期过后应解锁")
	}
	// 成功清零
	l.ReportFail("ip")
	l.ReportFail("ip")
	l.Reset("ip")
	l.ReportFail("ip")
	if l.Blocked("ip") {
		t.Error("成功清零后不应立即锁定")
	}
}
