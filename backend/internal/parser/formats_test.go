package parser

import (
	"strings"
	"testing"
	"time"
)

var ts20240501 = time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)

func TestGoLogFormats(t *testing.T) {
	cases := []struct {
		name, line, format, level, msg string
		attr                           string // 期望存在的附加字段
	}{
		{"stdlog", `2024/05/01 10:00:00 failed to connect: dial tcp 10.0.0.1:5432: connect: connection refused`,
			"text", "ERROR", "failed to connect: dial tcp 10.0.0.1:5432: connect: connection refused", ""},
		{"stdlog-shortfile", `2024/05/01 10:00:00 main.go:42: query failed: context deadline exceeded`,
			"text", "ERROR", "query failed: context deadline exceeded", "caller"},
		{"slog-text", `time=2024-05-01T10:00:00.000Z level=ERROR msg="db query failed" err="context deadline exceeded" user_id=42`,
			"logfmt", "ERROR", "db query failed: context deadline exceeded", "user_id"},
		{"slog-text-info", `time=2024-05-01T10:00:00Z level=INFO msg="retrying after error budget check" attempt=2`,
			"logfmt", "INFO", "retrying after error budget check", "attempt"},
		{"logrus", `time="2024-05-01T10:00:00Z" level=error msg="payment failed" error="timeout" order_id=7`,
			"logfmt", "ERROR", "payment failed: timeout", "order_id"},
		{"slog-json", `{"time":"2024-05-01T10:00:00Z","level":"ERROR","msg":"db query failed","err":"context deadline exceeded"}`,
			"json", "ERROR", "db query failed: context deadline exceeded", ""},
		{"zap-json", `{"level":"error","ts":1714557600,"caller":"svc/main.go:42","msg":"request failed","error":"EOF","stacktrace":"main.main\n\t/app/main.go:42"}`,
			"json", "ERROR", "request failed: EOF", "error_location"},
		{"zap-console", "2024-05-01T10:00:00.000Z\tERROR\tsvc/main.go:42\trequest failed\t{\"error\": \"EOF\", \"path\": \"/pay\"}",
			"text", "ERROR", "request failed: EOF", "path"},
	}
	p := newTestParser()
	for _, c := range cases {
		e, ok := p.ParseLine(c.line)
		if !ok {
			t.Fatalf("%s: not parsed", c.name)
		}
		if e.Format != c.format || e.Level != c.level || e.Message != c.msg {
			t.Errorf("%s:\n got  format=%s level=%s msg=%q\n want format=%s level=%s msg=%q", c.name, e.Format, e.Level, e.Message, c.format, c.level, c.msg)
		}
		if !e.Timestamp.Equal(ts20240501) {
			t.Errorf("%s: timestamp %v", c.name, e.Timestamp)
		}
		if c.attr != "" && e.Attributes[c.attr] == nil {
			t.Errorf("%s: missing attribute %q in %+v", c.name, c.attr, e.Attributes)
		}
	}
}

func TestLogfmtRejectsPlainText(t *testing.T) {
	for _, line := range []string{"a=b and some words", "key=value", `msg="unterminated`} {
		if _, ok := parseLogfmt(line); ok {
			t.Errorf("%q should not be treated as logfmt", line)
		}
	}
}

func TestJSErrorLevel(t *testing.T) {
	p := newTestParser()
	for _, line := range []string{
		"Uncaught TypeError: Cannot read properties of undefined (reading 'map')",
		"Unhandled Promise rejection: request aborted",
		"ReferenceError: foo is not defined",
		"java.lang.NullPointerException: Cannot invoke \"String.length()\"",
	} {
		if e, _ := p.ParseLine(line); e.Level != "ERROR" {
			t.Errorf("%q: level %s, want ERROR", line, e.Level)
		}
	}
	if e, _ := p.ParseLine("render took 12ms"); e.Level != "INFO" {
		t.Errorf("plain line misdetected as %s", e.Level)
	}
}

// join 模拟逐行输入，返回合并后的记录。
func join(input string) []string {
	j := NewJoiner()
	var out []string
	for _, l := range strings.Split(input, "\n") {
		if rec, ok := j.Push(l); ok {
			out = append(out, rec)
		}
	}
	if rec, ok := j.Flush(); ok {
		out = append(out, rec)
	}
	return out
}

const goPanic = `2024/05/01 10:00:00 handling request /pay
panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x4a2b3c]

goroutine 18 [running]:
panic({0x6b5e20?, 0x9c1f90?})
	/usr/local/go/src/runtime/panic.go:785 +0x132
main.(*Handler).Pay(0xc000010000, {0x7a4d38, 0xc0000a0000})
	/app/handler.go:42 +0x1d
created by net/http.(*Server).Serve in goroutine 1
	/usr/local/go/src/net/http/server.go:3360 +0x485
exit status 2
{"level":"info","msg":"restarted"}`

func TestJoinGoPanic(t *testing.T) {
	recs := join(goPanic)
	if len(recs) != 3 {
		t.Fatalf("expected 3 records (log, panic, json), got %d:\n%s", len(recs), strings.Join(recs, "\n-----\n"))
	}
	e, _ := newTestParser().ParseRecord(recs[1])
	if e.Level != "FATAL" || e.Format != "go-panic" || !strings.HasPrefix(e.Message, "panic: runtime error") {
		t.Fatalf("bad panic entry: %+v", e)
	}
	if got := e.Attributes["error_location"]; got != "main.(*Handler).Pay (/app/handler.go:42)" {
		t.Errorf("error_location = %v", got)
	}
	if !strings.Contains(e.Attributes["stack"].(string), "goroutine 18 [running]:") || !strings.Contains(e.Raw, "exit status 2") {
		t.Error("stack not preserved")
	}
}

func TestJoinJSAndJavaStacks(t *testing.T) {
	input := `Uncaught TypeError: Cannot read properties of undefined (reading 'map')
    at Dashboard (https://shop.example.com/assets/index-abc.js:1:2345)
    at renderWithHooks (https://shop.example.com/assets/vendor.js:1:999)
2024-05-01 10:00:01 ERROR [order] java.lang.IllegalStateException: boom
	at com.shop.Order.place(Order.java:42)
Caused by: java.io.IOException: disk full
	... 3 more
2024-05-01 10:00:02 INFO [order] recovered`
	recs := join(input)
	if len(recs) != 3 {
		t.Fatalf("expected 3 records, got %d:\n%s", len(recs), strings.Join(recs, "\n-----\n"))
	}
	p := newTestParser()
	js, _ := p.ParseRecord(recs[0])
	if js.Level != "ERROR" || js.Attributes["error_location"] != "Dashboard (https://shop.example.com/assets/index-abc.js:1:2345)" {
		t.Errorf("bad js entry: level=%s attrs=%+v", js.Level, js.Attributes)
	}
	java, _ := p.ParseRecord(recs[1])
	if java.Service != "order" || !strings.Contains(java.Attributes["stack"].(string), "Caused by: java.io.IOException") {
		t.Errorf("bad java entry: %+v", java)
	}
}

func TestJoinLeavesSingleLinesAlone(t *testing.T) {
	input := "line one\n\nline two\n{\"msg\":\"x\"}\n  {\"msg\":\"indented json\"}"
	if recs := join(input); len(recs) != 4 {
		t.Fatalf("expected 4 records, got %d: %q", len(recs), recs)
	}
}

func TestJoinTruncatesHugeStacks(t *testing.T) {
	j := NewJoiner()
	j.MaxLines = 3
	j.Push("Error: boom")
	for i := 0; i < 10; i++ {
		j.Push("    at frame")
	}
	rec, _ := j.Flush()
	if strings.Count(rec, "\n") != 3 || !strings.HasSuffix(rec, "(8 more lines truncated)") {
		t.Fatalf("unexpected record %q", rec)
	}
}

func TestStackLocationSkipsNativeFrames(t *testing.T) {
	stack := "SyntaxError: bad json\n    at JSON.parse (<anonymous>)\n    at loadCart (https://shop.example.com/app.js:10:5)"
	if got := StackLocation(stack); got != "loadCart (https://shop.example.com/app.js:10:5)" {
		t.Errorf("got %q", got)
	}
	if got := StackLocation("Error\n    at JSON.parse (<anonymous>)"); got != "JSON.parse (<anonymous>)" {
		t.Errorf("only native frame should still be returned, got %q", got)
	}
}
