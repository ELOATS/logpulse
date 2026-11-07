// Package analyzer 实现日志异常检测。
package analyzer

import (
	"math"
)

// Spike 是检测到的突增点。
type Spike struct {
	Index    int
	Observed float64
	Expected float64
	Score    float64 // 等效 z 值：与观测值同样罕见的正态分布 z 值
}

// DetectSpikes 在滑动基线上检测计数突增。
//
//   - baseline：用于计算基线的前序桶数量
//   - threshold：等效 z 值阈值（5 约等于单侧 p = 2.9e-7）
//   - minCount：观测值低于该值时忽略（避免极低流量下的零星事件）
//
// 日志计数是"事件数"，低流量时随机波动服从 Poisson 分布，远比正态分布的尾部重；
// 直接用 (x-均值)/标准差 会在低流量下产生大量误报。因此这里：
//  1. 用剔除离群点后的线性趋势估计基线（历史尖峰不会抬高基线，日周期也跟得上）；
//  2. 方差不显著高于均值时按 Poisson 计算尾部概率 P(X ≥ x)，过度离散（比 Poisson
//     更"抖"）时按同均值、同方差的负二项分布计算；
//  3. 把尾部概率换算成等效 z 值，使阈值与严重级别仍可按 z 值理解；
//  4. 同时检验 1、3、5 个桶的总和，发现"每个桶都不算太高、但持续了好几分钟"的故障。
func DetectSpikes(series []float64, baseline int, threshold float64, minCount float64) []Spike {
	if baseline < 3 {
		baseline = 3
	}
	// 基线只取决于被检验区间的起点 from：k=3 在第 i 桶的基线就是 k=1 在第 i-2 桶的基线，
	// 按起点缓存，每个起点只拟合一次
	fits := make([]baselineFit, len(series))
	fitted := make([]bool, len(series))
	fitAt := func(from int) (baselineFit, int) {
		w := series[max(from-baseline, 0):from]
		if !fitted[from] {
			fits[from], fitted[from] = fitBaseline(w), true
		}
		return fits[from], len(w)
	}
	var spikes []Spike
	for i := range series {
		x := math.Round(series[i])
		best := Spike{Index: i, Observed: x}
		for _, k := range scales {
			from := i - k + 1 // 本次检验覆盖的桶 [from, i]
			if from < 0 || from-baseline < -baseline/2 {
				continue // 历史不足一半基线时不做判断，防止冷启动误报
			}
			fit, n := fitAt(from)
			if k > 1 && x <= fit.predict(n+k-1) {
				break // 当前桶已恢复正常：不再用多桶窗口延长已结束的故障
			}
			// 预测被检验的 k 个桶的基线（沿趋势外推），并按 k 个桶求和
			var mean, variance float64
			for j := 0; j < k; j++ {
				m := fit.predict(n + j)
				mean += m
				variance += math.Max(fit.variance, m) // 方差不低于 Poisson 方差
			}
			// 多桶窗口：把窗口中最大的桶压到第二大的值再求和。孤立的尖峰会被"削平"
			// （它由单桶检验负责），而持续升高时各桶相近，几乎不受影响
			sum, top1, top2 := 0.0, 0.0, 0.0
			for j := from; j <= i; j++ {
				v := math.Round(series[j])
				sum += v
				if v > top1 {
					top1, top2 = v, top1
				} else if v > top2 {
					top2 = v
				}
			}
			if k > 1 {
				sum -= top1 - top2
			}
			if sum < minCount || sum <= mean {
				continue
			}
			if z := tailZ(sum, mean, variance); z > best.Score {
				best.Score, best.Expected = z, mean/float64(k)
			}
		}
		if best.Score >= threshold {
			spikes = append(spikes, best)
		}
	}
	return spikes
}

// scales 是检验的时间尺度（连续桶数）。
var scales = []int{1, 3, 5}

// 基线均值的下限：从未出现过的事件突然出现若干次，也按"平均每桶 0.25 次"评估，
// 避免均值为 0 时任何一次出现都被判为无限罕见。
const minMean = 0.25

// baselineFit 是基线窗口上的稳健线性趋势：y ≈ a + b·t。
type baselineFit struct {
	a, b     float64
	variance float64 // 残差方差（每桶）
	lo, hi   float64 // 预测值的上下限，防止趋势外推过度
}

func countTrue(b []bool) float64 {
	n := 0.0
	for _, v := range b {
		if v {
			n++
		}
	}
	return n
}

func (f baselineFit) predict(t int) float64 {
	return math.Min(math.Max(f.a+f.b*float64(t), f.lo), f.hi)
}

// fitBaseline 用带离群点剔除的最小二乘拟合趋势：反复剔除明显高于趋势的点
// （历史尖峰、上一次故障）后重新拟合。与窗口均值相比，它能跟上日周期等缓慢变化，
// 不会因为"平均值滞后于当前水平"而产生误报。
func fitBaseline(w []float64) baselineFit {
	n := len(w)
	keep := make([]bool, n)
	for i := range keep {
		keep[i] = true
	}
	var f baselineFit
	for iter := 0; iter < 4; iter++ {
		var sn, st, sy, stt, sty float64
		for i, y := range w {
			if keep[i] {
				t := float64(i)
				sn++
				st += t
				sy += y
				stt += t * t
				sty += t * y
			}
		}
		if den := sn*stt - st*st; sn >= 3 && den > 0 {
			f.b = (sn*sty - st*sy) / den
		} else {
			f.b = 0
		}
		f.a = (sy - f.b*st) / math.Max(sn, 1)
		var ss float64
		for i, y := range w {
			if keep[i] {
				r := y - (f.a + f.b*float64(i))
				ss += r * r
			}
		}
		f.variance = ss / math.Max(sn-2, 1)
		// 斜率不显著（< 2.5 倍标准误）时视为没有趋势：低流量下斜率估计噪声很大，
		// 外推会让基线忽高忽低；日周期等真实趋势在高流量下是显著的，仍会被采用
		if sxx := stt - st*st/math.Max(sn, 1); sxx > 0 && math.Abs(f.b) < 2.5*math.Sqrt(math.Max(f.variance, sy/math.Max(sn, 1))/sxx) {
			f.b = 0
			f.a = sy / math.Max(sn, 1)
			var ss0 float64
			for i, y := range w {
				if keep[i] {
					ss0 += (y - f.a) * (y - f.a)
				}
			}
			f.variance = ss0 / math.Max(sn-1, 1)
		}
		// 只剔除向上的离群点；+1 使极低计数（如 0/1/2）不被误剔
		cut := 3*math.Sqrt(math.Max(f.variance, f.a+f.b*float64(n)/2)) + 1
		changed := false
		for i, y := range w {
			if keep[i] && y-(f.a+f.b*float64(i)) > cut {
				keep[i], changed = false, true
			}
		}
		if !changed {
			break
		}
	}
	// 离散度检验：在 Poisson 下 (n-1)·方差/均值 ~ χ²(n-1)。只有方差显著高于均值
	// （过度离散）时才采用估计的方差；否则差异只是抽样噪声，按 Poisson 处理，
	// 避免低流量下因方差估计偏高而降低检出率
	m, sn := math.Max(f.a+f.b*float64(n)/2, minMean), countTrue(keep)
	if sn > 2 && f.variance/m < 1+2.5*math.Sqrt(2/(sn-1)) {
		f.variance = 0 // 使用时取 max(variance, mean)，即 Poisson 方差
	}
	f.lo, f.hi = minMean, minMean
	for i, y := range w {
		if keep[i] && y > f.hi {
			f.hi = y
		}
	}
	return f
}

// tailZ 计算 P(X ≥ x) 并换算为等效 z 值。
func tailZ(x, mean, variance float64) float64 {
	var logTail float64
	if variance <= mean*1.05 {
		logTail = poissonLogTail(x, mean)
	} else {
		logTail = negBinomLogTail(x, mean, variance)
	}
	return zFromLogTail(logTail)
}

// poissonLogTail 返回 ln P(X ≥ x)，X ~ Poisson(λ)。
func poissonLogTail(x, lambda float64) float64 {
	lg, _ := math.Lgamma(x + 1)
	logP := x*math.Log(lambda) - lambda - lg
	return logP + logSumRatio(x, func(k float64) float64 { return lambda / (k + 1) })
}

// negBinomLogTail 返回 ln P(X ≥ x)，X 为均值 m、方差 v 的负二项分布
// （等价于速率服从 Gamma 分布的 Poisson，恰好刻画"比 Poisson 更抖"的计数）。
func negBinomLogTail(x, m, v float64) float64 {
	r := m * m / (v - m) // 形状参数
	p := m / v           // 成功概率
	lgxr, _ := math.Lgamma(x + r)
	lgr, _ := math.Lgamma(r)
	lgx, _ := math.Lgamma(x + 1)
	logP := lgxr - lgr - lgx + r*math.Log(p) + x*math.Log1p(-p)
	return logP + logSumRatio(x, func(k float64) float64 { return (k + r) / (k + 1) * (1 - p) })
}

// logSumRatio 计算 ln Σ_{k≥x} pmf(k)/pmf(x)，ratio(k) = pmf(k+1)/pmf(k)。
// 只在 x 位于均值右侧时调用，此时各项单调递减，求和很快收敛。
func logSumRatio(x float64, ratio func(float64) float64) float64 {
	sum, term := 1.0, 1.0
	for k := x; k < x+100000; k++ {
		term *= ratio(k)
		sum += term
		if term < sum*1e-12 {
			break
		}
	}
	return math.Log(sum)
}

// zFromLogTail 把单侧尾部概率（对数）换算为标准正态的等效 z 值。
func zFromLogTail(logTail float64) float64 {
	if logTail >= 0 {
		return 0
	}
	if logTail > math.Log(1e-10) {
		return math.Sqrt2 * math.Erfcinv(2*math.Exp(logTail))
	}
	// 极小概率时用渐近式 Q(z) ≈ φ(z)/z 迭代求解，避免浮点下溢
	z := math.Sqrt(-2 * logTail)
	for i := 0; i < 5; i++ {
		z = math.Sqrt(-2 * (logTail + math.Log(z) + 0.5*math.Log(2*math.Pi)))
	}
	return z
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

// Incident 是若干个相邻突增桶合并后的事件。
type Incident struct {
	Start, End int     // 桶区间 [Start, End)
	Observed   float64 // 区间内观测总数
	Expected   float64 // 区间内基线总数
	Score      float64 // 区间内最大 z-score
	Spikes     int     // 区间内突增桶的数量
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
			cur.Spikes++
			continue
		}
		out = append(out, Incident{Start: s.Index, End: s.Index + 1, Observed: s.Observed, Expected: s.Expected, Score: s.Score, Spikes: 1})
	}
	return out
}

// Recount 用完整区间重新统计观测值与基线：合并时被跨过的间隔桶也计入，
// 使数字与区间内的日志实际数量一致（基线按突增桶的平均基线外推到整个区间）。
func (inc *Incident) Recount(series []float64) {
	inc.Observed = 0
	for i := inc.Start; i < inc.End && i < len(series); i++ {
		inc.Observed += series[i]
	}
	if inc.Spikes > 0 {
		inc.Expected = inc.Expected / float64(inc.Spikes) * float64(inc.End-inc.Start)
	}
}
