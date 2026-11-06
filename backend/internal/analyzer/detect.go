// Package analyzer 实现日志异常检测。
package analyzer

import (
	"math"
	"sort"
)

// Point 是某个时间桶的计数。
type Point struct {
	Index int     // 桶序号
	Value float64 // 计数
}

// Spike 是检测到的突增点。
type Spike struct {
	Index    int
	Observed float64
	Expected float64
	Score    float64 // 鲁棒 z-score
}

// DetectSpikes 使用滑动基线 + 鲁棒 z-score（中位数 / MAD）检测突增。
//
//   - baseline：用于计算基线的前序桶数量
//   - threshold：z-score 阈值
//   - minCount：观测值低于该值时忽略（避免低流量噪声）
//
// 与均值/标准差相比，中位数与 MAD 对历史中已存在的尖峰不敏感，误报更少。
func DetectSpikes(series []float64, baseline int, threshold float64, minCount float64) []Spike {
	if baseline < 3 {
		baseline = 3
	}
	var spikes []Spike
	window := make([]float64, 0, baseline)
	for i := range series {
		start := i - baseline
		if start < 0 {
			// 历史不足一半基线时不做判断，防止冷启动误报
			if i < baseline/2 {
				continue
			}
			start = 0
		}
		window = append(window[:0], series[start:i]...)
		x := series[i]
		if x < minCount {
			continue
		}
		med := median(window)
		mad := medianAbsDev(window, med)
		// 1.4826 使 MAD 在正态分布下与标准差可比；加下限避免平稳序列除以 0
		sigma := math.Max(1.4826*mad, math.Max(1, math.Sqrt(med)))
		z := (x - med) / sigma
		if z >= threshold {
			spikes = append(spikes, Spike{Index: i, Observed: x, Expected: med, Score: z})
		}
	}
	return spikes
}

// Severity 根据 z-score 给出严重级别。
func Severity(score, threshold float64) string {
	switch {
	case score >= threshold*2:
		return "high"
	case score >= threshold*1.5:
		return "medium"
	}
	return "low"
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func medianAbsDev(xs []float64, med float64) float64 {
	d := make([]float64, len(xs))
	for i, x := range xs {
		d[i] = math.Abs(x - med)
	}
	return median(d)
}

// Incident 是若干个相邻突增桶合并后的事件。
type Incident struct {
	Start, End int     // 桶区间 [Start, End)
	Observed   float64 // 区间内观测总数
	Expected   float64 // 区间内基线总数
	Score      float64 // 区间内最大 z-score
}

// MergeSpikes 将间隔不超过 gap 个桶的突增合并为一个事件，避免同一故障产生大量告警。
func MergeSpikes(spikes []Spike, gap int) []Incident {
	var out []Incident
	for _, s := range spikes {
		if n := len(out); n > 0 && s.Index-out[n-1].End <= gap {
			cur := &out[n-1]
			cur.End = s.Index + 1
			cur.Observed += s.Observed
			cur.Expected += s.Expected
			cur.Score = math.Max(cur.Score, s.Score)
			continue
		}
		out = append(out, Incident{Start: s.Index, End: s.Index + 1, Observed: s.Observed, Expected: s.Expected, Score: s.Score})
	}
	return out
}
