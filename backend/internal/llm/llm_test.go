package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/eloats/logpulse/backend/internal/store"
)

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"login failed password=hunter2 user=bob":          "login failed password=<REDACTED> user=bob",
		`config api_key: "sk-123" loaded`:                 `config api_key: <REDACTED> loaded`,
		"Authorization: Bearer abc.def.ghi":               "Authorization: <REDACTED>",
		"retrying with Bearer abc.def.ghi":                "retrying with Bearer <REDACTED>",
		"sent mail to alice@example.com":                  "sent mail to <REDACTED>",
		"connection to 10.1.2.3:5432 refused (attempt 2)": "connection to 10.1.2.3:5432 refused (attempt 2)",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func sampleInput() Input {
	start := time.Date(2026, 11, 6, 12, 50, 0, 0, time.UTC)
	return Input{
		Anomaly: store.Anomaly{
			Kind: "error_spike", Severity: "high", Service: "payment-service",
			WindowStart: start, WindowEnd: start.Add(5 * time.Minute),
			Observed: 39, Expected: 0, Score: 11, Description: "服务 payment-service 的错误日志突增",
			Details: json.RawMessage(`{"top_patterns":[{"template":"payment gateway timeout after <NUM> for order <NUM>","level":"ERROR","count":39}]}`),
		},
		SampleLogs: []store.LogRow{{Timestamp: start.Add(time.Minute), Level: "ERROR", Host: "pod-1", Message: "payment gateway timeout after 3333ms token=abc"}},
		Baseline:   []store.Pattern{{Level: "INFO", Count: 769, Template: "request handled in <NUM>"}},
	}
}

func TestBuildPrompt(t *testing.T) {
	p := BuildPrompt(sampleInput())
	for _, want := range []string{
		"<anomaly>", "错误日志突增 (error_spike)", "payment-service", "观测值: 39 条",
		"<top_patterns", "[ERROR] x39  payment gateway timeout after <NUM> for order <NUM>",
		"<sample_logs", "host=pod-1", "token=<REDACTED>", "<baseline_patterns", "request handled in <NUM>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q\n%s", want, p)
		}
	}
	if strings.Contains(p, "<lead_up_logs") {
		t.Error("empty lead-up section should be omitted")
	}
	if strings.Contains(p, "abc") {
		t.Error("secret leaked into prompt")
	}
}

// fakeAPI 模拟 Messages API，记录请求并返回给定响应。
func fakeAPI(t *testing.T, resp string) (*httptest.Server, *map[string]any, *http.Header) {
	var body map[string]any
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		hdr = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)
	return srv, &body, &hdr
}

func newTestSummarizer(url string) *Summarizer {
	return New("claude-opus-5-5", "medium", option.WithBaseURL(url), option.WithAPIKey("test"), option.WithMaxRetries(0))
}

func TestSummarizeRequestAndParse(t *testing.T) {
	insight := `{"summary":"支付网关超时导致 payment-service 错误突增","impact":"部分支付失败","likely_causes":[{"cause":"下游支付网关响应变慢","evidence":"39 条 payment gateway timeout"}],"next_steps":["检查网关延迟指标","确认重试与超时配置","检索关键词 gateway timeout"],"confidence":"medium"}`
	textJSON, _ := json.Marshal(insight)
	srv, body, hdr := fakeAPI(t, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5",
		"content":[{"type":"text","text":`+string(textJSON)+`}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":20}}`)

	got, model, err := newTestSummarizer(srv.URL).Summarize(context.Background(), sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	if model != "claude-opus-5-5" || got.Confidence != "medium" || len(got.NextSteps) != 3 || got.LikelyCauses[0].Evidence == "" {
		t.Fatalf("unexpected insight %+v (model %s)", got, model)
	}

	b := *body
	if b["model"] != "claude-opus-5-5" || b["fallbacks"] != "default" {
		t.Errorf("model/fallbacks wrong: %v / %v", b["model"], b["fallbacks"])
	}
	if !strings.Contains(hdr.Get("Anthropic-Beta"), "server-side-fallback-2026-07-01") {
		t.Errorf("missing fallback beta header: %q", hdr.Get("Anthropic-Beta"))
	}
	oc, _ := b["output_config"].(map[string]any)
	format, _ := oc["format"].(map[string]any)
	schema, _ := format["schema"].(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	if oc["effort"] != "medium" || format["type"] != "json_schema" || props["next_steps"] == nil || props["likely_causes"] == nil {
		t.Errorf("output_config wrong: %v", oc)
	}
	if _, ok := b["thinking"]; ok {
		t.Error("thinking should be left unset on this model")
	}
}

func TestSummarizeRefusal(t *testing.T) {
	srv, _, _ := fakeAPI(t, `{"id":"msg_2","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],
		"stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber","explanation":"x"},"usage":{"input_tokens":1,"output_tokens":0}}`)
	_, _, err := newTestSummarizer(srv.URL).Summarize(context.Background(), sampleInput())
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "cyber") {
		t.Fatalf("expected refusal error, got %v", err)
	}
	if Retryable(err) {
		t.Error("refusal must not be retried")
	}
}

func TestDiverseSample(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var rows []store.LogRow
	p1, p2 := int64(1), int64(2)
	for i := 0; i < 40; i++ { // 倒序：最新的在前
		rows = append(rows, store.LogRow{ID: int64(i), Timestamp: base.Add(time.Duration(100-i) * time.Second), PatternID: &p1})
	}
	rows = append(rows, store.LogRow{ID: 99, Timestamp: base, PatternID: &p2})
	got := diverseSample(rows, 30, 5)
	if len(got) != 6 {
		t.Fatalf("expected 5 from p1 + 1 from p2, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].Timestamp.Before(got[i-1].Timestamp) {
			t.Fatal("not chronological")
		}
	}
	if got[0].ID != 99 || got[1].Timestamp.Sub(got[len(got)-1].Timestamp) > -30*time.Second {
		t.Errorf("sample should include rare pattern and span the window: %+v", got)
	}
}

func TestWriteStackPrefersMostSevere(t *testing.T) {
	rows := []store.LogRow{
		{Level: "ERROR", Message: "request failed: EOF", Attributes: json.RawMessage(`{"stacktrace":"main.main\n\t/app/main.go:42"}`)},
		{Level: "FATAL", Message: "panic: nil pointer", Attributes: json.RawMessage(`{"stack":"goroutine 1 [running]:\nmain.Pay()\n\t/app/handler.go:42"}`)},
		{Level: "ERROR", Message: "no stack", Attributes: json.RawMessage(`{}`)},
	}
	var b strings.Builder
	writeStack(&b, rows)
	if !strings.Contains(b.String(), "panic: nil pointer") || !strings.Contains(b.String(), "handler.go:42") {
		t.Fatalf("expected FATAL panic stack, got:\n%s", b.String())
	}
}
