// Package pattern 将日志消息归一化为模板，实现日志聚类。
// 思路与 Drain 等日志模板挖掘算法一致：把消息中的可变部分（数字、IP、
// UUID、十六进制 ID、时间、路径参数等）替换为占位符，相同模板即为同一类日志。
package pattern

import (
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/eloats/logpulse/backend/internal/textutil"
)

// rule 把匹配 re 的片段替换为 repl；fn 非空时改用 fn 计算替换结果。
// gate 是廉价的预检：文本中不可能出现匹配时（缺少必需的字符）直接跳过该规则。
// 模板化在写入路径上对每条日志执行，绝大多数规则对一条消息都不会命中，
// 预检可省掉大部分正则扫描。
type rule struct {
	re   *regexp.Regexp
	repl string
	fn   func(string) string
	gate func(string) bool
}

func has(chars string) func(string) bool {
	return func(s string) bool { return strings.ContainsAny(s, chars) }
}

func hasSub(subs ...string) func(string) bool {
	return func(s string) bool {
		for _, sub := range subs {
			if strings.Contains(s, sub) {
				return true
			}
		}
		return false
	}
}

const digits = "0123456789"

// 规则顺序很重要：先匹配更具体的模式。
var rules = []rule{
	{re: regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?`), repl: "<TIME>", gate: has(":")},
	{re: regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`), repl: "<UUID>", gate: has("-")},
	{re: regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.-]+`), repl: "<EMAIL>", gate: has("@")},
	{re: regexp.MustCompile(`https?://[^\s"']+`), repl: "<URL>", gate: hasSub("://")},
	{re: regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d+)?\b`), repl: "<IP>", gate: has(".")},
	{re: regexp.MustCompile(`(?i)\b0x[0-9a-f]+\b`), repl: "<HEX>", gate: hasSub("0x", "0X")},
	// 同时含数字与 a-f 的十六进制串，至少 8 位才视为 ID（短的如 "b2" 保留）
	{re: regexp.MustCompile(`(?i)\b[0-9a-f]*\d[0-9a-f]*[a-f][0-9a-f]*\b|\b[0-9a-f]*[a-f][0-9a-f]*\d[0-9a-f]*\b`), fn: longHex, gate: has(digits)},
	{re: regexp.MustCompile(`(/[\w.-]+)*/\d+(/[\w.-]+)*`), fn: normalizePath, gate: has("/")},
	{re: regexp.MustCompile(`"[^"]{1,200}"`), repl: `"<STR>"`, gate: has(`"`)},
	{re: regexp.MustCompile(`'[^']{1,200}'`), fn: keepIdentifier, gate: has("'")},
	{re: regexp.MustCompile(`-?\b\d+(?:\.\d+)?(?:ms|s|us|µs|ns|m|h|kb|mb|gb|b|%)?\b`), repl: "<NUM>", gate: has(digits)},
}

// protectedRe 匹配有语义、不应被替换为占位符的片段（协议版本、HTTP 状态码）。
var protectedRe = regexp.MustCompile(`HTTP/\d(?:\.\d)?|\bstatus[=:]\s?\d{3}\b`)

// Template 返回消息的归一化模板。
func Template(msg string) string {
	t := textutil.Truncate(msg, 2000)
	// 先用不含数字的哨兵替换受保护片段，规则处理完后再还原
	var kept []string
	if strings.Contains(t, "HTTP/") || strings.Contains(t, "status") {
		t = protectedRe.ReplaceAllStringFunc(t, func(m string) string {
			kept = append(kept, m)
			return "\x01" + string(rune('A'+len(kept)-1)) + "\x01"
		})
	}
	for _, r := range rules {
		if !r.gate(t) {
			continue
		}
		if r.fn != nil {
			t = r.re.ReplaceAllStringFunc(t, r.fn)
		} else {
			t = r.re.ReplaceAllString(t, r.repl)
		}
	}
	for i, k := range kept {
		t = strings.Replace(t, "\x01"+string(rune('A'+i))+"\x01", k, 1)
	}
	return collapse(collapseSpaces(strings.TrimSpace(t)))
}

func longHex(s string) string {
	if len(s) >= 8 {
		return "<HEX>"
	}
	return s
}

// collapseSpaces 把连续空白（与正则 \s 相同：空格、\t、\n、\f、\r）合并为一个空格。
func collapseSpaces(s string) string {
	isSpace := func(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r' }
	clean := true
	for i := 0; i < len(s); i++ {
		if isSpace(s[i]) && (s[i] != ' ' || (i+1 < len(s) && isSpace(s[i+1]))) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if isSpace(s[i]) {
			b.WriteByte(' ')
			for i+1 < len(s) && isSpace(s[i+1]) {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

var digitSeg = regexp.MustCompile(`^\d+$`)

var identRe = regexp.MustCompile(`^'[A-Za-z_$][A-Za-z_$.-]{0,40}'$`)

// keepIdentifier 保留单引号中的短标识符（如 JS 的 reading 'map'），它们通常是错误的关键信息；
// 其余单引号字符串替换为占位符。
func keepIdentifier(s string) string {
	if identRe.MatchString(s) {
		return s
	}
	return `'<STR>'`
}

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
