package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestSearchPatternsAndTopPatterns(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	ctx := context.Background()

	page, err := s.SearchLogs(ctx, LogFilter{Service: "pay", Levels: []string{"ERROR"}, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	want := count(t, s, `SELECT count(*) FROM logs WHERE service = 'pay' AND level = 'ERROR'`)
	if page.Total != want || len(page.Items) != 5 || page.TotalCapped {
		t.Errorf("search: total=%d items=%d capped=%v, want total=%d items=5", page.Total, len(page.Items), page.TotalCapped, want)
	}
	for i := 1; i < len(page.Items); i++ {
		if page.Items[i].Timestamp.After(page.Items[i-1].Timestamp) {
			t.Error("results not ordered newest first")
		}
	}
	noCount, _ := s.SearchLogs(ctx, LogFilter{Service: "pay", Levels: []string{"ERROR"}, Limit: 5, SkipCount: true})
	if noCount.Total != 0 || len(noCount.Items) != 5 {
		t.Errorf("SkipCount: total=%d items=%d", noCount.Total, len(noCount.Items))
	}

	since := t0.Add(time.Hour)
	ps, err := s.Patterns(ctx, PatternFilter{Levels: []string{"ERROR", "FATAL"}, FirstSeenSince: &since, Sort: "new", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT count(*) FROM log_patterns WHERE level IN ('ERROR','FATAL') AND first_seen >= $1`, since); int64(len(ps)) != n {
		t.Errorf("patterns: got %d, want %d", len(ps), n)
	}
	for _, p := range ps {
		if (p.Level != "ERROR" && p.Level != "FATAL") || p.FirstSeen.Before(since) {
			t.Errorf("pattern outside filter: %+v", p)
		}
	}

	top, err := s.TopPatterns(ctx, "api", t0, t0.Add(30*time.Minute), []string{"ERROR", "FATAL"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for i, p := range top {
		total += p.Count
		if i > 0 && p.Count > top[i-1].Count {
			t.Error("top patterns not ordered by count")
		}
	}
	if len(top) == 0 || len(top) > 3 || total == 0 {
		t.Errorf("top patterns: %+v", top)
	}
	b, _ := json.Marshal(top[0])
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"pattern_id", "template", "level", "count"} {
		if _, ok := m[k]; !ok {
			t.Errorf("top pattern JSON missing %q: %s", k, b)
		}
	}
}

func TestUpsertAnomalySkipsUnchanged(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := Anomaly{Kind: "error_spike", Severity: "high", Service: "api", WindowStart: t0, WindowEnd: t0.Add(5 * time.Minute),
		Score: 9, Observed: 30, Expected: 2, Description: "d", Details: json.RawMessage(`{"top_patterns":[]}`)}
	xmin := func() string {
		var v string
		_ = s.Pool.QueryRow(ctx, `SELECT xmin::text FROM anomalies WHERE dedup_key = 'k'`).Scan(&v)
		return v
	}
	if ins, err := s.UpsertAnomaly(ctx, "k", a); err != nil || !ins {
		t.Fatalf("first upsert: inserted=%v err=%v", ins, err)
	}
	v1 := xmin()
	if ins, err := s.UpsertAnomaly(ctx, "k", a); err != nil || ins {
		t.Fatalf("unchanged upsert: inserted=%v err=%v", ins, err)
	}
	if xmin() != v1 {
		t.Error("unchanged anomaly was rewritten")
	}
	a.Score = 12
	if ins, err := s.UpsertAnomaly(ctx, "k", a); err != nil || ins {
		t.Fatalf("changed upsert: inserted=%v err=%v", ins, err)
	}
	if xmin() == v1 || count(t, s, `SELECT count(*) FROM anomalies WHERE score = 12`) != 1 {
		t.Error("changed anomaly was not updated")
	}

	list, err := s.Anomalies(ctx, AnomalyFilter{Kind: "error_spike", Service: "api"})
	if err != nil || len(list) != 1 {
		t.Fatalf("anomalies: %v %v", list, err)
	}
	got, err := s.GetAnomaly(ctx, list[0].ID)
	if err != nil || got.Score != 12 {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := s.GetAnomaly(ctx, 999999); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("missing anomaly: want pgx.ErrNoRows, got %v", err)
	}
}
