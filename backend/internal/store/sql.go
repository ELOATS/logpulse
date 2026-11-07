package store

import (
	"strconv"
	"strings"

	"github.com/eloats/logpulse/backend/internal/parser"
)

// sqlBuilder 收集查询参数，占位符（$1、$2…）按追加顺序编号。
type sqlBuilder struct{ args []any }

// arg 追加一个参数并返回它的占位符。
func (b *sqlBuilder) arg(v any) string {
	b.args = append(b.args, v)
	return "$" + strconv.Itoa(len(b.args))
}

// cond 把 tmpl 中的每个 ? 依次替换为 vals 对应的占位符。
func (b *sqlBuilder) cond(tmpl string, vals ...any) string {
	var sb strings.Builder
	i := 0
	for _, c := range tmpl {
		if c == '?' && i < len(vals) {
			sb.WriteString(b.arg(vals[i]))
			i++
			continue
		}
		sb.WriteRune(c)
	}
	return sb.String()
}

// where 把条件拼成 "WHERE a AND b"；没有条件时返回空串。
func where(conds []string) string {
	if len(conds) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(conds, " AND ")
}

// clampLimit 把非法或过大的 limit 换成默认值。
func clampLimit(n, def, maxN int) int {
	if n <= 0 || n > maxN {
		return def
	}
	return n
}

// errorLevelSQL 是"属于错误级别"的 SQL 条件，与 parser.ErrorLevels 保持一致。
var errorLevelSQL = "level IN ('" + strings.Join(parser.ErrorLevels, "','") + "')"
