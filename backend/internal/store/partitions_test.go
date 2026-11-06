package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/eloats/logpulse/backend/internal/db"
	"github.com/eloats/logpulse/backend/internal/parser"
)

var day0 = time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

func entriesOn(day time.Time, n int, svc, msg string) []parser.Entry {
	out := make([]parser.Entry, n)
	for i := range out {
		out[i] = parser.Entry{Timestamp: day.Add(time.Duration(i) * time.Minute), Service: svc, Level: "ERROR",
			Message: fmt.Sprintf("%s %d", msg, i), Raw: "x", Format: "plain", Attributes: map[string]any{}}
	}
	return out
}

func count(t *testing.T, s *Store, q string, args ...any) int64 {
	var n int64
	if err := s.Pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// 旧版本（未分区）带数据升级到分区表：数据、id、全文检索、外键都应保持。
func TestPartitionMigrationPreservesData(t *testing.T) {
	s := testStoreAt(t, "004")
	ctx := context.Background()
	exec := func(q string, args ...any) {
		if _, err := s.Pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO log_patterns (hash, template, count, first_seen, last_seen) VALUES ('h', 'disk full', 3, $1, $1)`, day0)
	for i, ts := range []time.Time{day0.Add(time.Hour), day0.Add(25 * time.Hour), day0.Add(26 * time.Hour)} {
		exec(`INSERT INTO logs (ts, level, service, message, raw, pattern_id) VALUES ($1, 'ERROR', 'api', $2, 'r', 1)`, ts, fmt.Sprintf("disk full on node%d", i))
	}
	var maxID int64
	_ = s.Pool.QueryRow(ctx, `SELECT max(id) FROM logs`).Scan(&maxID)

	if err := db.Migrate(ctx, s.Pool); err != nil {
		t.Fatal(err)
	}
	if got := count(t, s, `SELECT count(*) FROM logs`); got != 3 {
		t.Fatalf("rows after migration = %d, want 3", got)
	}
	if got := count(t, s, `SELECT count(*) FROM logs WHERE search @@ to_tsquery('simple', 'disk')`); got != 3 {
		t.Errorf("full-text search after migration = %d, want 3", got)
	}
	parts, _ := s.Partitions(ctx)
	if len(parts) != 2 {
		t.Errorf("partitions = %+v, want 2 days", parts)
	}
	// 序列延续：新写入的 id 大于旧数据
	if _, err := s.Ingest(ctx, entriesOn(day0.Add(48*time.Hour), 1, "api", "new")); err != nil {
		t.Fatal(err)
	}
	if got := count(t, s, `SELECT min(id) FROM logs WHERE message LIKE 'new%'`); got <= maxID {
		t.Errorf("new id %d not greater than old max %d", got, maxID)
	}
	// 外键仍然有效
	if _, err := s.Pool.Exec(ctx, `INSERT INTO logs (ts, level, message, raw, pattern_id) VALUES ($1, 'INFO', 'x', 'x', 999999)`, day0); err == nil {
		t.Error("expected foreign key violation")
	}
}

func TestRetentionDropsOldPartitions(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for d := 0; d < 4; d++ { // 4 天，每天一个独有的错误模式
		if _, err := s.Ingest(ctx, entriesOn(day0.AddDate(0, 0, d), 10, "api", fmt.Sprintf("day%d failure", d))); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO anomalies (dedup_key, kind, severity, window_start, window_end, description)
		VALUES ('old', 'error_spike', 'high', $1, $2, 'x'), ('new', 'error_spike', 'high', $3, $4, 'y')`,
		day0, day0.Add(time.Hour), day0.AddDate(0, 0, 3), day0.AddDate(0, 0, 3).Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if parts, _ := s.Partitions(ctx); len(parts) != 4 {
		t.Fatalf("partitions before = %d, want 4", len(parts))
	}

	res, err := s.ApplyRetention(ctx, day0.AddDate(0, 0, 2).Add(5*time.Hour)) // 保留第 3、4 天
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DroppedPartitions) != 2 {
		t.Errorf("dropped %v, want 2 partitions", res.DroppedPartitions)
	}
	if got := count(t, s, `SELECT count(*) FROM logs`); got != 20 {
		t.Errorf("logs left = %d, want 20", got)
	}
	if got := count(t, s, `SELECT count(*) FROM log_counts WHERE bucket < $1`, day0.AddDate(0, 0, 2)); got != 0 {
		t.Errorf("stale counts left: %d", got)
	}
	if got := count(t, s, `SELECT count(*) FROM log_patterns`); got != 2 || res.DeletedPatterns != 2 {
		t.Errorf("patterns left = %d (deleted %d), want 2", got, res.DeletedPatterns)
	}
	if got := count(t, s, `SELECT count(*) FROM anomalies`); got != 1 {
		t.Errorf("anomalies left = %d, want 1", got)
	}
	// 统计与原始数据仍一致
	o, _ := s.Overview(ctx, nil, nil)
	if o.TotalLogs != 20 {
		t.Errorf("overview total = %d, want 20", o.TotalLogs)
	}
	// 迟到的旧日志：分区缓存已失效，写入会重新建分区而不是报错
	if _, err := s.Ingest(ctx, entriesOn(day0, 1, "api", "late")); err != nil {
		t.Fatalf("late write into dropped day: %v", err)
	}
}

func TestConcurrentIngestCreatesPartitionOnce(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			// 每个写入方使用独立的 Store（各自的分区缓存），模拟多个后端实例
			st := New(s.Pool)
			if _, err := st.Ingest(ctx, entriesOn(day0.AddDate(0, 1, 0), 50, fmt.Sprintf("svc%d", g), "boom")); err != nil {
				errs <- err
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := count(t, s, `SELECT count(*) FROM logs`); got != 400 {
		t.Errorf("logs = %d, want 400", got)
	}
}
