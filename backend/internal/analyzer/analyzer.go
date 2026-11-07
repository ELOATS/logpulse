package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sync/atomic"
	"time"

	"github.com/eloats/logpulse/backend/internal/parser"
	"github.com/eloats/logpulse/backend/internal/store"
)

// Options 是异常检测参数。
type Options struct {
	Bucket          time.Duration
	BaselineBuckets int
	ZThreshold      float64
	MinCount        int
	Lookback        time.Duration
}

type Analyzer struct {
	store   *store.Store
	opts    Options
	log     *slog.Logger
	trigger chan struct{}

	// AfterRun 在每轮检测成功后异步调用（例如生成大模型摘要），可为空。
	// 不阻塞下一轮检测；上一次尚未结束时跳过本次。
	AfterRun     func(ctx context.Context)
	afterRunning atomic.Bool
}

// minTriggerGap 是由写入触发的两次检测之间的最小间隔：持续写入时，每个请求都会
// Trigger，但没必要每个请求都重新分析一遍整个回溯窗口。
const minTriggerGap = 10 * time.Second

func New(s *store.Store, opts Options, log *slog.Logger) *Analyzer {
	if opts.Bucket <= 0 {
		opts.Bucket = time.Minute
	}
	if opts.BaselineBuckets <= 0 {
		opts.BaselineBuckets = 60
	}
	if opts.ZThreshold <= 0 {
		opts.ZThreshold = 5
	}
	if opts.Lookback <= 0 {
		opts.Lookback = 24 * time.Hour
	}
	return &Analyzer{store: s, opts: opts, log: log, trigger: make(chan struct{}, 1)}
}

// Result 汇总一次分析运行。
type Result struct {
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	Services     int       `json:"services"`
	Detected     int       `json:"detected"`
	NewAnomalies int       `json:"new_anomalies"`
	DurationMS   int64     `json:"duration_ms"`
}

// Run 在 [最新日志时间 - Lookback, 最新日志时间] 范围内执行检测。
// 以数据中的时间而非当前时间为准，这样导入的历史日志同样可以被分析。
func (a *Analyzer) Run(ctx context.Context) (Result, error) {
	started := time.Now()
	var res Result
	minTS, maxTS, err := a.store.DataRange(ctx)
	if err != nil || maxTS == nil {
		return res, err
	}
	b := a.opts.Bucket
	to := maxTS.Truncate(b).Add(b)
	from := to.Add(-a.opts.Lookback)
	if minTS.Truncate(b).After(from) {
		from = minTS.Truncate(b)
	}
	res.From, res.To = from, to

	record := func(key string, an store.Anomaly) error {
		inserted, err := a.store.UpsertAnomaly(ctx, key, an)
		if err != nil {
			return err
		}
		res.Detected++
		if inserted {
			res.NewAnomalies++
		}
		return nil
	}

	series, err := a.loadSeries(ctx, from, to)
	if err != nil {
		return res, err
	}
	res.Services = len(series)
	for service, s := range series {
		for _, check := range []struct {
			kind   string
			values []float64
			label  string
			levels []string // 根因线索只统计这些级别（nil 表示全部）
		}{
			{"error_spike", s.errors, "错误日志", parser.ErrorLevels},
			{"volume_spike", s.total, "日志量", nil},
		} {
			spikes := DetectSpikes(check.values, a.opts.BaselineBuckets, a.opts.ZThreshold, float64(a.opts.MinCount))
			for _, inc := range MergeSpikes(spikes, 2) {
				inc.Recount(check.values)
				ws, we := from.Add(time.Duration(inc.Start)*b), from.Add(time.Duration(inc.End)*b)
				top, err := a.store.TopPatterns(ctx, service, ws, we, check.levels, 5)
				if err != nil {
					return res, err
				}
				an := store.Anomaly{
					Kind: check.kind, Severity: Severity(inc.Score, a.opts.ZThreshold), Service: service,
					WindowStart: ws, WindowEnd: we, Score: inc.Score, Observed: inc.Observed, Expected: inc.Expected,
					Description: fmt.Sprintf("服务 %s 的%s在 %s ~ %s 期间突增：共 %.0f 条，基线约 %.0f 条（峰值 z=%.1f）",
						parser.DisplayService(service), check.label, ws.Format("01-02 15:04"), we.Format("15:04"), inc.Observed, inc.Expected, inc.Score),
				}
				an.Details, _ = json.Marshal(map[string]any{"top_patterns": top})
				if err := record(fmt.Sprintf("%s:%s:%d", check.kind, service, ws.Unix()), an); err != nil {
					return res, err
				}
			}
		}
	}

	// 新出现的错误/告警模式：只在系统已有足够历史之后才算"新"
	since := maxTime(from, minTS.Add(time.Duration(a.opts.BaselineBuckets)*b))
	newPatterns, err := a.store.Patterns(ctx, store.PatternFilter{Levels: parser.AlertLevels, FirstSeenSince: &since, Sort: "new", Limit: 200})
	if err != nil {
		return res, err
	}
	slices.Reverse(newPatterns) // 按首次出现时间正序处理
	for _, p := range newPatterns {
		sev := map[string]string{"FATAL": "high", "WARN": "low"}[p.Level]
		if sev == "" {
			sev = "medium"
		}
		pid := p.ID
		an := store.Anomaly{
			Kind: "new_pattern", Severity: sev, Service: p.Service, PatternID: &pid,
			WindowStart: p.FirstSeen, WindowEnd: p.LastSeen, Observed: float64(p.Count), Score: float64(p.Count),
			Description: fmt.Sprintf("服务 %s 首次出现新的 %s 日志模式：%s", parser.DisplayService(p.Service), p.Level, p.Template),
		}
		an.Details, _ = json.Marshal(map[string]any{"template": p.Template, "sample": p.Sample, "count": p.Count})
		if err := record(fmt.Sprintf("new_pattern:%d", p.ID), an); err != nil {
			return res, err
		}
	}
	res.DurationMS = time.Since(started).Milliseconds()
	return res, nil
}

// Trigger 请求尽快执行一次检测（非阻塞，多次请求会合并）。
func (a *Analyzer) Trigger() {
	select {
	case a.trigger <- struct{}{}:
	default:
	}
}

// Loop 周期性（以及被 Trigger 时）运行检测，直到 ctx 结束。
// interval <= 0 时只响应 Trigger；由 Trigger 引发的检测之间至少间隔 minTriggerGap。
func (a *Analyzer) Loop(ctx context.Context, interval time.Duration) {
	var tick <-chan time.Time
	if interval > 0 {
		t := time.NewTicker(interval)
		defer t.Stop()
		tick = t.C
	}
	var lastRun time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
		case <-a.trigger:
			if wait := minTriggerGap - time.Since(lastRun); wait > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
			}
		}
		lastRun = time.Now()
		res, err := a.Run(ctx)
		if err != nil {
			a.log.Error("analyze failed", "err", err)
			continue
		}
		if res.NewAnomalies > 0 {
			a.log.Info("anomalies detected", "new", res.NewAnomalies, "total", res.Detected)
		}
		if a.AfterRun != nil && a.afterRunning.CompareAndSwap(false, true) {
			go func() {
				defer a.afterRunning.Store(false)
				a.AfterRun(ctx)
			}()
		}
	}
}

type serviceSeries struct{ total, errors []float64 }

func (a *Analyzer) loadSeries(ctx context.Context, from, to time.Time) (map[string]*serviceSeries, error) {
	n := int(to.Sub(from) / a.opts.Bucket)
	rows, err := a.store.CountSeries(ctx, from, to, a.opts.Bucket)
	if err != nil {
		return nil, err
	}
	out := map[string]*serviceSeries{}
	for _, r := range rows {
		s, ok := out[r.Service]
		if !ok {
			s = &serviceSeries{total: make([]float64, n), errors: make([]float64, n)}
			out[r.Service] = s
		}
		if r.Index >= 0 && r.Index < n {
			s.total[r.Index] = float64(r.Total)
			s.errors[r.Index] = float64(r.Errors)
		}
	}
	return out, nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
