// Package strx 跨模块共享的字符串工具（truncate / 部署名清洗等，
// 替代各模块的私有复制版本）。
package strx

import (
	"strings"
	"unicode/utf8"
)

// Truncate 截断到 n 字节（落库字段超长保护），截点回退到 rune 边界——
// 按裸字节切会切碎 UTF-8 中文出非法字符串（v0.12.0 审计：全仓 10 份私有
// 拷贝里只有 cron 一份是 rune 安全的，其余统一收敛到这里）。
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// NormalizeName 项目名 → 部署名段：小写 + 白名单（a-z0-9_.-）外的字符
// 替换为 "-"。与 release/canary/slots 的部署隔离域命名共用。
func NormalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	out := make([]rune, 0, len(name))
	for _, ch := range name {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9', ch == '_', ch == '.', ch == '-':
			out = append(out, ch)
		default:
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "project"
	}
	return string(out)
}
