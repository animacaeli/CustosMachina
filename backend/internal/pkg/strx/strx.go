// Package strx 跨模块共享的字符串工具（truncate / 部署名清洗等，
// 替代各模块的私有复制版本）。
package strx

import "strings"

// Truncate 按字节截断（落库字段超长保护）。
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
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
