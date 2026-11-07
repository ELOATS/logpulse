package timerange

import (
	"testing"
	"time"
)

var (
	start  = time.Date(2026, 11, 6, 11, 9, 13, 0, time.UTC)
	anchor = time.Date(2026, 11, 6, 14, 8, 58, 0, time.UTC)
)

func TestPickBucket(t *testing.T) {
	cases := map[time.Duration]time.Duration{
		time.Hour:            time.Minute,
		2 * time.Hour:        time.Minute,
		6 * time.Hour:        5 * time.Minute,
		24 * time.Hour:       15 * time.Minute,
		7 * 24 * time.Hour:   3 * time.Hour,
		400 * 24 * time.Hour: 24 * time.Hour,
	}
	for span, want := range cases {
		if got := PickBucket(span); got != want {
			t.Errorf("PickBucket(%v) = %v, want %v", span, got, want)
		}
	}
}

func TestResolveAnchorsToLatestAndAligns(t *testing.T) {
	r, err := Resolve("1h", start, anchor, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Bucket != time.Minute || r.BucketLabel != "1m" || r.BucketSeconds != 60 {
		t.Errorf("bucket: %+v", r)
	}
	if want := time.Date(2026, 11, 6, 13, 8, 0, 0, time.UTC); !r.From.Equal(want) {
		t.Errorf("from = %v, want %v", r.From, want)
	}
	if want := time.Date(2026, 11, 6, 14, 9, 0, 0, time.UTC); !r.To.Equal(want) {
		t.Errorf("to = %v, want %v", r.To, want)
	}
}

func TestResolveRangeLongerThanData(t *testing.T) {
	r, _ := Resolve("24h", start, anchor, 0) // 数据只有 3 小时
	if r.Bucket != 15*time.Minute {
		t.Fatalf("bucket = %v", r.Bucket)
	}
	if want := time.Date(2026, 11, 6, 11, 0, 0, 0, time.UTC); !r.From.Equal(want) {
		t.Errorf("from = %v, want first data bucket %v", r.From, want)
	}
}

func TestResolveAll(t *testing.T) {
	r, _ := Resolve("all", start, anchor, 0)
	if r.Bucket != 5*time.Minute || !r.From.Equal(time.Date(2026, 11, 6, 11, 5, 0, 0, time.UTC)) {
		t.Errorf("all: %+v", r)
	}
	r, _ = Resolve("all", start, anchor, time.Hour) // 指定桶大小
	if r.Bucket != time.Hour || !r.From.Equal(time.Date(2026, 11, 6, 11, 0, 0, 0, time.UTC)) {
		t.Errorf("all with bucket: %+v", r)
	}
}

func TestPartial(t *testing.T) {
	r, _ := Resolve("all", start, anchor, 5*time.Minute)
	if !r.Partial(time.Date(2026, 11, 6, 11, 5, 0, 0, time.UTC)) {
		t.Error("bucket starting before the first log should be partial")
	}
	if r.Partial(time.Date(2026, 11, 6, 11, 10, 0, 0, time.UTC)) {
		t.Error("fully covered bucket should not be partial")
	}
	if !r.Partial(time.Date(2026, 11, 6, 14, 5, 0, 0, time.UTC)) {
		t.Error("bucket still in progress should be partial")
	}
	// 数据完整覆盖范围时，对齐后的第一个桶是完整的
	r, _ = Resolve("1h", start, anchor, 0)
	if r.Partial(r.From) {
		t.Error("first bucket of a fully covered range should be complete")
	}
}

func TestDurationParsing(t *testing.T) {
	if d, err := Duration("90m"); err != nil || d != 90*time.Minute {
		t.Errorf("90m: %v %v", d, err)
	}
	for _, bad := range []string{"", "yesterday", "10s", "-1h"} {
		if _, err := Duration(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestFloorUsesUnixTime(t *testing.T) {
	// 7 分钟不能整除一天：按 Unix 时间取整，与数据库分桶一致
	ts := time.Date(2026, 11, 6, 14, 8, 58, 0, time.UTC)
	got := Floor(ts, 7*time.Minute)
	if got.Unix()%(7*60) != 0 || got.After(ts) || ts.Sub(got) >= 7*time.Minute {
		t.Errorf("Floor = %v", got)
	}
	r, _ := Resolve("1h", start, anchor, 7*time.Minute)
	if r.From.Unix()%(7*60) != 0 || r.To.Unix()%(7*60) != 0 {
		t.Errorf("range not aligned to unix multiples: %v ~ %v", r.From, r.To)
	}
	if n := len(r.Starts()); n != int(r.To.Sub(r.From)/r.Bucket) {
		t.Errorf("Starts len %d", n)
	}
}
