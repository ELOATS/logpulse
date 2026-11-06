package store

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"
)

// logs 表按天（UTC）分区，分区名为 logs_pYYYYMMDD。

const partitionPrefix = "logs_p"

var partitionName = regexp.MustCompile(`^logs_p(\d{8})$`)

// partitionCache 记录已确认存在的分区，避免每次写入都查询系统表。
type partitionCache struct {
	mu   sync.Mutex
	days map[string]bool
}

func dayOf(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// ensurePartitions 确保给定时间涉及的每一天都有分区。
// 建分区在独立的短事务中进行，并用 advisory lock 串行化，避免并发写入时重复创建。
func (s *Store) ensurePartitions(ctx context.Context, times []time.Time) error {
	need := map[string]time.Time{}
	s.parts.mu.Lock()
	if s.parts.days == nil {
		s.parts.days = map[string]bool{}
	}
	for _, t := range times {
		d := dayOf(t)
		key := d.Format("20060102")
		if !s.parts.days[key] {
			need[key] = d
		}
	}
	s.parts.mu.Unlock()
	if len(need) == 0 {
		return nil
	}
	keys := make([]string, 0, len(need))
	for k := range need {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('logpulse_partitions'))`); err != nil {
		return err
	}
	for _, k := range keys {
		d := need[k]
		_, err := tx.Exec(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s%s PARTITION OF logs FOR VALUES FROM ('%s') TO ('%s')`,
			partitionPrefix, k, d.Format(time.RFC3339), d.AddDate(0, 0, 1).Format(time.RFC3339)))
		if err != nil {
			return fmt.Errorf("create partition %s: %w", k, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.parts.mu.Lock()
	for _, k := range keys {
		s.parts.days[k] = true
	}
	s.parts.mu.Unlock()
	return nil
}

// Partition 描述一个日志分区。
type Partition struct {
	Name string    `json:"name"`
	Day  time.Time `json:"day"`
}

// Partitions 列出 logs 的所有分区（按日期升序）。
func (s *Store) Partitions(ctx context.Context) ([]Partition, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT c.relname FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		JOIN pg_class p ON p.oid = i.inhparent
		WHERE p.relname = 'logs' AND p.relnamespace = to_regnamespace(current_schema())`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Partition
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		m := partitionName.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		d, err := time.Parse("20060102", m[1])
		if err != nil {
			continue
		}
		out = append(out, Partition{Name: name, Day: d})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	return out, rows.Err()
}

// RetentionResult 汇总一次数据清理。
type RetentionResult struct {
	Cutoff            time.Time `json:"cutoff"`
	DroppedPartitions []string  `json:"dropped_partitions"`
	DeletedCounts     int64     `json:"deleted_counts"`
	DeletedPatterns   int64     `json:"deleted_patterns"`
	DeletedAnomalies  int64     `json:"deleted_anomalies"`
}

// ApplyRetention 删除早于 cutoff 所在日期的整天数据：删除对应分区，并清理同期的分钟计数、
// 不再有日志的模式（其异常随之级联删除）以及更早的异常。
// 以天为粒度：cutoff 当天的数据会保留到第二天。
func (s *Store) ApplyRetention(ctx context.Context, cutoff time.Time) (RetentionResult, error) {
	res := RetentionResult{Cutoff: dayOf(cutoff), DroppedPartitions: []string{}}
	parts, err := s.Partitions(ctx)
	if err != nil {
		return res, err
	}
	for _, p := range parts {
		if !p.Day.AddDate(0, 0, 1).After(res.Cutoff) { // 整个分区都早于 cutoff
			if _, err := s.Pool.Exec(ctx, `DROP TABLE IF EXISTS `+p.Name); err != nil {
				return res, fmt.Errorf("drop %s: %w", p.Name, err)
			}
			res.DroppedPartitions = append(res.DroppedPartitions, p.Name)
			s.parts.mu.Lock()
			delete(s.parts.days, p.Day.Format("20060102"))
			s.parts.mu.Unlock()
		}
	}
	tag, err := s.Pool.Exec(ctx, `DELETE FROM log_counts WHERE bucket < $1`, res.Cutoff)
	if err != nil {
		return res, err
	}
	res.DeletedCounts = tag.RowsAffected()
	tag, err = s.Pool.Exec(ctx, `DELETE FROM anomalies WHERE window_end < $1`, res.Cutoff)
	if err != nil {
		return res, err
	}
	res.DeletedAnomalies = tag.RowsAffected()
	// 最近出现时间早于 cutoff 的模式已没有任何日志
	tag, err = s.Pool.Exec(ctx, `DELETE FROM log_patterns WHERE last_seen < $1`, res.Cutoff)
	if err != nil {
		return res, err
	}
	res.DeletedPatterns = tag.RowsAffected()
	return res, nil
}
