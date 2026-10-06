package strx

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// v0.12.1 复核：共享工具此前 0% 覆盖——Truncate 的 rune 边界回退是
// 多个模块委托进来的安全属性，必须有能失败的检查。
func TestTruncate(t *testing.T) {
	if got := Truncate("short", 10); got != "short" {
		t.Errorf("短串应原样返回, got %q", got)
	}
	if got := Truncate("abcdefghij", 10); got != "abcdefghij" {
		t.Errorf("等长应原样返回, got %q", got)
	}
	if got := Truncate("abcdefghijk", 10); got != "abcdefghij" {
		t.Errorf("ASCII 截断不符, got %q", got)
	}
	// 中文：3 字节/字符——截点必须回退到 rune 边界
	s := strings.Repeat("中", 10) // 30 字节
	got := Truncate(s, 10)
	if !utf8.ValidString(got) {
		t.Errorf("截断产物不是合法 UTF-8: %q", got)
	}
	if got != strings.Repeat("中", 3) {
		t.Errorf("10 字节应保留 3 个中文, got %q", got)
	}
	// 多字节中间截点回退
	got = Truncate("a中中中", 5) // 'a'+中(3)=4, 第二个中截在中间 → 回退到 4
	if got != "a中" || !utf8.ValidString(got) {
		t.Errorf("截点在多字节中间应回退 rune 起点, got %q", got)
	}
}

func TestNormalizeName(t *testing.T) {
	cases := map[string]string{
		"My App":     "my-app",
		"API网关":      "api--",
		"  X.Y_9  ":  "x.y_9",
		"":           "project",
		"UPPER-Case": "upper-case",
	}
	for in, want := range cases {
		if got := NormalizeName(in); got != want {
			t.Errorf("NormalizeName(%q)=%q want %q", in, got, want)
		}
	}
}
