package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eloats/logpulse/backend/internal/analyzer"
	"github.com/eloats/logpulse/backend/internal/dbtest"
	"github.com/eloats/logpulse/backend/internal/store"
)

// 集成测试：需要 PostgreSQL，见 dbtest 包说明。
func testServer(t *testing.T, maxUploadMB int64) *httptest.Server {
	st := store.New(dbtest.Pool(t, ""))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer((&Server{
		Store: st, Analyzer: analyzer.New(st, analyzer.Options{}, log), Log: log,
		AllowedOrigin: "*", MaxUploadMB: maxUploadMB,
	}).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func call(t *testing.T, method, url, contentType, body string) (int, map[string]any) {
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestIngestSearchAndErrors(t *testing.T) {
	srv := testServer(t, 1)
	text := "2024-05-01 10:00:00 ERROR [api] db timeout\npanic: boom\n\ngoroutine 1 [running]:\nmain.main()\n\t/app/main.go:9 +0x1\n"
	code, res := call(t, "POST", srv.URL+"/api/logs?service=svc", "text/plain", text)
	if code != 200 || res["accepted"] != float64(2) {
		t.Fatalf("text ingest: %d %v", code, res)
	}
	code, res = call(t, "POST", srv.URL+"/api/logs", "application/json", `[{"level":"error","service":"pay","msg":"card declined"}]`)
	if code != 200 || res["accepted"] != float64(1) {
		t.Fatalf("json ingest: %d %v", code, res)
	}
	code, res = call(t, "GET", srv.URL+"/api/logs?q=boom", "", "")
	if code != 200 || res["total"] != float64(1) {
		t.Fatalf("search: %d %v", code, res)
	}
	items := res["items"].([]any)
	attrs := items[0].(map[string]any)["attributes"].(map[string]any)
	if attrs["timestamp_inferred"] != true || attrs["error_location"] == nil {
		t.Errorf("panic record attributes: %v", attrs)
	}

	if code, _ := call(t, "POST", srv.URL+"/api/anomalies/abc/ack", "", ""); code != 400 {
		t.Errorf("invalid id: %d, want 400", code)
	}
	if code, _ := call(t, "POST", srv.URL+"/api/anomalies/999/ack", "", ""); code != 404 {
		t.Errorf("missing anomaly: %d, want 404", code)
	}
	if code, _ := call(t, "GET", srv.URL+"/api/logs?from=yesterday", "", ""); code != 400 {
		t.Errorf("bad time: %d, want 400", code)
	}
	if code, _ := call(t, "GET", srv.URL+"/api/stats/timeseries?bucket=xx", "", ""); code != 400 {
		t.Errorf("bad bucket: %d, want 400", code)
	}
	// 超过大小限制（1 MB）：读取错误返回 400，而不是 500
	big := strings.Repeat("2024-05-01 10:00:00 INFO [api] x\n", 40000)
	if code, res := call(t, "POST", srv.URL+"/api/logs", "text/plain", big); code != 400 {
		t.Errorf("oversized body: %d %v, want 400", code, res)
	}
}
