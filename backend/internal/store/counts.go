package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/eloats/logpulse/backend/internal/parser"
	"github.com/jackc/pgx/v5"
)

// 本文件实现基于 log_counts（按分钟预聚合）的统计查询。
//
// 为保证结果与直接扫描 logs 完全一致：时间范围内的整分钟读 log_counts，
// 首尾不足一分钟的部分读原始日志（走 ts 索引，最多两分钟的数据）。

type countKey struct {
	bucket         time.Time
	service, level string
}

// addCounts 在写入日志的同一事务中累加分钟计数。
// 键按固定顺序写入，并发写入时加锁顺序一致，避免死锁。
func addCounts(ctx context.Context, tx pgx.Tx, entries []parser.Entry) error {
	counts := map[countKey]int64{}
	for _, e := range entries {
		counts[countKey{e.Timestamp.Truncate(time.Minute), e.Service, e.Level}]++
	}
	keys := make([]countKey, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if !a.bucket.Equal(b.bucket) {
			return a.bucket.Before(b.bucket)
		}
		if a.service != b.service {
			return a.service < b.service
		}
		return a.level < b.level
	})
	buckets := make([]time.Time, len(keys))
	services := make([]string, len(keys))
	levels := make([]string, len(keys))
	ns := make([]int64, len(keys))
	for i, k := range keys {
		buckets[i], services[i], levels[i], ns[i] = k.bucket, k.service, k.level, counts[k]
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO log_counts (bucket, service, level, count)
		SELECT * FROM unnest($1::timestamptz[], $2::text[], $3::text[], $4::bigint[])
		ON CONFLICT (bucket, service, level) DO UPDATE SET count = log_counts.count + EXCLUDED.count`,
		buckets, services, levels, ns)
	if err != nil {
		return fmt.Errorf("update log_counts: %w", err)
	}
	return nil
}

func ceilMinute(t time.Time) time.Time {
	if f := t.Truncate(time.Minute); !f.Equal(t) {
		return f.Add(time.Minute)
	}
	return t
}

// countSource 构造一个子查询，输出列 (t, service, level, c)：
// 整分钟部分来自 log_counts（t 为分钟起点，c 为计数），首尾零头来自 logs（t 为日志时间，c = 1）。
func countSource(from, to *time.Time, service string, levels []string, args *[]any) string {
	add := func(v any) string {
		*args = append(*args, v)
		return fmt.Sprintf("$%d", len(*args))
	}
	var conds, rawConds, edges []string
	if service != "" {
		p := add(service)
		conds = append(conds, "service = "+p)
		rawConds = append(rawConds, "service = "+p)
	}
	if len(levels) > 0 {
		p := add(levels)
		conds = append(conds, "level = ANY("+p+")")
		rawConds = append(rawConds, "level = ANY("+p+")")
	}
	switch {
	case from != nil && to != nil && !ceilMinute(*from).Before(to.Truncate(time.Minute)):
		// 范围不足以包含任何整分钟：全部读原始日志
		conds = append(conds, "false")
		edges = append(edges, fmt.Sprintf("(ts >= %s AND ts < %s)", add(*from), add(*to)))
	default:
		if from != nil {
			fc := ceilMinute(*from)
			conds = append(conds, "bucket >= "+add(fc))
			if !fc.Equal(*from) {
				edges = append(edges, fmt.Sprintf("(ts >= %s AND ts < %s)", add(*from), add(fc)))
			}
		}
		if to != nil {
			tf := to.Truncate(time.Minute)
			conds = append(conds, "bucket < "+add(tf))
			if !tf.Equal(*to) {
				edges = append(edges, fmt.Sprintf("(ts >= %s AND ts < %s)", add(tf), add(*to)))
			}
		}
	}
	q := "SELECT bucket AS t, service, level, count AS c FROM log_counts"
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	if len(edges) > 0 {
		q += " UNION ALL SELECT ts, service, level, 1 FROM logs WHERE (" + strings.Join(edges, " OR ") + ")"
		for _, c := range rawConds {
			q += " AND " + c
		}
	}
	return q
}

// Overview 计算总览统计（读预聚合表，耗时与日志总量无关）。
func (s *Store) Overview(ctx context.Context, from, to *time.Time) (Overview, error) {
	o := Overview{Levels: map[string]int64{}, TopServices: []NameCount{}}
	var args []any
	src := countSource(from, to, "", nil, &args)
	rows, err := s.Pool.Query(ctx, `SELECT service, level, sum(c)::bigint FROM (`+src+`) s GROUP BY 1, 2`, args...)
	if err != nil {
		return o, err
	}
	bySvc := map[string]*NameCount{}
	for rows.Next() {
		var svc, lvl string
		var n int64
		if err := rows.Scan(&svc, &lvl, &n); err != nil {
			rows.Close()
			return o, err
		}
		o.TotalLogs += n
		o.Levels[lvl] += n
		nc, ok := bySvc[svc]
		if !ok {
			nc = &NameCount{Name: svc}
			bySvc[svc] = nc
		}
		nc.Count += n
		if lvl == "ERROR" || lvl == "FATAL" {
			o.ErrorLogs += n
			nc.Errors += n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return o, err
	}
	o.Services = int64(len(bySvc))
	if o.TotalLogs > 0 {
		o.ErrorRate = float64(o.ErrorLogs) / float64(o.TotalLogs)
	}
	for _, nc := range bySvc {
		o.TopServices = append(o.TopServices, *nc)
	}
	sort.Slice(o.TopServices, func(i, j int) bool {
		a, b := o.TopServices[i], o.TopServices[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Name < b.Name
	})
	if len(o.TopServices) > 10 {
		o.TopServices = o.TopServices[:10]
	}

	// 最早/最晚时间需精确到毫秒：min/max 走 ts 索引，只读首尾各一行
	where, wargs := LogFilter{From: from, To: to}.where()
	if err := s.Pool.QueryRow(ctx, `SELECT min(ts), max(ts) FROM logs `+where, wargs...).Scan(&o.FirstLog, &o.LastLog); err != nil {
		return o, err
	}
	if err := s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM log_patterns), (SELECT count(*) FROM anomalies WHERE NOT acknowledged)`).
		Scan(&o.Patterns, &o.OpenAnomalies); err != nil {
		return o, err
	}
	return o, nil
}

// usesCounts 判断查询能否由预聚合表回答：只按时间、服务、级别过滤，且时间桶为整分钟。
func usesCounts(f LogFilter, bucket time.Duration) bool {
	return f.Query == "" && f.Host == "" && f.PatternID == 0 && bucket >= time.Minute && bucket%time.Minute == 0
}

// Timeseries 按时间桶聚合日志量；能用预聚合表时读预聚合表，否则扫描原始日志。
func (s *Store) Timeseries(ctx context.Context, f LogFilter, bucket time.Duration) ([]TimePoint, error) {
	if bucket < time.Second {
		bucket = time.Minute
	}
	if !usesCounts(f, bucket) {
		return s.timeseriesRaw(ctx, f, bucket)
	}
	var args []any
	src := countSource(f.From, f.To, f.Service, f.Levels, &args)
	args = append(args, bucket.Seconds())
	b := fmt.Sprintf("$%d", len(args))
	rows, err := s.Pool.Query(ctx, `SELECT to_timestamp(floor(extract(epoch FROM t) / `+b+`) * `+b+`) AS b,
			sum(c)::bigint,
			coalesce(sum(c) FILTER (WHERE level IN ('ERROR','FATAL')), 0)::bigint,
			coalesce(sum(c) FILTER (WHERE level = 'WARN'), 0)::bigint
		FROM (`+src+`) s GROUP BY b ORDER BY b`, args...)
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

// SeriesRow 是异常检测用的 (服务, 桶序号) 计数。
type SeriesRow struct {
	Service       string
	Index         int
	Total, Errors int64
}

// CountSeries 返回 [from, to) 内每个服务、每个时间桶的日志量与错误数，桶序号从 from 起算。
func (s *Store) CountSeries(ctx context.Context, from, to time.Time, bucket time.Duration) ([]SeriesRow, error) {
	var args []any
	var src string
	if bucket >= time.Minute && bucket%time.Minute == 0 {
		src = countSource(&from, &to, "", nil, &args)
	} else {
		args = append(args, from, to)
		src = "SELECT ts AS t, service, level, 1 AS c FROM logs WHERE ts >= $1 AND ts < $2"
	}
	args = append(args, from, bucket.Seconds())
	f, b := fmt.Sprintf("$%d", len(args)-1), fmt.Sprintf("$%d", len(args))
	rows, err := s.Pool.Query(ctx, `SELECT service,
			floor(extract(epoch FROM t - `+f+`::timestamptz) / `+b+`)::int AS idx,
			sum(c)::bigint,
			coalesce(sum(c) FILTER (WHERE level IN ('ERROR','FATAL')), 0)::bigint
		FROM (`+src+`) s GROUP BY 1, 2`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SeriesRow
	for rows.Next() {
		var r SeriesRow
		if err := rows.Scan(&r.Service, &r.Index, &r.Total, &r.Errors); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
