// Package shellx 提供远端 shell 命令的参数引号化。
package shellx

import "strings"

// Quote 把任意字符串安全地包成 POSIX 单引号字面量（内嵌单引号转义为 '\”）。
// 拼进远端 shell 命令的用户输入必须经过它——注意 fmt 的 %q 产出的是 Go 字符串
// 字面量而非 shell 转义：双引号内 $、反引号、$(...) 照常展开，不能当作防注入手段。
func Quote(v string) string { return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'" }
