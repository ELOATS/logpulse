// Package parser 将原始日志行解析为结构化条目。
// 支持：JSON 行日志、Nginx/Apache combined 访问日志、RFC3164 Syslog、
// 以 "时间戳 级别 消息" 开头的通用文本日志，其余作为纯文本兜底。
package parser

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Entry 是一条结构化日志。
type Entry struct {
	Timestamp  time.Time      `json:"timestamp"`
	Level      string         `json:"level"`
	Service    string         `json:"service"`
	Host       string         `json:"host"`
	Message    string         `json:"message"`
	Raw        string         `json:"raw"`
	Format     string         `json:"format"`
	Attributes map[string]any `json:"attributes"`

	// NoTimestamp 表示原始日志没有时间戳，Timestamp 使用的是解析时刻
	NoTimestamp bool `json:"-"`
}

// Parser 持有解析时的默认值。
type Parser struct {
	DefaultService string
	Now            func() time.Time
}

func New(defaultService string) *Parser {
	return &Parser{DefaultService: defaultService, Now: time.Now}
}

var (
	combinedRe = regexp.MustCompile(`^(\S+) \S+ (\S+) \[([^\]]+)\] "([^"]*)" (\d{3}) (\d+|-)(?: "([^"]*)" "([^"]*)")?`)
	syslogRe   = regexp.MustCompile(`^(?:<\d+>)?([A-Z][a-z]{2}\s+\d{1,2} \d{2}:\d{2}:\d{2}) (\S+) ([^:\[\s]+)(?:\[(\d+)\])?: (.*)$`)
	genericTS  = regexp.MustCompile(`^\[?(\d{4}[-/]\d{2}[-/]\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?)\]?\s+`)
	levelRe    = regexp.MustCompile(`(?i)^\[?(TRACE|DEBUG|INFO|NOTICE|WARN|WARNING|ERROR|ERR|FATAL|CRITICAL|CRIT|PANIC|SEVERE)\]?:?\s+`)
	serviceRe  = regexp.MustCompile(`^\[([\w.\-/]+)\]:?\s+`)
	callerRe   = regexp.MustCompile(`^([\w.\-/]+\.go:\d+):?\s+`) // Go 调用位置，如 main.go:42: 或 svc/main.go:42<TAB>
	errTypeRe  = regexp.MustCompile(`\b(?:Uncaught|Unhandled)\b|\b[A-Z][A-Za-z]*(?:Error|Exception)\b`)
	logfmtRe   = regexp.MustCompile(`^[\w.@-]+=`)
	keywordRe  = regexp.MustCompile(`(?i)\b(fatal|panic|critical|error|exception|failed|failure|warn|warning|timeout|refused|denied)\b`)
)

var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999Z0700",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999Z0700",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006/01/02 15:04:05.999999999", // Go 标准库 log
	"02/Jan/2006:15:04:05 -0700",
	time.RFC1123Z,
	time.RFC1123,
}

// ParseLine 解析单行日志，空行返回 ok=false。
func (p *Parser) ParseLine(line string) (Entry, bool) {
	line = strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(line) == "" {
		return Entry{}, false
	}
	var e Entry
	switch {
	case strings.HasPrefix(line, "panic: ") || strings.HasPrefix(line, "fatal error: "):
		e = Entry{Level: "FATAL", Message: line, Format: "go-panic"}
	case logfmtRe.MatchString(line):
		if m, ok := parseLogfmt(line); ok {
			e = p.fromMap(m)
			e.Format = "logfmt"
			break
		}
		e = p.parseText(line)
	case strings.HasPrefix(strings.TrimSpace(line), "{"):
		if ent, ok := p.parseJSON(line); ok {
			e = ent
			break
		}
		e = p.parseText(line)
	default:
		if ent, ok := p.parseCombined(line); ok {
			e = ent
		} else if ent, ok := p.parseSyslog(line); ok {
			e = ent
		} else {
			e = p.parseText(line)
		}
	}
	e.Raw = line
	if e.Timestamp.IsZero() {
		e.Timestamp = p.Now()
		e.NoTimestamp = true
	}
	if e.Service == "" {
		e.Service = p.DefaultService
	}
	if e.Level == "" {
		e.Level = guessLevel(e.Message)
	}
	if e.Attributes == nil {
		e.Attributes = map[string]any{}
	}
	e.Timestamp = e.Timestamp.UTC()
	return e, true
}

// FromMap 将调用方提交的 JSON 对象转为条目（用于 API 直接推送）。
func (p *Parser) FromMap(m map[string]any) Entry {
	raw, _ := json.Marshal(m)
	e := p.fromMap(m)
	e.Raw = string(raw)
	if e.Timestamp.IsZero() {
		e.Timestamp = p.Now()
	}
	if e.Service == "" {
		e.Service = p.DefaultService
	}
	if e.Level == "" {
		e.Level = guessLevel(e.Message)
	}
	e.Timestamp = e.Timestamp.UTC()
	return e
}

func (p *Parser) parseJSON(line string) (Entry, bool) {
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(line))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return Entry{}, false
	}
	return p.fromMap(m), true
}

func (p *Parser) fromMap(m map[string]any) Entry {
	e := Entry{Format: "json", Attributes: map[string]any{}}
	used := map[string]bool{}
	take := func(keys ...string) (any, bool) {
		for _, k := range keys {
			if v, ok := m[k]; ok && v != nil {
				used[k] = true
				return v, true
			}
		}
		return nil, false
	}
	if v, ok := take("timestamp", "time", "ts", "@timestamp", "datetime", "date"); ok {
		e.Timestamp = parseTimeValue(v)
	}
	if v, ok := take("level", "severity", "lvl", "loglevel", "log.level", "levelname"); ok {
		e.Level = NormalizeLevel(fmt.Sprint(v))
	}
	if v, ok := take("message", "msg", "log", "text", "event"); ok {
		e.Message = fmt.Sprint(v)
	}
	// Go 生态（zap/slog/logrus）习惯把错误放在 error/err 字段，并入消息才能按具体错误聚类
	for _, k := range []string{"error", "err"} {
		if v, ok := m[k].(string); ok && v != "" {
			used[k] = true
			e.Message = joinError(e.Message, v)
			break
		}
	}
	if v, ok := take("service", "app", "application", "logger", "component", "service.name"); ok {
		e.Service = fmt.Sprint(v)
	}
	if v, ok := take("host", "hostname", "host.name", "pod", "instance"); ok {
		e.Host = fmt.Sprint(v)
	}
	for k, v := range m {
		if !used[k] {
			e.Attributes[k] = v
		}
	}
	if e.Message == "" {
		b, _ := json.Marshal(e.Attributes)
		e.Message = string(b)
	}
	for _, k := range []string{"stack", "stacktrace", "stack_trace", "error.stack"} {
		if st, ok := e.Attributes[k].(string); ok {
			if loc := StackLocation(st); loc != "" {
				e.Attributes["error_location"] = loc
			}
			break
		}
	}
	return e
}

func joinError(msg, errText string) string {
	switch {
	case msg == "":
		return errText
	case strings.Contains(msg, errText):
		return msg
	}
	return msg + ": " + errText
}

// parseLogfmt 解析 key=value 格式（Go slog TextHandler、logrus 等），值可带引号与转义。
// 出现不带 "=" 的单词时认为不是 logfmt。
func parseLogfmt(line string) (map[string]any, bool) {
	m := map[string]any{}
	i, n := 0, len(line)
	for i < n {
		for i < n && line[i] == ' ' {
			i++
		}
		if i >= n {
			break
		}
		start := i
		for i < n && line[i] != '=' && line[i] != ' ' {
			i++
		}
		if i >= n || line[i] != '=' || i == start {
			return nil, false
		}
		key := line[start:i]
		i++
		if i < n && line[i] == '"' {
			j := i + 1
			for j < n && line[j] != '"' {
				if line[j] == '\\' {
					j++
				}
				j++
			}
			if j >= n {
				return nil, false
			}
			quoted := line[i : j+1]
			if v, err := strconv.Unquote(quoted); err == nil {
				m[key] = v
			} else {
				m[key] = quoted[1 : len(quoted)-1]
			}
			i = j + 1
		} else {
			start := i
			for i < n && line[i] != ' ' {
				i++
			}
			m[key] = line[start:i]
		}
	}
	_, hasMsg := m["msg"]
	_, hasLevel := m["level"]
	return m, len(m) >= 2 && (hasMsg || hasLevel)
}

func (p *Parser) parseCombined(line string) (Entry, bool) {
	m := combinedRe.FindStringSubmatch(line)
	if m == nil {
		return Entry{}, false
	}
	ts, err := time.Parse("02/Jan/2006:15:04:05 -0700", m[3])
	if err != nil {
		return Entry{}, false
	}
	status, _ := strconv.Atoi(m[5])
	level := "INFO"
	switch {
	case status >= 500:
		level = "ERROR"
	case status >= 400:
		level = "WARN"
	}
	attrs := map[string]any{"client_ip": m[1], "request": m[4], "status": status}
	if m[2] != "-" {
		attrs["user"] = m[2]
	}
	if m[6] != "-" {
		if n, err := strconv.Atoi(m[6]); err == nil {
			attrs["bytes"] = n
		}
	}
	if m[7] != "" && m[7] != "-" {
		attrs["referer"] = m[7]
	}
	if m[8] != "" {
		attrs["user_agent"] = m[8]
	}
	return Entry{
		Timestamp:  ts,
		Level:      level,
		Service:    "",
		Message:    fmt.Sprintf("%s status=%d", m[4], status),
		Format:     "access",
		Attributes: attrs,
	}, true
}

func (p *Parser) parseSyslog(line string) (Entry, bool) {
	m := syslogRe.FindStringSubmatch(line)
	if m == nil {
		return Entry{}, false
	}
	now := p.Now()
	ts, err := time.ParseInLocation("Jan _2 15:04:05 2006", m[1]+" "+strconv.Itoa(now.Year()), time.Local)
	if err != nil {
		return Entry{}, false
	}
	// 跨年：如果解析出的时间在未来超过一天，则视为去年
	if ts.After(now.Add(24 * time.Hour)) {
		ts = ts.AddDate(-1, 0, 0)
	}
	attrs := map[string]any{}
	if m[4] != "" {
		attrs["pid"] = m[4]
	}
	msg := m[5]
	level := ""
	if lm := levelRe.FindStringSubmatch(msg); lm != nil {
		level = NormalizeLevel(lm[1])
		msg = msg[len(lm[0]):]
	}
	return Entry{
		Timestamp: ts, Host: m[2], Service: m[3], Level: level,
		Message: msg, Format: "syslog", Attributes: attrs,
	}, true
}

func (p *Parser) parseText(line string) Entry {
	e := Entry{Format: "plain", Attributes: map[string]any{}}
	rest := line
	if m := genericTS.FindStringSubmatch(rest); m != nil {
		if ts, ok := parseTimeString(strings.Replace(m[1], ",", ".", 1)); ok {
			e.Timestamp = ts
			e.Format = "text"
			rest = rest[len(m[0]):]
		}
	}
	if m := levelRe.FindStringSubmatch(rest); m != nil {
		e.Level = NormalizeLevel(m[1])
		rest = rest[len(m[0]):]
	}
	if m := serviceRe.FindStringSubmatch(rest); m != nil {
		e.Service = m[1]
		rest = rest[len(m[0]):]
	}
	// "级别" 在服务名之后的情况，如 "2024-01-01 10:00:00 [api] ERROR ..."
	if e.Level == "" {
		if m := levelRe.FindStringSubmatch(rest); m != nil {
			e.Level = NormalizeLevel(m[1])
			rest = rest[len(m[0]):]
		}
	}
	if m := callerRe.FindStringSubmatch(rest); m != nil {
		e.Attributes["caller"] = m[1]
		rest = rest[len(m[0]):]
	}
	e.Message = strings.TrimSpace(rest)
	mergeTrailingJSON(&e)
	return e
}

// mergeTrailingJSON 处理 zap console 格式末尾的 "<TAB>{...}" 字段。
func mergeTrailingJSON(e *Entry) {
	idx := strings.LastIndex(e.Message, "\t{")
	if idx < 0 || !strings.HasSuffix(e.Message, "}") {
		return
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(e.Message[idx+1:]), &fields); err != nil {
		return
	}
	e.Message = strings.TrimSpace(e.Message[:idx])
	for k, v := range fields {
		if s, ok := v.(string); ok && (k == "error" || k == "err") && s != "" {
			e.Message = joinError(e.Message, s)
			continue
		}
		e.Attributes[k] = v
	}
}

// NormalizeLevel 将各种级别写法归一为 DEBUG/INFO/WARN/ERROR/FATAL。
func NormalizeLevel(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "TRACE", "DEBUG", "FINE", "FINER", "FINEST", "VERBOSE", "7":
		return "DEBUG"
	case "INFO", "INFORMATION", "NOTICE", "6", "5":
		return "INFO"
	case "WARN", "WARNING", "4":
		return "WARN"
	case "ERROR", "ERR", "SEVERE", "3":
		return "ERROR"
	case "FATAL", "CRITICAL", "CRIT", "PANIC", "EMERG", "ALERT", "0", "1", "2":
		return "FATAL"
	}
	return "INFO"
}

func guessLevel(msg string) string {
	kw := strings.ToLower(keywordRe.FindString(msg))
	switch {
	case kw == "fatal" || kw == "panic" || kw == "critical":
		return "FATAL"
	case errTypeRe.MatchString(msg): // TypeError、NullPointerException、Uncaught ... 等
		return "ERROR"
	case kw == "error" || kw == "exception" || kw == "failed" || kw == "failure" || kw == "refused" || kw == "denied":
		return "ERROR"
	case kw == "warn" || kw == "warning" || kw == "timeout":
		return "WARN"
	}
	return "INFO"
}

func parseTimeValue(v any) time.Time {
	switch t := v.(type) {
	case string:
		if ts, ok := parseTimeString(t); ok {
			return ts
		}
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return epochToTime(f)
		}
	case float64:
		return epochToTime(t)
	}
	return time.Time{}
}

func epochToTime(f float64) time.Time {
	switch {
	case f > 1e17: // 纳秒
		return time.Unix(0, int64(f))
	case f > 1e14: // 微秒
		return time.UnixMicro(int64(f))
	case f > 1e11: // 毫秒
		return time.UnixMilli(int64(f))
	}
	sec := int64(f)
	return time.Unix(sec, int64((f-float64(sec))*1e9))
}

func parseTimeString(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range timeLayouts {
		if ts, err := time.Parse(layout, s); err == nil {
			return ts, true
		}
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return epochToTime(f), true
	}
	return time.Time{}, false
}
