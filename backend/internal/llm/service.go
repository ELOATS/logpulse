package llm

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/eloats/logpulse/backend/internal/store"
)

// Service 负责收集异常上下文、调用模型并保存结果。
type Service struct {
	Store       *store.Store
	Summarizer  *Summarizer
	Log         *slog.Logger
	SampleLines int
	AutoPerRun  int
}

// Generate 为指定异常生成（或重新生成）分析，并写入数据库。
func (s *Service) Generate(ctx context.Context, id int64) (store.Anomaly, error) {
	a, err := s.Store.GetAnomaly(ctx, id)
	if err != nil {
		return a, err
	}
	in, err := s.collect(ctx, a)
	if err != nil {
		return a, err
	}
	insight, model, genErr := s.Summarizer.Summarize(ctx, in)
	if genErr != nil {
		s.Log.Warn("ai insight failed", "anomaly", id, "err", genErr)
		// 临时性错误不落库，后台下一轮会自动重试；其他错误记录下来避免重复消耗
		if !Retryable(genErr) {
			_ = s.Store.SaveInsight(ctx, id, nil, model, genErr.Error())
		}
		return a, genErr
	}
	if err := s.Store.SaveInsight(ctx, id, insight, model, ""); err != nil {
		return a, err
	}
	return s.Store.GetAnomaly(ctx, id)
}

// AutoGenerate 为待处理的中/高危异常批量生成分析，每轮数量受 AutoPerRun 限制。
func (s *Service) AutoGenerate(ctx context.Context) {
	if s.AutoPerRun <= 0 {
		return
	}
	pending, err := s.Store.PendingInsights(ctx, s.AutoPerRun)
	if err != nil {
		s.Log.Error("list pending insights", "err", err)
		return
	}
	for _, a := range pending {
		if ctx.Err() != nil {
			return
		}
		if _, err := s.Generate(ctx, a.ID); err != nil && Retryable(err) {
			return // 限流或服务不可用时本轮停止，等待下一轮
		}
	}
}

func (s *Service) collect(ctx context.Context, a store.Anomaly) (Input, error) {
	in := Input{Anomaly: a}
	f := store.LogFilter{Service: a.Service, Limit: s.SampleLines}
	from, to := a.WindowStart, a.WindowEnd
	switch a.Kind {
	case "new_pattern":
		if a.PatternID != nil {
			f.PatternID = *a.PatternID
		}
		f.Limit = min(s.SampleLines, 10) // 同一模式的样例高度相似，少量即可
	case "error_spike":
		f.Levels = []string{"ERROR", "FATAL"}
	}
	if f.PatternID == 0 {
		f.From, f.To = &from, &to
	}
	want := f.Limit
	f.Limit = 500 // 多取一些再按模式抽样，避免样例被单一高频模式占满
	page, err := s.Store.SearchLogs(ctx, f)
	if err != nil {
		return in, err
	}
	in.SampleLogs = diverseSample(page.Items, want, 5)

	leadFrom := from.Add(-10 * time.Minute)
	lead, err := s.Store.SearchLogs(ctx, store.LogFilter{
		Service: a.Service, Levels: []string{"WARN", "ERROR", "FATAL"}, From: &leadFrom, To: &from, Limit: 10,
	})
	if err != nil {
		return in, err
	}
	in.LeadUp = lead.Items

	in.Baseline, err = s.Store.Patterns(ctx, store.PatternFilter{Service: a.Service, Sort: "count", Limit: 10})
	return in, err
}

// diverseSample 从按时间倒序的日志中抽样：每个模式最多 perPattern 条，
// 总数不超过 limit，并在时间上均匀分布，最后按时间正序返回。
func diverseSample(rows []store.LogRow, limit, perPattern int) []store.LogRow {
	if limit <= 0 {
		return nil
	}
	groups := map[int64][]store.LogRow{}
	var order []int64
	for _, r := range rows {
		var key int64 = -1
		if r.PatternID != nil {
			key = *r.PatternID
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], r)
	}
	var out []store.LogRow
	for _, k := range order {
		g := groups[k]
		n := min(perPattern, len(g))
		for i := 0; i < n && len(out) < limit; i++ {
			out = append(out, g[i*len(g)/n]) // 等间隔取样，覆盖整个窗口
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out
}
