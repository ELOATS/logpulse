package store

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/eloats/logpulse/backend/internal/dbtest"
	"github.com/eloats/logpulse/backend/internal/parser"
)

// 集成测试：需要 PostgreSQL，见 dbtest 包说明。
func testStore(t *testing.T) *Store { return testStoreAt(t, "") }

// testStoreAt 在临时 schema 中执行到指定迁移版本（空表示全部）。
func testStoreAt(t *testing.T, migration string) *Store {
	return New(dbtest.Pool(t, migration))
}

var t0 = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

// seed 写入 3 小时、3 个服务、多种级别的日志，分多个批次写入（同一分钟跨批次，检验计数累加）。
func seed(t *testing.T, s *Store) {
	r := rand.New(rand.NewSource(1))
	services := []string{"api", "pay", "web"}
	levels := []string{"INFO", "INFO", "INFO", "WARN", "ERROR", "FATAL", "DEBUG"}
	var batch []parser.Entry
	for i := 0; i < 6000; i++ {
		ts := t0.Add(time.Duration(r.Int63n(int64(3 * time.Hour))))
		batch = append(batch, parser.Entry{
			Timestamp: ts, Service: services[r.Intn(3)], Level: levels[r.Intn(len(levels))],
			Message: fmt.Sprintf("event %d", r.Intn(50)), Raw: "x", Format: "plain", Attributes: map[string]any{},
		})
		if len(batch) == 700 {
			if _, err := s.Ingest(context.Background(), batch); err != nil {
				t.Fatal(err)
			}
			batch = batch[:0]
		}
	}
	if _, err := s.Ingest(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
}

func tp(d time.Duration) *time.Time { v := t0.Add(d); return &v }

var ranges = []struct {
	name     string
	from, to *time.Time
}{
	{"全部", nil, nil},
	{"仅起点（非整分钟）", tp(37*time.Minute + 13*time.Second), nil},
	{"仅终点（非整分钟）", nil, tp(2*time.Hour + 5*time.Minute + 42*time.Second)},
	{"首尾都不是整分钟", tp(12*time.Minute + 1*time.Second), tp(95*time.Minute + 59*time.Second)},
	{"整分钟对齐", tp(30 * time.Minute), tp(90 * time.Minute)},
	{"不足一分钟", tp(50*time.Minute + 10*time.Second), tp(50*time.Minute + 40*time.Second)},
	{"跨一个分钟边界", tp(50*time.Minute + 40*time.Second), tp(51*time.Minute + 20*time.Second)},
}

func TestOverviewMatchesRaw(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	ctx := context.Background()
	for _, rg := range ranges {
		got, err := s.Overview(ctx, rg.from, rg.to)
		if err != nil {
			t.Fatal(err)
		}
		want, err := s.overviewRaw(ctx, rg.from, rg.to)
		if err != nil {
			t.Fatal(err)
		}
		if got.TotalLogs != want.TotalLogs || got.ErrorLogs != want.ErrorLogs || got.Services != want.Services ||
			!reflect.DeepEqual(got.Levels, want.Levels) || !sameTimes(got.FirstLog, want.FirstLog) || !sameTimes(got.LastLog, want.LastLog) {
			t.Errorf("%s:\n got  total=%d err=%d svc=%d levels=%v first=%v last=%v\n want total=%d err=%d svc=%d levels=%v first=%v last=%v",
				rg.name, got.TotalLogs, got.ErrorLogs, got.Services, got.Levels, got.FirstLog, got.LastLog,
				want.TotalLogs, want.ErrorLogs, want.Services, want.Levels, want.FirstLog, want.LastLog)
		}
		if !reflect.DeepEqual(asMap(got.TopServices), asMap(want.TopServices)) {
			t.Errorf("%s: top services %v != %v", rg.name, got.TopServices, want.TopServices)
		}
	}
}

func TestTimeseriesMatchesRaw(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	ctx := context.Background()
	for _, rg := range ranges {
		for _, f := range []LogFilter{{}, {Service: "pay"}, {Levels: []string{"ERROR", "FATAL"}}, {Service: "api", Levels: []string{"WARN"}}} {
			f.From, f.To = rg.from, rg.to
			for _, b := range []time.Duration{time.Minute, 5 * time.Minute, time.Hour} {
				if !usesCounts(f, b) {
					t.Fatal("expected counts path")
				}
				got, err := s.Timeseries(ctx, f, b)
				if err != nil {
					t.Fatal(err)
				}
				want, err := s.timeseries(ctx, f, b, false)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s / %+v / %v: %d points vs %d points\n got  %v\n want %v", rg.name, f, b, len(got), len(want), head(got), head(want))
				}
			}
		}
	}
}

func TestCountSeriesMatchesRaw(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	ctx := context.Background()
	from, to := t0.Add(20*time.Minute), t0.Add(140*time.Minute)
	for _, b := range []time.Duration{time.Minute, 5 * time.Minute, 90 * time.Second} { // 90s 走原始日志路径
		got, err := s.CountSeries(ctx, from, to, b)
		if err != nil {
			t.Fatal(err)
		}
		want := map[[2]any][2]int64{}
		rows, err := s.Pool.Query(ctx, `SELECT service, floor(extract(epoch FROM ts - $1::timestamptz) / $3)::int,
			count(*), count(*) FILTER (WHERE level IN ('ERROR','FATAL')) FROM logs WHERE ts >= $1 AND ts < $2 GROUP BY 1, 2`, from, to, b.Seconds())
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var svc string
			var idx int
			var n, e int64
			_ = rows.Scan(&svc, &idx, &n, &e)
			want[[2]any{svc, idx}] = [2]int64{n, e}
		}
		rows.Close()
		gotMap := map[[2]any][2]int64{}
		for _, r := range got {
			gotMap[[2]any{r.Service, r.Index}] = [2]int64{r.Total, r.Errors}
		}
		if !reflect.DeepEqual(gotMap, want) {
			t.Errorf("bucket %v: %d rows vs %d rows", b, len(gotMap), len(want))
		}
	}
}

func sameTimes(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func asMap(ns []NameCount) map[string]NameCount {
	m := map[string]NameCount{}
	for _, n := range ns {
		m[n.Name] = n
	}
	return m
}

func head(p []TimePoint) []TimePoint {
	if len(p) > 3 {
		return p[:3]
	}
	return p
}

// 回归：超长消息在截断处切开多字节字符时，曾导致 PostgreSQL 拒绝整批写入。
func TestIngestLongMultibyteMessage(t *testing.T) {
	s := testStore(t)
	msg := strings.Repeat("a", 999) + strings.Repeat("错", 800) // 第 1000、2000 字节都落在字符中间
	e := parser.Entry{Timestamp: t0, Service: "api", Level: "ERROR", Message: msg, Raw: msg, Format: "plain", Attributes: map[string]any{}}
	if _, err := s.Ingest(context.Background(), []parser.Entry{e}); err != nil {
		t.Fatal(err)
	}
}

// overviewRaw 直接扫描 logs 表计算总览：独立实现，作为测试对照。
func (s *Store) overviewRaw(ctx context.Context, from, to *time.Time) (Overview, error) {
	o := Overview{Levels: map[string]int64{}, TopServices: []NameCount{}}
	where, args := LogFilter{From: from, To: to}.where()
	err := s.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE level IN ('ERROR','FATAL')),
		count(DISTINCT service), min(ts), max(ts) FROM logs `+where, args...).
		Scan(&o.TotalLogs, &o.ErrorLogs, &o.Services, &o.FirstLog, &o.LastLog)
	if err != nil {
		return o, err
	}
	if o.TotalLogs > 0 {
		o.ErrorRate = float64(o.ErrorLogs) / float64(o.TotalLogs)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM log_patterns`).Scan(&o.Patterns); err != nil {
		return o, err
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM anomalies WHERE NOT acknowledged`).Scan(&o.OpenAnomalies); err != nil {
		return o, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT level, count(*) FROM logs `+where+` GROUP BY level`, args...)
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var l string
		var c int64
		if err := rows.Scan(&l, &c); err != nil {
			rows.Close()
			return o, err
		}
		o.Levels[l] = c
	}
	rows.Close()
	rows, err = s.Pool.Query(ctx, `SELECT service, count(*), count(*) FILTER (WHERE level IN ('ERROR','FATAL'))
		FROM logs `+where+` GROUP BY service ORDER BY count(*) DESC LIMIT 10`, args...)
	if err != nil {
		return o, err
	}
	defer rows.Close()
	for rows.Next() {
		var n NameCount
		if err := rows.Scan(&n.Name, &n.Count, &n.Errors); err != nil {
			return o, err
		}
		o.TopServices = append(o.TopServices, n)
	}
	return o, rows.Err()
}
