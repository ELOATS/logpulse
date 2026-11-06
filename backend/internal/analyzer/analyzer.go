package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

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

	// AfterRun 在每轮检测成功后调用（例如生成大模型摘要），可为空。
	AfterRun func(ctx context.Context)
}

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
	var minTS, maxTS *time.Time
	if err := a.store.Pool.QueryRow(ctx, `SELECT min(ts), max(ts) FROM logs`).Scan(&minTS, &maxTS); err != nil {
		return res, err
	}
	if maxTS == nil {
		return res, nil
	}
	b := a.opts.Bucket
	to := maxTS.Truncate(b).Add(b)
	from := to.Add(-a.opts.Lookback)
	if minTS.Truncate(b).After(from) {
		from = minTS.Truncate(b)
	}
	res.From, res.To = from, to

	series, err := a.loadSeries(ctx, from, to)
	if err != nil {
		return res, err
	}
	res.Services = len(series)
	n := int(to.Sub(from) / b)

	for service, s := range series {
		for _, check := range []struct {
			kind   string
			values []float64
			label  string
		}{
			{"error_spike", s.errors, "错误日志"},
			{"volume_spike", s.total, "日志量"},
		} {
			spikes := DetectSpikes(check.values[:n], a.opts.BaselineBuckets, a.opts.ZThreshold, float64(a.opts.MinCount))
			for _, inc := range MergeSpikes(spikes, 2) {
				inc.Recount(check.values[:n])
				ws, we := from.Add(time.Duration(inc.Start)*b), from.Add(time.Duration(inc.End)*b)
				an := store.Anomaly{
					Kind: check.kind, Severity: Severity(inc.Score, a.opts.ZThreshold), Service: service,
					WindowStart: ws, WindowEnd: we, Score: inc.Score, Observed: inc.Observed, Expected: inc.Expected,
					Description: fmt.Sprintf("服务 %s 的%s在 %s ~ %s 期间突增：共 %.0f 条，基线约 %.0f 条（峰值 z=%.1f）",
						displayService(service), check.label, ws.Format("01-02 15:04"), we.Format("15:04"), inc.Observed, inc.Expected, inc.Score),
				}
				onlyErrors := check.kind == "error_spike"
				top, err := a.topPatterns(ctx, service, ws, we, onlyErrors)
				if err != nil {
					return res, err
				}
				an.Details, _ = json.Marshal(map[string]any{"top_patterns": top})
				key := fmt.Sprintf("%s:%s:%d", check.kind, service, ws.Unix())
				inserted, err := a.store.UpsertAnomaly(ctx, key, an)
				if err != nil {
					return res, err
				}
				res.Detected++
				if inserted {
					res.NewAnomalies++
				}
			}
		}
	}

	// 新出现的错误/告警模式：只在系统已有足够历史之后才算"新"
	warmup := minTS.Add(time.Duration(a.opts.BaselineBuckets) * b)
	newPatterns, err := a.newPatterns(ctx, maxTime(from, warmup))
	if err != nil {
		return res, err
	}
	for _, p := range newPatterns {
		sev := "medium"
		switch p.Level {
		case "FATAL":
			sev = "high"
		case "WARN":
			sev = "low"
		}
		pid := p.ID
		an := store.Anomaly{
			Kind: "new_pattern", Severity: sev, Service: p.Service, PatternID: &pid,
			WindowStart: p.FirstSeen, WindowEnd: p.LastSeen, Observed: float64(p.Count), Score: float64(p.Count),
			Description: fmt.Sprintf("服务 %s 首次出现新的 %s 日志模式：%s", displayService(p.Service), p.Level, p.Template),
		}
		an.Details, _ = json.Marshal(map[string]any{"template": p.Template, "sample": p.Sample, "count": p.Count})
		inserted, err := a.store.UpsertAnomaly(ctx, fmt.Sprintf("new_pattern:%d", p.ID), an)
		if err != nil {
			return res, err
		}
		res.Detected++
		if inserted {
			res.NewAnomalies++
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
// interval <= 0 时只响应 Trigger。
func (a *Analyzer) Loop(ctx context.Context, interval time.Duration) {
	var tick <-chan time.Time
	if interval > 0 {
		t := time.NewTicker(interval)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
		case <-a.trigger:
		}
		{
			res, err := a.Run(ctx)
			if err != nil {
				a.log.Error("analyze failed", "err", err)
				continue
			}
			if res.NewAnomalies > 0 {
				a.log.Info("anomalies detected", "new", res.NewAnomalies, "total", res.Detected)
			}
			if a.AfterRun != nil {
				a.AfterRun(ctx)
			}
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

type topPattern struct {
	PatternID int64  `json:"pattern_id"`
	Template  string `json:"template"`
	Level     string `json:"level"`
	Count     int64  `json:"count"`
}

// topPatterns 给出异常窗口内占比最高的日志模式，作为根因线索。
func (a *Analyzer) topPatterns(ctx context.Context, service string, from, to time.Time, onlyErrors bool) ([]topPattern, error) {
	levelCond := ""
	if onlyErrors {
		levelCond = "AND l.level IN ('ERROR','FATAL')"
	}
	rows, err := a.store.Pool.Query(ctx, `
		SELECT p.id, p.template, p.level, count(*)
		FROM logs l JOIN log_patterns p ON p.id = l.pattern_id
		WHERE l.service = $1 AND l.ts >= $2 AND l.ts < $3 `+levelCond+`
		GROUP BY p.id, p.template, p.level ORDER BY count(*) DESC LIMIT 5`, service, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []topPattern{}
	for rows.Next() {
		var t topPattern
		if err := rows.Scan(&t.PatternID, &t.Template, &t.Level, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (a *Analyzer) newPatterns(ctx context.Context, since time.Time) ([]store.Pattern, error) {
	rows, err := a.store.Pool.Query(ctx, `
		SELECT id, service, level, template, sample, count, first_seen, last_seen
		FROM log_patterns WHERE level IN ('WARN','ERROR','FATAL') AND first_seen >= $1
		ORDER BY first_seen DESC LIMIT 200`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Pattern
	for rows.Next() {
		var p store.Pattern
		if err := rows.Scan(&p.ID, &p.Service, &p.Level, &p.Template, &p.Sample, &p.Count, &p.FirstSeen, &p.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FirstSeen.Before(out[j].FirstSeen) })
	return out, rows.Err()
}

func displayService(s string) string {
	if s == "" {
		return "(未命名)"
	}
	return s
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
