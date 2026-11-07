package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func getJSON(t *testing.T, url string, out any) int {
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_ = json.NewDecoder(resp.Body).Decode(out)
	return resp.StatusCode
}

type dashboardJSON struct {
	Range *struct {
		From, To, Anchor string
		Bucket           string `json:"bucket"`
		BucketSeconds    int64  `json:"bucket_seconds"`
	} `json:"range"`
	Overview struct {
		TotalLogs int64 `json:"total_logs"`
	} `json:"overview"`
	Series []struct {
		Bucket  string `json:"bucket"`
		Total   int64  `json:"total"`
		Partial bool   `json:"partial"`
	} `json:"series"`
}

func TestDashboardResolvesRangeOnServer(t *testing.T) {
	srv := testServer(t, 10)
	var empty dashboardJSON
	if code := getJSON(t, srv.URL+"/api/stats/dashboard", &empty); code != 200 || empty.Range != nil || empty.Series == nil || len(empty.Series) != 0 {
		t.Fatalf("empty db: %d %+v", code, empty)
	}

	// 10:00:13 起每 30 秒一条，直到 12:59:43（共 360 条）
	var b strings.Builder
	start := time.Date(2024, 5, 1, 10, 0, 13, 0, time.UTC)
	for i := 0; i < 360; i++ {
		fmt.Fprintf(&b, "%s INFO [api] request %d\n", start.Add(time.Duration(i)*30*time.Second).Format("2006-01-02 15:04:05"), i)
	}
	if code, res := call(t, "POST", srv.URL+"/api/logs", "text/plain", b.String()); code != 200 {
		t.Fatalf("ingest: %d %v", code, res)
	}

	var d dashboardJSON
	getJSON(t, srv.URL+"/api/stats/dashboard?range=1h", &d)
	if d.Range == nil || d.Range.Bucket != "1m" || d.Range.From != "2024-05-01T11:59:00Z" || d.Range.Anchor != "2024-05-01T12:59:43Z" {
		t.Fatalf("1h range: %+v", d.Range)
	}
	if len(d.Series) != 61 || d.Series[0].Partial || !d.Series[60].Partial {
		t.Errorf("1h series: %d points, first partial=%v, last partial=%v", len(d.Series), d.Series[0].Partial, d.Series[len(d.Series)-1].Partial)
	}
	var sum int64
	for _, p := range d.Series {
		sum += p.Total
	}
	if d.Overview.TotalLogs != 122 || sum != 122 { // 起点对齐到 11:59:00：第 238 条（11:59:13）~ 第 359 条（12:59:43）
		t.Errorf("1h totals: overview=%d series=%d, want 122", d.Overview.TotalLogs, sum)
	}

	var all dashboardJSON
	getJSON(t, srv.URL+"/api/stats/dashboard?range=all", &all)
	if all.Range.Bucket != "5m" || all.Range.From != "2024-05-01T10:00:00Z" || !all.Series[0].Partial || all.Series[1].Partial {
		t.Errorf("all: %+v first=%+v second=%+v", all.Range, all.Series[0], all.Series[1])
	}
	if all.Overview.TotalLogs != 360 {
		t.Errorf("all total = %d", all.Overview.TotalLogs)
	}
	var def dashboardJSON
	getJSON(t, srv.URL+"/api/stats/dashboard", &def)
	if def.Range.From != "2024-05-01T10:00:00Z" || def.Range.Bucket != "15m" { // 默认 24h，起点不早于数据所在的第一个桶
		t.Errorf("default range: %+v", def.Range)
	}

	// 旧接口同样接受 range，结果与 dashboard 一致
	var ov struct {
		TotalLogs int64 `json:"total_logs"`
	}
	getJSON(t, srv.URL+"/api/stats/overview?range=1h", &ov)
	var ts []struct {
		Partial bool `json:"partial"`
	}
	getJSON(t, srv.URL+"/api/stats/timeseries?range=1h", &ts)
	if ov.TotalLogs != 122 || len(ts) != 61 || !ts[60].Partial {
		t.Errorf("overview/timeseries with range: total=%d points=%d", ov.TotalLogs, len(ts))
	}

	for _, u := range []string{"/api/stats/dashboard?range=yesterday", "/api/stats/dashboard?range=10s", "/api/stats/dashboard?bucket=0", "/api/stats/timeseries?range=-1h"} {
		if code, _ := call(t, "GET", srv.URL+u, "", ""); code != 400 {
			t.Errorf("%s: %d, want 400", u, code)
		}
	}
}

func TestDashboardFillsEmptyBuckets(t *testing.T) {
	srv := testServer(t, 10)
	// 10:00 ~ 10:09 每分钟一条，然后中断 10 分钟，10:20 ~ 10:29 再每分钟一条
	var b strings.Builder
	for _, m := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29} {
		fmt.Fprintf(&b, "2024-05-01 10:%02d:30 INFO [api] tick\n", m)
	}
	if code, res := call(t, "POST", srv.URL+"/api/logs", "text/plain", b.String()); code != 200 {
		t.Fatalf("ingest: %d %v", code, res)
	}
	var d dashboardJSON
	getJSON(t, srv.URL+"/api/stats/dashboard?range=1h&bucket=1m", &d)
	if len(d.Series) != 30 {
		t.Fatalf("points = %d, want 30 (10:00 ~ 10:29, gap filled)", len(d.Series))
	}
	for i, p := range d.Series {
		want := int64(1)
		if i >= 10 && i < 20 {
			want = 0
		}
		if p.Total != want {
			t.Errorf("%s: total %d, want %d", p.Bucket, p.Total, want)
		}
	}
	if !d.Series[0].Partial || !d.Series[29].Partial || d.Series[15].Partial {
		t.Errorf("partial flags: first=%v gap=%v last=%v", d.Series[0].Partial, d.Series[15].Partial, d.Series[29].Partial)
	}
}
