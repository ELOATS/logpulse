// Package pattern 将日志消息归一化为模板，实现日志聚类。
// 思路与 Drain 等日志模板挖掘算法一致：把消息中的可变部分（数字、IP、
// UUID、十六进制 ID、时间、路径参数等）替换为占位符，相同模板即为同一类日志。
package pattern

import (
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"strings"
)

type rule struct {
	re   *regexp.Regexp
	repl string
}

// 规则顺序很重要：先匹配更具体的模式。
var rules = []rule{
	{regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?`), "<TIME>"},
	{regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`), "<UUID>"},
	{regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.-]+`), "<EMAIL>"},
	{regexp.MustCompile(`https?://[^\s"']+`), "<URL>"},
	{regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d+)?\b`), "<IP>"},
	{regexp.MustCompile(`(?i)\b0x[0-9a-f]+\b`), "<HEX>"},
	{regexp.MustCompile(`(?i)\b[0-9a-f]*\d[0-9a-f]*[a-f][0-9a-f]*\b|\b[0-9a-f]*[a-f][0-9a-f]*\d[0-9a-f]*\b`), "<HEX?>"}, // 长度在 replaceHex 中过滤
	{regexp.MustCompile(`(/[\w.-]+)*/\d+(/[\w.-]+)*`), "<PATH>"},
	{regexp.MustCompile(`"[^"]{1,200}"`), `"<STR>"`},
	{regexp.MustCompile(`'[^']{1,200}'`), `'<STR>'`},
	{regexp.MustCompile(`-?\b\d+(?:\.\d+)?(?:ms|s|us|µs|ns|m|h|kb|mb|gb|b|%)?\b`), "<NUM>"},
}

var spaceRe = regexp.MustCompile(`\s+`)

// protectedRe 匹配有语义、不应被替换为占位符的片段（协议版本、HTTP 状态码）。
var protectedRe = regexp.MustCompile(`HTTP/\d(?:\.\d)?|\bstatus[=:]\s?\d{3}\b`)

// Template 返回消息的归一化模板。
func Template(msg string) string {
	t := msg
	if len(t) > 2000 {
		t = t[:2000]
	}
	// 先用不含数字的哨兵替换受保护片段，规则处理完后再还原
	var kept []string
	t = protectedRe.ReplaceAllStringFunc(t, func(m string) string {
		kept = append(kept, m)
		return "\x01" + string(rune('A'+len(kept)-1)) + "\x01"
	})
	for _, r := range rules {
		if r.repl == "<HEX?>" {
			t = r.re.ReplaceAllStringFunc(t, func(s string) string {
				if len(s) >= 8 {
					return "<HEX>"
				}
				return s
			})
			continue
		}
		if r.repl == "<PATH>" {
			t = r.re.ReplaceAllStringFunc(t, normalizePath)
			continue
		}
		t = r.re.ReplaceAllString(t, r.repl)
	}
	for i, k := range kept {
		t = strings.Replace(t, "\x01"+string(rune('A'+i))+"\x01", k, 1)
	}
	t = spaceRe.ReplaceAllString(strings.TrimSpace(t), " ")
	return collapse(t)
}

var digitSeg = regexp.MustCompile(`^\d+$`)

// normalizePath 只替换路径中纯数字的片段，如 /users/123/orders -> /users/<NUM>/orders
func normalizePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if digitSeg.MatchString(s) {
			segs[i] = "<NUM>"
		}
	}
	return strings.Join(segs, "/")
}

// collapse 合并连续重复的占位符，例如 "<NUM> <NUM> <NUM>" -> "<NUM>+"
func collapse(t string) string {
	toks := strings.Split(t, " ")
	out := toks[:0]
	for i, tok := range toks {
		if i > 0 && isPlaceholder(tok) {
			prev := strings.TrimSuffix(out[len(out)-1], "+")
			if prev == tok {
				if !strings.HasSuffix(out[len(out)-1], "+") {
					out[len(out)-1] += "+"
				}
				continue
			}
		}
		out = append(out, tok)
	}
	return strings.Join(out, " ")
}

func isPlaceholder(tok string) bool {
	return strings.HasPrefix(tok, "<") && strings.HasSuffix(tok, ">")
}

// Hash 计算 (service, level, template) 的稳定哈希，作为模式唯一键。
func Hash(service, level, template string) string {
	h := sha1.Sum([]byte(service + "\x00" + level + "\x00" + template))
	return hex.EncodeToString(h[:])
}
