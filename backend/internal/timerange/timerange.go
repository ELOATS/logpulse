// Package timerange 解析"最近 N"类时间范围：以最新一条日志为锚点计算起点、
// 选择时间桶大小、把起点对齐到桶边界，并标记不完整的时间桶。
//
// 这些规则集中在服务端，客户端只需传 range=24h 并按返回结果渲染。
package timerange

import (
	"fmt"
	"time"
)

// Range 是解析后的时间范围。
type Range struct {
	Key           string        `json:"key"`
	From          time.Time     `json:"from"`           // 已对齐到 Bucket 边界
	To            time.Time     `json:"to"`             // 最后一个时间桶的结束（开区间）
	Anchor        time.Time     `json:"anchor"`         // 最新一条日志的时间
	DataStart     time.Time     `json:"data_start"`     // 最早一条日志的时间
	Bucket        time.Duration `json:"-"`              //
	BucketLabel   string        `json:"bucket"`         // 如 "15m"
	BucketSeconds int64         `json:"bucket_seconds"` //
}

// Presets 是预设的范围；"all" 表示全部数据。其他值按 Go 时长解析（如 "90m"、"48h"）。
var Presets = map[string]time.Duration{
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"all": 0,
}

// buckets 是可选的时间桶大小（从小到大）。
var buckets = []time.Duration{
	time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 3 * time.Hour, 6 * time.Hour, 24 * time.Hour,
}

// MaxPoints 是趋势图期望的最大点数，据此选择时间桶。
const MaxPoints = 120

// PickBucket 返回使 span 内的点数不超过 MaxPoints 的最小时间桶。
func PickBucket(span time.Duration) time.Duration {
	for _, b := range buckets {
		if span/b <= MaxPoints {
			return b
		}
	}
	return buckets[len(buckets)-1]
}

// Duration 解析范围键；"all" 返回 0。
func Duration(key string) (time.Duration, error) {
	if d, ok := Presets[key]; ok {
		return d, nil
	}
	d, err := time.ParseDuration(key)
	if err != nil || d < time.Minute {
		return 0, fmt.Errorf("invalid range %q: use one of 1h, 6h, 24h, 7d, all or a duration >= 1m", key)
	}
	return d, nil
}

// Resolve 计算范围。dataStart / anchor 是最早与最晚一条日志的时间；
// bucket 为 0 时自动选择。起点不早于数据起点所在的桶。
func Resolve(key string, dataStart, anchor time.Time, bucket time.Duration) (Range, error) {
	d, err := Duration(key)
	if err != nil {
		return Range{}, err
	}
	from := dataStart
	if d > 0 {
		from = anchor.Add(-d)
	}
	span := anchor.Sub(from)
	if d > 0 {
		span = d
	}
	if bucket <= 0 {
		bucket = PickBucket(span)
	}
	from = Floor(from, bucket)
	if floor := Floor(dataStart, bucket); from.Before(floor) {
		from = floor // 范围比数据还长：从数据所在的第一个桶开始
	}
	return Range{
		Key: key, From: from, To: Floor(anchor, bucket).Add(bucket),
		Anchor: anchor.UTC(), DataStart: dataStart.UTC(),
		Bucket: bucket, BucketLabel: label(bucket), BucketSeconds: int64(bucket / time.Second),
	}, nil
}

// Floor 把 t 向下取整到 bucket 的整数倍（按 Unix 时间），与数据库中
// floor(extract(epoch FROM ts) / b) * b 的分桶方式一致。
func Floor(t time.Time, bucket time.Duration) time.Time {
	bs := int64(bucket / time.Second)
	if bs <= 0 {
		return t.UTC()
	}
	u := t.Unix()
	return time.Unix(u-((u%bs)+bs)%bs, 0).UTC()
}

// Starts 返回 [From, To) 内每个时间桶的起点。
func (r Range) Starts() []time.Time {
	var out []time.Time
	for t := r.From; t.Before(r.To); t = t.Add(r.Bucket) {
		out = append(out, t)
	}
	return out
}

// Partial 判断起点为 start 的时间桶是否不完整：开始于最早一条日志之前，
// 或结束于最新一条日志之后（尚未结束）。不完整的桶计数偏低，容易被误读为"骤降"。
func (r Range) Partial(start time.Time) bool {
	return start.Before(r.DataStart) || start.Add(r.Bucket).After(r.Anchor)
}

func label(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return d.String()
}
