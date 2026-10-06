package auth

import (
	"context"
	"testing"
	"time"
)

// P8-M1：未消费的过期 token 应在后续 Save 时被清扫，不随时间累积。
func TestMemoryRefreshStore_SweepOnSave(t *testing.T) {
	m := newMemoryRefreshStore()
	ctx := context.Background()
	if err := m.Save(ctx, "old", 1, -time.Minute); err != nil { // 已过期
		t.Fatal(err)
	}
	if err := m.Save(ctx, "live", 2, time.Minute); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	n := len(m.tokens)
	_, hasOld := m.tokens["old"]
	m.mu.Unlock()
	if hasOld {
		t.Error("过期 token 应在 Save 时被清扫")
	}
	if n != 1 {
		t.Errorf("清扫后应只剩 1 项，got %d", n)
	}
	if _, ok, _ := m.Consume(ctx, "old"); ok {
		t.Error("过期 token 不应可消费")
	}
	if _, ok, _ := m.Consume(ctx, "live"); !ok {
		t.Error("有效 token 应可消费")
	}
}
