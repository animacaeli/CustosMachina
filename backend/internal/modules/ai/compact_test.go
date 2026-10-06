// compact_test.go P7-M4：token 估算 / 自动压缩（超窗触发+失败降级）/ 手动 compact。
package ai

import (
	"strings"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	cjk := []Message{{Role: "user", Content: "一二三四五"}} // 5 CJK ≈ 5 token
	if got := estimateTokens(cjk); got != 5 {
		t.Errorf("纯中文 5 字应估 5，got %d", got)
	}
	mixed := []Message{{Role: "user", Content: "abcd"}} // ASCII 4×0.3≈1
	if got := estimateTokens(mixed); got < 1 || got > 2 {
		t.Errorf("ASCII 4 字符估值异常: %d", got)
	}
}

func TestTruncateMessages(t *testing.T) {
	msgs := []Message{{Role: "system", Content: "sys"}}
	for i := 0; i < 20; i++ {
		msgs = append(msgs, Message{Role: "user", Content: longText(500)})
	}
	out := truncateMessages(msgs, 2000)
	if len(out) >= len(msgs) {
		t.Fatalf("应发生截断: %d -> %d", len(msgs), len(out))
	}
	if out[0].Role != "system" {
		t.Fatal("system 应保留")
	}
	if estimateTokens(out) > 2000 {
		t.Fatalf("截断后应回到预算内: %d", estimateTokens(out))
	}
}

func longText(n int) string {
	out := make([]rune, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, '字')
	}
	return string(out)
}

func TestRebuildWithCompact(t *testing.T) {
	msgs := []Message{{Role: "system", Content: "sys"}}
	for i := 0; i < 12; i++ {
		msgs = append(msgs, Message{Role: "user", Content: "m"})
	}
	out := rebuildWithCompact(msgs, "前情：用户在排查 nginx")
	if len(out) != 1+compactKeepN {
		t.Fatalf("应保留 system + 最近 %d 条，got %d", compactKeepN, len(out))
	}
	sys, _ := out[0].Content.(string)
	if !strings.Contains(sys, "前情提要") || !strings.Contains(sys, "nginx") {
		t.Errorf("system 应含前情提要: %.80s", sys)
	}
}
