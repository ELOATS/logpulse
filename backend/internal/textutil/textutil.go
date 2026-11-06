// Package textutil 提供与 PostgreSQL 文本类型兼容的字符串处理。
package textutil

import (
	"strings"
	"unicode/utf8"
)

// Truncate 把 s 截断到不超过 n 个字节，且不会切开多字节字符。
// （按字节直接切片可能产生非法 UTF-8，PostgreSQL 会拒绝写入整个批次。）
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Clean 把非法 UTF-8 替换为 U+FFFD 并去掉 NUL 字节：PostgreSQL 的 text / jsonb 不接受这两者，
// 一条这样的日志会导致整批写入失败。
func Clean(s string) string {
	if utf8.ValidString(s) && strings.IndexByte(s, 0) < 0 {
		return s
	}
	return strings.ReplaceAll(strings.ToValidUTF8(s, "�"), "\x00", "")
}

// CleanValue 递归清理 JSON 值（map / slice）中的所有字符串与键。
func CleanValue(v any) any {
	switch t := v.(type) {
	case string:
		return Clean(t)
	case map[string]any:
		for k, x := range t {
			ck := Clean(k)
			if ck != k {
				delete(t, k)
			}
			t[ck] = CleanValue(x)
		}
		return t
	case []any:
		for i, x := range t {
			t[i] = CleanValue(x)
		}
		return t
	}
	return v
}
