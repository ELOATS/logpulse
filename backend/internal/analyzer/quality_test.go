package analyzer

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"strings"
	"testing"
)

// 本文件用模拟数据评估检测器质量：误报率（每条序列每天的误报次数）与故障检出率。
//
// 模拟的序列：每分钟计数服从 Poisson，速率带日周期（±30%）和过度离散（Gamma 混合），
// 接近真实日志"有周期、比 Poisson 更抖"的特点。

const day = 1440

func poisson(r *rand.Rand, lambda float64) float64 {
	if lambda <= 0 {
		return 0
	}
	if lambda > 50 { // 大均值用正态近似
		return math.Max(0, math.Round(r.NormFloat64()*math.Sqrt(lambda)+lambda))
	}
	l, k, p := math.Exp(-lambda), 0.0, 1.0
	for {
		p *= r.Float64()
		if p <= l {
			return k
		}
		k++
	}
}

// gamma 采样（Marsaglia-Tsang），shape >= 1
func gamma(r *rand.Rand, shape float64) float64 {
	d := shape - 1.0/3
	c := 1 / math.Sqrt(9*d)
	for {
		x := r.NormFloat64()
		v := 1 + c*x
		if v <= 0 {
			continue
		}
		v = v * v * v
		if u := r.Float64(); math.Log(u) < 0.5*x*x+d-d*v+d*math.Log(v) {
			return d * v
		}
	}
}

// simulate 生成一天的序列；dispersion 越小越"抖"（0 表示纯 Poisson）
func simulate(r *rand.Rand, base, dispersion float64) ([]float64, []float64) {
	s, rate := make([]float64, day), make([]float64, day)
	phase := r.Float64() * 2 * math.Pi
	for i := range s {
		lam := base * (1 + 0.3*math.Sin(2*math.Pi*float64(i)/day+phase))
		if dispersion > 0 {
			lam *= gamma(r, dispersion) / dispersion
		}
		rate[i] = lam
		s[i] = poisson(r, lam)
	}
	return s, rate
}

type detector func([]float64, int, float64, float64) []Spike

type quality struct {
	fpPerDay float64
	recall   map[float64]float64 // 故障强度（倍数）→ 检出率
}

func evaluate(det detector, threshold, base, dispersion float64, days int, seed int64, baseline int) quality {
	r := rand.New(rand.NewSource(seed))
	q := quality{recall: map[float64]float64{}}
	fps := 0
	for d := 0; d < days; d++ {
		s, _ := simulate(r, base, dispersion)
		fps += len(MergeSpikes(det(s, baseline, threshold, 5), 2))
	}
	q.fpPerDay = float64(fps) / float64(days)
	// 故障：连续 5 分钟，计数变为平时的 m 倍（且至少多出 5 条，模拟低流量服务的小规模故障）
	for _, m := range []float64{2, 3, 5, 10} {
		hit := 0
		for d := 0; d < days; d++ {
			s, rate := simulate(r, base, dispersion)
			at := 300 + r.Intn(day-400)
			for i := at; i < at+5; i++ {
				extra := math.Max(rate[i]*(m-1), 5)
				s[i] += poisson(r, extra)
			}
			for _, sp := range det(s, baseline, threshold, 5) {
				if sp.Index >= at && sp.Index < at+5 {
					hit++
					break
				}
			}
		}
		q.recall[m] = float64(hit) / float64(days)
	}
	return q
}

// detectSpikesLegacy 是改进前的算法（中位数 + MAD 的 z-score），仅用于对比。
func detectSpikesLegacy(series []float64, baseline int, threshold float64, minCount float64) []Spike {
	var spikes []Spike
	for i := range series {
		start := i - baseline
		if start < 0 {
			if i < baseline/2 {
				continue
			}
			start = 0
		}
		x := series[i]
		if x < minCount {
			continue
		}
		w := series[start:i]
		med := median(w)
		sigma := math.Max(1.4826*medianAbsDev(w, med), math.Max(1, math.Sqrt(med)))
		if z := (x - med) / sigma; z >= threshold {
			spikes = append(spikes, Spike{Index: i, Observed: x, Expected: med, Score: z})
		}
	}
	return spikes
}

var scenarios = []struct {
	name       string
	base, disp float64
}{
	{"极低流量 0.5/分", 0.5, 0},
	{"低流量 2/分", 2, 0},
	{"中流量 20/分", 20, 0},
	{"中流量 20/分（抖动大）", 20, 8},
	{"高流量 400/分", 400, 0},
	{"高流量 400/分（抖动大）", 400, 20},
}

func report(t *testing.T, name string, det detector, threshold float64, baseline int) map[string]quality {
	out := map[string]quality{}
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s（阈值 %.1f，基线 %d 分钟）\n%-24s %10s %8s %8s %8s %8s\n", name, threshold, baseline, "场景", "误报/天", "2倍", "3倍", "5倍", "10倍")
	for i, sc := range scenarios {
		q := evaluate(det, threshold, sc.base, sc.disp, 60, int64(100+i), baseline)
		out[sc.name] = q
		fmt.Fprintf(&b, "%-24s %10.2f %7.0f%% %7.0f%% %7.0f%% %7.0f%%\n", sc.name, q.fpPerDay,
			q.recall[2]*100, q.recall[3]*100, q.recall[5]*100, q.recall[10]*100)
	}
	t.Log(b.String())
	return out
}

func TestDetectorQuality(t *testing.T) {
	if testing.Short() {
		t.Skip("质量评估较慢，-short 时跳过")
	}
	report(t, "旧算法", detectSpikesLegacy, 3, 30)
	// 参数扫描：LOGPULSE_DETECTOR_SWEEP=1 go test ./internal/analyzer -run Quality -v
	if os.Getenv("LOGPULSE_DETECTOR_SWEEP") != "" {
		for _, th := range []float64{4, 4.5, 5, 5.5} {
			for _, bl := range []int{30, 60, 120} {
				report(t, "新算法", DetectSpikes, th, bl)
			}
		}
	}
	// 默认参数（与 config 中的默认值一致）
	got := report(t, "新算法", DetectSpikes, 5, 60)
	for name, q := range got {
		// 每条序列每天平均误报不超过 0.1 次（5 个服务 × 2 项指标 ≈ 每天 1 次以内）
		if q.fpPerDay > 0.1 {
			t.Errorf("%s: 误报 %.2f/天，超过 0.1", name, q.fpPerDay)
		}
		// 5 倍故障必须稳定检出；3 倍故障大部分检出。
		// 低流量场景下"至少多 5 条/分钟"的下限使 2 倍、3 倍故障强度相同（约 7 条/分钟，
		// 基线 2 条/分钟），在每天几乎零误报的约束下已接近统计上可检出的极限
		if q.recall[5] < 0.95 {
			t.Errorf("%s: 5 倍故障检出率 %.0f%%，低于 95%%", name, q.recall[5]*100)
		}
		if q.recall[3] < 0.7 {
			t.Errorf("%s: 3 倍故障检出率 %.0f%%，低于 70%%", name, q.recall[3]*100)
		}
	}
}
