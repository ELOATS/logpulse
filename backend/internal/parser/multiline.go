package parser

import (
	"fmt"
	"regexp"
	"strings"
)

// Joiner 把逐行读入的日志合并为完整记录：异常堆栈的后续行会并入其首行。
// 支持：
//   - 缩进的续行（JS/Java 的 "    at ..."、Go 堆栈的 "\t/path/file.go:42"）
//   - Java 的 "Caused by:" / "... 12 more"
//   - Go 的 panic / fatal error 输出（含不缩进的 goroutine 与函数帧行、空行）
type Joiner struct {
	MaxLines int // 单条记录最多保留的行数，超出部分只计数

	lines     []string
	inTrace   bool
	truncated int
}

func NewJoiner() *Joiner { return &Joiner{MaxLines: 200} }

var (
	goTraceLine = regexp.MustCompile(`^(goroutine \d+ \[|\[signal |created by |exit status \d+|[\w./*()\[\]{},:-]+\(.*\)( \+0x[0-9a-f]+)?$)`)
	javaCont    = regexp.MustCompile(`^(Caused by: |Suppressed: |\.\.\. \d+ more)`)
)

// Push 输入一行；当这一行开启了新记录时，返回上一条已完整的记录。
func (j *Joiner) Push(line string) (string, bool) {
	line = strings.TrimRight(line, "\r\n")
	if len(j.lines) > 0 && j.isContinuation(line) {
		if len(j.lines) < j.MaxLines {
			j.lines = append(j.lines, line)
		} else {
			j.truncated++
		}
		return "", false
	}
	rec, ok := j.Flush()
	if strings.TrimSpace(line) != "" {
		j.lines = append(j.lines, line)
		j.inTrace = strings.HasPrefix(line, "panic: ") || strings.HasPrefix(line, "fatal error: ")
	}
	return rec, ok
}

// Flush 返回缓冲中的记录（输入结束时调用）。
func (j *Joiner) Flush() (string, bool) {
	if len(j.lines) == 0 {
		return "", false
	}
	// 去掉记录末尾的空行（Go traceback 中允许空行）
	end := len(j.lines)
	for end > 1 && strings.TrimSpace(j.lines[end-1]) == "" {
		end--
	}
	rec := strings.Join(j.lines[:end], "\n")
	if j.truncated > 0 {
		rec += fmt.Sprintf("\n... (%d more lines truncated)", j.truncated)
	}
	j.lines, j.inTrace, j.truncated = j.lines[:0], false, 0
	return rec, true
}

func (j *Joiner) isContinuation(line string) bool {
	trimmed := strings.TrimSpace(line)
	switch {
	case trimmed == "":
		return j.inTrace
	case line[0] == ' ' || line[0] == '\t':
		return !strings.HasPrefix(trimmed, "{") // 缩进的 JSON 视为新记录
	case javaCont.MatchString(line):
		return true
	case j.inTrace:
		return goTraceLine.MatchString(line)
	}
	return false
}

// ParseRecord 解析一条可能包含多行堆栈的记录：首行按普通日志解析，
// 其余行存入 attributes.stack，并提取最可能出错的代码位置。
func (p *Parser) ParseRecord(rec string) (Entry, bool) {
	first, rest, multi := strings.Cut(rec, "\n")
	e, ok := p.ParseLine(first)
	if !ok || !multi {
		return e, ok
	}
	e.Raw = rec
	e.Attributes["stack"] = rest
	if loc := StackLocation(rest); loc != "" {
		e.Attributes["error_location"] = loc
	}
	// 带堆栈的记录几乎总是错误；首行没有级别信息时提升为 ERROR
	if (e.Level == "INFO" || e.Level == "DEBUG") && looksLikeStack(rest) {
		e.Level = "ERROR"
	}
	return e, true
}

var (
	jsFrameRe   = regexp.MustCompile(`(?m)^\s*at (.+)$`)                  // Chrome / Node / Java
	ffFrameRe   = regexp.MustCompile(`(?m)^\s*([\w$.<>/]*@\S+:\d+:\d+)$`) // Firefox / Safari: fn@url:line:col
	goFileRe    = regexp.MustCompile(`(?m)^\t(\S+\.go:\d+)`)              // Go 堆栈中的文件行
	goFuncRe    = regexp.MustCompile(`^([\w./*()\[\]{},-]+)\(`)           // Go 堆栈中的函数行
	stackHintRe = regexp.MustCompile(`(?m)^\s+at |\.go:\d+|^goroutine \d+|@\S+:\d+:\d+`)
)

// StackLocation 从堆栈中提取首个业务代码位置（跳过 Go runtime 帧）。
func StackLocation(stack string) string {
	if goFileRe.MatchString(stack) {
		lines := strings.Split(stack, "\n")
		for i, l := range lines {
			if !strings.HasPrefix(l, "\t") || !strings.Contains(l, ".go:") || strings.Contains(l, "/src/runtime/") {
				continue
			}
			file := strings.Fields(strings.TrimSpace(l))[0]
			if i > 0 {
				if fm := goFuncRe.FindStringSubmatch(lines[i-1]); fm != nil {
					return fm[1] + " (" + file + ")"
				}
			}
			return file
		}
	}
	// JS/Java：取第一个不是引擎内置函数的帧（跳过 "JSON.parse (<anonymous>)"、"(native)" 等）
	var firstFrame string
	for _, m := range jsFrameRe.FindAllStringSubmatch(stack, -1) {
		frame := strings.TrimSpace(m[1])
		if firstFrame == "" {
			firstFrame = frame
		}
		if !strings.Contains(frame, "<anonymous>") && !strings.Contains(frame, "(native)") {
			return frame
		}
	}
	if firstFrame != "" {
		return firstFrame
	}
	if m := ffFrameRe.FindStringSubmatch(stack); m != nil {
		return m[1]
	}
	return ""
}

func looksLikeStack(s string) bool { return stackHintRe.MatchString(s) }
