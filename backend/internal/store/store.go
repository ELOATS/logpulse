// Package store 封装所有 PostgreSQL 读写操作。
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/eloats/logpulse/backend/internal/parser"
	"github.com/eloats/logpulse/backend/internal/pattern"
	"github.com/eloats/logpulse/backend/internal/textutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	Pool  *pgxpool.Pool
	parts partitionCache
}

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

// ---------- 写入 ----------

type IngestResult struct {
	Accepted    int `json:"accepted"`
	NewPatterns int `json:"new_patterns"`
}

type patternAgg struct {
	service, level, template, sample string
	count                            int64
	first, last                      time.Time
}

// Ingest 批量写入日志：先聚合并 upsert 模式，再用 COPY 高速写入日志行。
func (s *Store) Ingest(ctx context.Context, entries []parser.Entry) (IngestResult, error) {
	if len(entries) == 0 {
		return IngestResult{}, nil
	}
	hashes := make([]string, len(entries))
	aggs := map[string]*patternAgg{}
	order := []string{}
	for i, e := range entries {
		tpl := pattern.Template(e.Message)
		h := pattern.Hash(e.Service, e.Level, tpl)
		hashes[i] = h
		a, ok := aggs[h]
		if !ok {
			a = &patternAgg{service: e.Service, level: e.Level, template: tpl, sample: textutil.Truncate(e.Message, 1000), first: e.Timestamp, last: e.Timestamp}
			aggs[h] = a
			order = append(order, h)
		}
		a.count++
		if e.Timestamp.Before(a.first) {
			a.first = e.Timestamp
		}
		if e.Timestamp.After(a.last) {
			a.last = e.Timestamp
		}
	}

	times := make([]time.Time, len(entries))
	for i, e := range entries {
		times[i] = e.Timestamp
	}
	if err := s.ensurePartitions(ctx, times); err != nil {
		return IngestResult{}, err
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return IngestResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	ids := make(map[string]int64, len(aggs))
	res := IngestResult{Accepted: len(entries)}
	batch := &pgx.Batch{}
	for _, h := range order {
		a := aggs[h]
		batch.Queue(`
			INSERT INTO log_patterns (hash, service, level, template, sample, count, first_seen, last_seen)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (hash) DO UPDATE SET
				count = log_patterns.count + EXCLUDED.count,
				first_seen = LEAST(log_patterns.first_seen, EXCLUDED.first_seen),
				last_seen = GREATEST(log_patterns.last_seen, EXCLUDED.last_seen)
			RETURNING id, (xmax = 0) AS inserted`,
			h, a.service, a.level, a.template, a.sample, a.count, a.first, a.last)
	}
	br := tx.SendBatch(ctx, batch)
	for _, h := range order {
		var id int64
		var inserted bool
		if err := br.QueryRow().Scan(&id, &inserted); err != nil {
			br.Close()
			return IngestResult{}, fmt.Errorf("upsert pattern: %w", err)
		}
		ids[h] = id
		if inserted {
			res.NewPatterns++
		}
	}
	if err := br.Close(); err != nil {
		return IngestResult{}, err
	}

	if err := addCounts(ctx, tx, entries); err != nil {
		return IngestResult{}, err
	}

	rows := make([][]any, len(entries))
	for i, e := range entries {
		attrs, err := json.Marshal(e.Attributes)
		if err != nil {
			attrs = []byte("{}")
		}
		rows[i] = []any{e.Timestamp, e.Level, e.Service, e.Host, e.Message, e.Raw, e.Format, string(attrs), ids[hashes[i]]}
	}
	_, err = tx.CopyFrom(ctx, pgx.Identifier{"logs"},
		[]string{"ts", "level", "service", "host", "message", "raw", "format", "attributes", "pattern_id"},
		pgx.CopyFromRows(rows))
	if err != nil {
		return IngestResult{}, fmt.Errorf("copy logs: %w", err)
	}
	return res, tx.Commit(ctx)
}

// ---------- 查询日志 ----------

type LogFilter struct {
	Query     string
	Levels    []string
	Service   string
	Host      string
	PatternID int64
	From, To  *time.Time
	Limit     int
	Offset    int
	SkipCount bool // 不统计总数（只需要样例时）
}

type LogRow struct {
	ID         int64           `json:"id"`
	Timestamp  time.Time       `json:"timestamp"`
	Level      string          `json:"level"`
	Service    string          `json:"service"`
	Host       string          `json:"host"`
	Message    string          `json:"message"`
	Raw        string          `json:"raw"`
	Format     string          `json:"format"`
	Attributes json.RawMessage `json:"attributes"`
	PatternID  *int64          `json:"pattern_id"`
}

type LogPage struct {
	Items []LogRow `json:"items"`
	Total int64    `json:"total"`
	// TotalCapped 为 true 表示匹配数超过计数上限，Total 是下限（显示为"10,000+"）
	TotalCapped bool `json:"total_capped"`
}

// countCap 是检索结果计数的上限：精确统计大量匹配需要扫描全部匹配行，
// 而界面只需要知道"有很多"。翻页超过上限时会相应提高。
const countCap = 10000

// where 返回 WHERE 子句与参数。
func (f LogFilter) where() (string, []any) {
	var b sqlBuilder
	w := f.build(&b)
	return w, b.args
}

// build 用 b 编号参数，返回 WHERE 子句（可与其他使用同一 b 的片段拼接）。
func (f LogFilter) build(b *sqlBuilder) string {
	var conds []string
	if q := strings.TrimSpace(f.Query); q != "" {
		// 全文检索 + 子串匹配兜底（全文检索对符号、路径等不敏感）
		conds = append(conds, b.cond("(search @@ websearch_to_tsquery('simple', ?) OR message ILIKE ?)", q, "%"+escapeLike(q)+"%"))
	}
	if len(f.Levels) > 0 {
		conds = append(conds, b.cond("level = ANY(?)", f.Levels))
	}
	if f.Service != "" {
		conds = append(conds, b.cond("service = ?", f.Service))
	}
	if f.Host != "" {
		conds = append(conds, b.cond("host = ?", f.Host))
	}
	if f.PatternID > 0 {
		conds = append(conds, b.cond("pattern_id = ?", f.PatternID))
	}
	if f.From != nil {
		conds = append(conds, b.cond("ts >= ?", *f.From))
	}
	if f.To != nil {
		conds = append(conds, b.cond("ts < ?", *f.To))
	}
	return where(conds)
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (s *Store) SearchLogs(ctx context.Context, f LogFilter) (LogPage, error) {
	f.Limit = clampLimit(f.Limit, 100, 500)
	var b sqlBuilder
	w := f.build(&b)
	page := LogPage{Items: []LogRow{}}
	limit := int64(max(countCap, f.Offset+f.Limit))
	countSQL := fmt.Sprintf("SELECT count(*) FROM (SELECT 1 FROM logs %s LIMIT %d) t", w, limit+1)
	pageSQL := fmt.Sprintf(`SELECT id, ts, level, service, host, message, raw, format, attributes, pattern_id
		FROM logs %s ORDER BY ts DESC, id DESC LIMIT %d OFFSET %d`, w, f.Limit, f.Offset)

	batch := &pgx.Batch{}
	if !f.SkipCount {
		batch.Queue(countSQL, b.args...).QueryRow(func(r pgx.Row) error { return r.Scan(&page.Total) })
	}
	batch.Queue(pageSQL, b.args...).Query(func(rows pgx.Rows) error {
		var err error
		page.Items, err = pgx.CollectRows(rows, pgx.RowToStructByPos[LogRow])
		return err
	})
	if err := s.Pool.SendBatch(ctx, batch).Close(); err != nil {
		return page, err
	}
	if page.Total > limit {
		page.Total, page.TotalCapped = limit, true
	}
	return page, nil
}

// DataRange 返回最早与最晚一条日志的时间（没有日志时为 nil）。
func (s *Store) DataRange(ctx context.Context) (minTS, maxTS *time.Time, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT min(ts), max(ts) FROM logs`).Scan(&minTS, &maxTS)
	return minTS, maxTS, err
}

// ---------- 统计 ----------

type Overview struct {
	TotalLogs     int64            `json:"total_logs"`
	ErrorLogs     int64            `json:"error_logs"`
	ErrorRate     float64          `json:"error_rate"`
	Patterns      int64            `json:"patterns"`
	OpenAnomalies int64            `json:"open_anomalies"`
	Services      int64            `json:"services"`
	FirstLog      *time.Time       `json:"first_log"`
	LastLog       *time.Time       `json:"last_log"`
	Levels        map[string]int64 `json:"levels"`
	TopServices   []NameCount      `json:"top_services"`
}

type NameCount struct {
	Name   string `json:"name"`
	Count  int64  `json:"count"`
	Errors int64  `json:"errors"`
}

type TimePoint struct {
	Bucket time.Time `json:"bucket"`
	Total  int64     `json:"total"`
	Errors int64     `json:"errors"`
	Warns  int64     `json:"warns"`
}

func (s *Store) Services(ctx context.Context) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT service FROM log_patterns ORDER BY service`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// ---------- 模式 ----------

type Pattern struct {
	ID        int64     `json:"id"`
	Service   string    `json:"service"`
	Level     string    `json:"level"`
	Template  string    `json:"template"`
	Sample    string    `json:"sample"`
	Count     int64     `json:"count"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

type PatternFilter struct {
	Service        string
	Level          string
	Levels         []string
	Query          string
	FirstSeenSince *time.Time
	Sort           string // count | recent | new
	Limit          int
}

func (s *Store) Patterns(ctx context.Context, f PatternFilter) ([]Pattern, error) {
	var b sqlBuilder
	var conds []string
	if f.Service != "" {
		conds = append(conds, b.cond("service = ?", f.Service))
	}
	if f.Level != "" {
		conds = append(conds, b.cond("level = ?", f.Level))
	}
	if len(f.Levels) > 0 {
		conds = append(conds, b.cond("level = ANY(?)", f.Levels))
	}
	if f.Query != "" {
		like := b.arg("%" + escapeLike(f.Query) + "%")
		conds = append(conds, "(template ILIKE "+like+" OR sample ILIKE "+like+")")
	}
	if f.FirstSeenSince != nil {
		conds = append(conds, b.cond("first_seen >= ?", *f.FirstSeenSince))
	}
	order := map[string]string{"recent": "last_seen DESC", "new": "first_seen DESC"}[f.Sort]
	if order == "" {
		order = "count DESC"
	}
	rows, err := s.Pool.Query(ctx, fmt.Sprintf(`SELECT id, service, level, template, sample, count, first_seen, last_seen
		FROM log_patterns %s ORDER BY %s, id LIMIT %d`, where(conds), order, clampLimit(f.Limit, 100, 500)), b.args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Pattern])
}

// PatternCount 是某个时间窗口内一个模式的日志数。
type PatternCount struct {
	PatternID int64  `json:"pattern_id"`
	Template  string `json:"template"`
	Level     string `json:"level"`
	Count     int64  `json:"count"`
}

// TopPatterns 返回服务在 [from, to) 内日志数最多的模式（levels 为空表示所有级别），作为根因线索。
func (s *Store) TopPatterns(ctx context.Context, service string, from, to time.Time, levels []string, limit int) ([]PatternCount, error) {
	var b sqlBuilder
	conds := []string{b.cond("l.service = ?", service), b.cond("l.ts >= ?", from), b.cond("l.ts < ?", to)}
	if len(levels) > 0 {
		conds = append(conds, b.cond("l.level = ANY(?)", levels))
	}
	rows, err := s.Pool.Query(ctx, fmt.Sprintf(`
		SELECT p.id, p.template, p.level, count(*)
		FROM logs l JOIN log_patterns p ON p.id = l.pattern_id
		%s GROUP BY p.id, p.template, p.level ORDER BY count(*) DESC LIMIT %d`, where(conds), limit), b.args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[PatternCount])
}

// ---------- 异常 ----------

type Anomaly struct {
	ID           int64           `json:"id"`
	Kind         string          `json:"kind"`
	Severity     string          `json:"severity"`
	Service      string          `json:"service"`
	PatternID    *int64          `json:"pattern_id"`
	WindowStart  time.Time       `json:"window_start"`
	WindowEnd    time.Time       `json:"window_end"`
	Score        float64         `json:"score"`
	Observed     float64         `json:"observed"`
	Expected     float64         `json:"expected"`
	Description  string          `json:"description"`
	Details      json.RawMessage `json:"details"`
	Acknowledged bool            `json:"acknowledged"`
	CreatedAt    time.Time       `json:"created_at"`

	AIInsight     json.RawMessage `json:"ai_insight"`
	AIModel       *string         `json:"ai_model"`
	AIError       *string         `json:"ai_error"`
	AIGeneratedAt *time.Time      `json:"ai_generated_at"`
}

const anomalyColumns = `id, kind, severity, service, pattern_id, window_start, window_end,
	score, observed, expected, description, details, acknowledged, created_at,
	ai_insight, ai_model, ai_error, ai_generated_at`

// queryAnomalies 执行返回 anomalyColumns 的查询（列顺序与 Anomaly 字段顺序一致）。
func (s *Store) queryAnomalies(ctx context.Context, sql string, args ...any) ([]Anomaly, error) {
	rows, err := s.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Anomaly])
}

// UpsertAnomaly 按 dedup_key 去重写入，返回是否为新异常。
func (s *Store) UpsertAnomaly(ctx context.Context, key string, a Anomaly) (bool, error) {
	if len(a.Details) == 0 {
		a.Details = json.RawMessage("{}")
	}
	var inserted bool
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO anomalies (dedup_key, kind, severity, service, pattern_id, window_start, window_end,
			score, observed, expected, description, details)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (dedup_key) DO UPDATE SET
			severity = EXCLUDED.severity, score = EXCLUDED.score, observed = EXCLUDED.observed,
			expected = EXCLUDED.expected, description = EXCLUDED.description, details = EXCLUDED.details
		-- 每轮检测都会重新写入回溯窗口内的所有异常；内容没变时不更新，避免无谓的行版本与表膨胀
		WHERE (anomalies.severity, anomalies.score, anomalies.observed, anomalies.expected, anomalies.description, anomalies.details)
			IS DISTINCT FROM (EXCLUDED.severity, EXCLUDED.score, EXCLUDED.observed, EXCLUDED.expected, EXCLUDED.description, EXCLUDED.details)
		RETURNING (xmax = 0)`,
		key, a.Kind, a.Severity, a.Service, a.PatternID, a.WindowStart, a.WindowEnd,
		a.Score, a.Observed, a.Expected, a.Description, string(a.Details)).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) { // 内容未变，未更新
		return false, nil
	}
	return inserted, err
}

type AnomalyFilter struct {
	Kind         string
	Severity     string
	Service      string
	Acknowledged *bool
	Sort         string // time（默认）| severity
	Limit        int
}

func (s *Store) Anomalies(ctx context.Context, f AnomalyFilter) ([]Anomaly, error) {
	var b sqlBuilder
	var conds []string
	for _, c := range []struct{ col, v string }{{"kind", f.Kind}, {"severity", f.Severity}, {"service", f.Service}} {
		if c.v != "" {
			conds = append(conds, b.cond(c.col+" = ?", c.v))
		}
	}
	if f.Acknowledged != nil {
		conds = append(conds, b.cond("acknowledged = ?", *f.Acknowledged))
	}
	order := "window_start DESC, score DESC"
	if f.Sort == "severity" {
		order = "CASE severity WHEN 'high' THEN 0 WHEN 'medium' THEN 1 ELSE 2 END, " + order
	}
	return s.queryAnomalies(ctx, fmt.Sprintf(`SELECT %s FROM anomalies %s ORDER BY %s LIMIT %d`,
		anomalyColumns, where(conds), order, clampLimit(f.Limit, 100, 500)), b.args...)
}

func (s *Store) GetAnomaly(ctx context.Context, id int64) (Anomaly, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+anomalyColumns+` FROM anomalies WHERE id = $1`, id)
	if err != nil {
		return Anomaly{}, err
	}
	return pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[Anomaly])
}

// SaveInsight 保存大模型分析结果；errMsg 非空表示生成失败。
func (s *Store) SaveInsight(ctx context.Context, id int64, insight any, model, errMsg string) error {
	var raw []byte
	if insight != nil {
		var err error
		if raw, err = json.Marshal(insight); err != nil {
			return err
		}
	}
	_, err := s.Pool.Exec(ctx, `UPDATE anomalies SET ai_insight = $2, ai_model = NULLIF($3, ''),
		ai_error = NULLIF($4, ''), ai_generated_at = now() WHERE id = $1`, id, raw, model, errMsg)
	return err
}

// PendingInsights 返回尚未生成（也未失败过）分析的未确认中/高危异常，严重者优先。
func (s *Store) PendingInsights(ctx context.Context, limit int) ([]Anomaly, error) {
	return s.queryAnomalies(ctx, `SELECT `+anomalyColumns+` FROM anomalies
		WHERE ai_generated_at IS NULL AND NOT acknowledged AND severity IN ('medium', 'high')
		ORDER BY (severity = 'high') DESC, window_start DESC LIMIT $1`, limit)
}

func (s *Store) AckAnomaly(ctx context.Context, id int64, ack bool) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE anomalies SET acknowledged = $2 WHERE id = $1`, id, ack)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
