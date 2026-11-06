// Package store 封装所有 PostgreSQL 读写操作。
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/eloats/logpulse/backend/internal/parser"
	"github.com/eloats/logpulse/backend/internal/pattern"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	Pool *pgxpool.Pool
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
			a = &patternAgg{service: e.Service, level: e.Level, template: tpl, sample: truncate(e.Message, 1000), first: e.Timestamp, last: e.Timestamp}
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
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
}

func (f LogFilter) where() (string, []any) {
	var conds []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, strings.ReplaceAll(cond, "?", fmt.Sprintf("$%d", len(args))))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		// 全文检索 + 子串匹配兜底（全文检索对符号、路径等不敏感）
		args = append(args, q, "%"+escapeLike(q)+"%")
		n := len(args)
		conds = append(conds, fmt.Sprintf("(search @@ websearch_to_tsquery('simple', $%d) OR message ILIKE $%d)", n-1, n))
	}
	if len(f.Levels) > 0 {
		add("level = ANY(?)", f.Levels)
	}
	if f.Service != "" {
		add("service = ?", f.Service)
	}
	if f.Host != "" {
		add("host = ?", f.Host)
	}
	if f.PatternID > 0 {
		add("pattern_id = ?", f.PatternID)
	}
	if f.From != nil {
		add("ts >= ?", *f.From)
	}
	if f.To != nil {
		add("ts < ?", *f.To)
	}
	if len(conds) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (s *Store) SearchLogs(ctx context.Context, f LogFilter) (LogPage, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	where, args := f.where()
	page := LogPage{Items: []LogRow{}}
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM logs "+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	args = append(args, f.Limit, f.Offset)
	q := fmt.Sprintf(`SELECT id, ts, level, service, host, message, raw, format, attributes, pattern_id
		FROM logs %s ORDER BY ts DESC, id DESC LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args))
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var r LogRow
		if err := rows.Scan(&r.ID, &r.Timestamp, &r.Level, &r.Service, &r.Host, &r.Message, &r.Raw, &r.Format, &r.Attributes, &r.PatternID); err != nil {
			return page, err
		}
		page.Items = append(page.Items, r)
	}
	return page, rows.Err()
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

func (s *Store) Overview(ctx context.Context, from, to *time.Time) (Overview, error) {
	o := Overview{Levels: map[string]int64{}, TopServices: []NameCount{}}
	where, args := LogFilter{From: from, To: to}.where()
	err := s.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE level IN ('ERROR','FATAL')),
		count(DISTINCT service), min(ts), max(ts) FROM logs `+where, args...).
		Scan(&o.TotalLogs, &o.ErrorLogs, &o.Services, &o.FirstLog, &o.LastLog)
	if err != nil {
		return o, err
	}
	if o.TotalLogs > 0 {
		o.ErrorRate = float64(o.ErrorLogs) / float64(o.TotalLogs)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM log_patterns`).Scan(&o.Patterns); err != nil {
		return o, err
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM anomalies WHERE NOT acknowledged`).Scan(&o.OpenAnomalies); err != nil {
		return o, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT level, count(*) FROM logs `+where+` GROUP BY level`, args...)
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var l string
		var c int64
		if err := rows.Scan(&l, &c); err != nil {
			rows.Close()
			return o, err
		}
		o.Levels[l] = c
	}
	rows.Close()
	rows, err = s.Pool.Query(ctx, `SELECT service, count(*), count(*) FILTER (WHERE level IN ('ERROR','FATAL'))
		FROM logs `+where+` GROUP BY service ORDER BY count(*) DESC LIMIT 10`, args...)
	if err != nil {
		return o, err
	}
	defer rows.Close()
	for rows.Next() {
		var n NameCount
		if err := rows.Scan(&n.Name, &n.Count, &n.Errors); err != nil {
			return o, err
		}
		o.TopServices = append(o.TopServices, n)
	}
	return o, rows.Err()
}

type TimePoint struct {
	Bucket time.Time `json:"bucket"`
	Total  int64     `json:"total"`
	Errors int64     `json:"errors"`
	Warns  int64     `json:"warns"`
}

// Timeseries 按时间桶聚合日志量。from/to 为空时使用数据的实际时间范围。
func (s *Store) Timeseries(ctx context.Context, f LogFilter, bucket time.Duration) ([]TimePoint, error) {
	if bucket < time.Second {
		bucket = time.Minute
	}
	where, args := f.where()
	args = append(args, bucket.Seconds())
	n := len(args)
	q := fmt.Sprintf(`SELECT to_timestamp(floor(extract(epoch FROM ts) / $%d) * $%d) AS b,
			count(*),
			count(*) FILTER (WHERE level IN ('ERROR','FATAL')),
			count(*) FILTER (WHERE level = 'WARN')
		FROM logs %s GROUP BY b ORDER BY b`, n, n, where)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TimePoint{}
	for rows.Next() {
		var p TimePoint
		if err := rows.Scan(&p.Bucket, &p.Total, &p.Errors, &p.Warns); err != nil {
			return nil, err
		}
		p.Bucket = p.Bucket.UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Services(ctx context.Context) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT service FROM log_patterns ORDER BY service`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var sv string
		if err := rows.Scan(&sv); err != nil {
			return nil, err
		}
		out = append(out, sv)
	}
	return out, rows.Err()
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
	Service string
	Level   string
	Query   string
	Sort    string // count | recent | new
	Limit   int
}

func (s *Store) Patterns(ctx context.Context, f PatternFilter) ([]Pattern, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	var conds []string
	var args []any
	if f.Service != "" {
		args = append(args, f.Service)
		conds = append(conds, fmt.Sprintf("service = $%d", len(args)))
	}
	if f.Level != "" {
		args = append(args, f.Level)
		conds = append(conds, fmt.Sprintf("level = $%d", len(args)))
	}
	if f.Query != "" {
		args = append(args, "%"+escapeLike(f.Query)+"%")
		conds = append(conds, fmt.Sprintf("(template ILIKE $%d OR sample ILIKE $%d)", len(args), len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	order := "count DESC"
	switch f.Sort {
	case "recent":
		order = "last_seen DESC"
	case "new":
		order = "first_seen DESC"
	}
	args = append(args, f.Limit)
	rows, err := s.Pool.Query(ctx, fmt.Sprintf(`SELECT id, service, level, template, sample, count, first_seen, last_seen
		FROM log_patterns %s ORDER BY %s, id LIMIT $%d`, where, order, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Pattern{}
	for rows.Next() {
		var p Pattern
		if err := rows.Scan(&p.ID, &p.Service, &p.Level, &p.Template, &p.Sample, &p.Count, &p.FirstSeen, &p.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
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

func scanAnomaly(row pgx.Row) (Anomaly, error) {
	var a Anomaly
	err := row.Scan(&a.ID, &a.Kind, &a.Severity, &a.Service, &a.PatternID, &a.WindowStart, &a.WindowEnd,
		&a.Score, &a.Observed, &a.Expected, &a.Description, &a.Details, &a.Acknowledged, &a.CreatedAt,
		&a.AIInsight, &a.AIModel, &a.AIError, &a.AIGeneratedAt)
	return a, err
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
		RETURNING (xmax = 0)`,
		key, a.Kind, a.Severity, a.Service, a.PatternID, a.WindowStart, a.WindowEnd,
		a.Score, a.Observed, a.Expected, a.Description, string(a.Details)).Scan(&inserted)
	return inserted, err
}

type AnomalyFilter struct {
	Kind         string
	Severity     string
	Service      string
	Acknowledged *bool
	Limit        int
}

func (s *Store) Anomalies(ctx context.Context, f AnomalyFilter) ([]Anomaly, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	var conds []string
	var args []any
	add := func(col string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if f.Kind != "" {
		add("kind", f.Kind)
	}
	if f.Severity != "" {
		add("severity", f.Severity)
	}
	if f.Service != "" {
		add("service", f.Service)
	}
	if f.Acknowledged != nil {
		add("acknowledged", *f.Acknowledged)
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	args = append(args, f.Limit)
	rows, err := s.Pool.Query(ctx, fmt.Sprintf(`SELECT %s FROM anomalies %s
		ORDER BY window_start DESC, score DESC LIMIT $%d`, anomalyColumns, where, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Anomaly{}
	for rows.Next() {
		a, err := scanAnomaly(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetAnomaly(ctx context.Context, id int64) (Anomaly, error) {
	return scanAnomaly(s.Pool.QueryRow(ctx, `SELECT `+anomalyColumns+` FROM anomalies WHERE id = $1`, id))
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
	rows, err := s.Pool.Query(ctx, `SELECT `+anomalyColumns+` FROM anomalies
		WHERE ai_generated_at IS NULL AND NOT acknowledged AND severity IN ('medium', 'high')
		ORDER BY (severity = 'high') DESC, window_start DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Anomaly
	for rows.Next() {
		a, err := scanAnomaly(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
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
