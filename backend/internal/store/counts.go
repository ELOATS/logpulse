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
func countSource(b *sqlBuilder, from, to *time.Time, service string, levels []string) string {
	var conds, rawConds, edges []string
	if service != "" {
		p := b.arg(service)
		conds = append(conds, "service = "+p)
		rawConds = append(rawConds, "service = "+p)
	}
	if len(levels) > 0 {
		p := b.arg(levels)
		conds = append(conds, "level = ANY("+p+")")
		rawConds = append(rawConds, "level = ANY("+p+")")
	}
	switch {
	case from != nil && to != nil && !ceilMinute(*from).Before(to.Truncate(time.Minute)):
		// 范围不足以包含任何整分钟：全部读原始日志
		conds = append(conds, "false")
		edges = append(edges, b.cond("(ts >= ? AND ts < ?)", *from, *to))
	default:
		if from != nil {
			fc := ceilMinute(*from)
			conds = append(conds, b.cond("bucket >= ?", fc))
			if !fc.Equal(*from) {
				edges = append(edges, b.cond("(ts >= ? AND ts < ?)", *from, fc))
			}
		}
		if to != nil {
			tf := to.Truncate(time.Minute)
			conds = append(conds, b.cond("bucket < ?", tf))
			if !tf.Equal(*to) {
				edges = append(edges, b.cond("(ts >= ? AND ts < ?)", tf, *to))
			}
		}
	}
	q := "SELECT bucket AS t, service, level, count AS c FROM log_counts " + where(conds)
	if len(edges) > 0 {
		q += " UNION ALL SELECT ts, service, level, 1 FROM logs WHERE (" + strings.Join(edges, " OR ") + ")"
		for _, c := range rawConds {
			q += " AND " + c
		}
	}
	return q
}

// rawSource 是与 countSource 同形状 (t, service, level, c) 的原始日志子查询。
func rawSource(b *sqlBuilder, f LogFilter) string {
	return "SELECT ts AS t, service, level, 1 AS c FROM logs " + f.build(b)
}

// wholeMinutes 判断时间桶是否为整分钟（才能由按分钟预聚合的计数回答）。
func wholeMinutes(bucket time.Duration) bool {
	return bucket >= time.Minute && bucket%time.Minute == 0
}

// usesCounts 判断查询能否由预聚合表回答：只按时间、服务、级别过滤，且时间桶为整分钟。
func usesCounts(f LogFilter, bucket time.Duration) bool {
	return f.Query == "" && f.Host == "" && f.PatternID == 0 && wholeMinutes(bucket)
}

// Overview 计算总览统计（读预聚合表，耗时与日志总量无关）。三个查询在一次往返中完成。
func (s *Store) Overview(ctx context.Context, from, to *time.Time) (Overview, error) {
	o := Overview{Levels: map[string]int64{}, TopServices: []NameCount{}}
	bySvc := map[string]*NameCount{}

	var cb sqlBuilder
	src := countSource(&cb, from, to, "", nil)
	// 最早/最晚时间需精确到毫秒：min/max 走 ts 索引，只读首尾各一行
	rangeWhere, rangeArgs := LogFilter{From: from, To: to}.where()

	batch := &pgx.Batch{}
	batch.Queue(`SELECT service, level, sum(c)::bigint FROM (`+src+`) s GROUP BY 1, 2`, cb.args...).Query(func(rows pgx.Rows) error {
		var svc, lvl string
		var n int64
		_, err := pgx.ForEachRow(rows, []any{&svc, &lvl, &n}, func() error {
			o.TotalLogs += n
			o.Levels[lvl] += n
			nc, ok := bySvc[svc]
			if !ok {
				nc = &NameCount{Name: svc}
				bySvc[svc] = nc
			}
			nc.Count += n
			if parser.IsError(lvl) {
				o.ErrorLogs += n
				nc.Errors += n
			}
			return nil
		})
		return err
	})
	batch.Queue(`SELECT min(ts), max(ts) FROM logs `+rangeWhere, rangeArgs...).QueryRow(func(r pgx.Row) error {
		return r.Scan(&o.FirstLog, &o.LastLog)
	})
	batch.Queue(`SELECT (SELECT count(*) FROM log_patterns), (SELECT count(*) FROM anomalies WHERE NOT acknowledged)`).QueryRow(func(r pgx.Row) error {
		return r.Scan(&o.Patterns, &o.OpenAnomalies)
	})
	if err := s.Pool.SendBatch(ctx, batch).Close(); err != nil {
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
	return o, nil
}

// Timeseries 按时间桶聚合日志量；能用预聚合表时读预聚合表，否则扫描原始日志。
func (s *Store) Timeseries(ctx context.Context, f LogFilter, bucket time.Duration) ([]TimePoint, error) {
	return s.timeseries(ctx, f, bucket, usesCounts(f, bucket))
}

func (s *Store) timeseries(ctx context.Context, f LogFilter, bucket time.Duration, fromCounts bool) ([]TimePoint, error) {
	if bucket < time.Second {
		bucket = time.Minute
	}
	var b sqlBuilder
	src := rawSource(&b, f)
	if fromCounts {
		b = sqlBuilder{}
		src = countSource(&b, f.From, f.To, f.Service, f.Levels)
	}
	bs := b.arg(bucket.Seconds())
	rows, err := s.Pool.Query(ctx, `SELECT to_timestamp(floor(extract(epoch FROM t) / `+bs+`) * `+bs+`) AS b,
			sum(c)::bigint,
			coalesce(sum(c) FILTER (WHERE `+errorLevelSQL+`), 0)::bigint,
			coalesce(sum(c) FILTER (WHERE level = 'WARN'), 0)::bigint
		FROM (`+src+`) s GROUP BY b ORDER BY b`, b.args...)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[TimePoint])
	for i := range out {
		out[i].Bucket = out[i].Bucket.UTC()
	}
	return out, err
}

// SeriesRow 是异常检测用的 (服务, 桶序号) 计数。
type SeriesRow struct {
	Service       string
	Index         int
	Total, Errors int64
}

// CountSeries 返回 [from, to) 内每个服务、每个时间桶的日志量与错误数，桶序号从 from 起算。
func (s *Store) CountSeries(ctx context.Context, from, to time.Time, bucket time.Duration) ([]SeriesRow, error) {
	var b sqlBuilder
	var src string
	if wholeMinutes(bucket) {
		src = countSource(&b, &from, &to, "", nil)
	} else {
		src = rawSource(&b, LogFilter{From: &from, To: &to})
	}
	f, bs := b.arg(from), b.arg(bucket.Seconds())
	rows, err := s.Pool.Query(ctx, `SELECT service,
			floor(extract(epoch FROM t - `+f+`::timestamptz) / `+bs+`)::int AS idx,
			sum(c)::bigint,
			coalesce(sum(c) FILTER (WHERE `+errorLevelSQL+`), 0)::bigint
		FROM (`+src+`) s GROUP BY 1, 2`, b.args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[SeriesRow])
}
