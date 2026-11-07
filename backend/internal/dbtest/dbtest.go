// Package dbtest 为集成测试提供隔离的 PostgreSQL schema。
//
//	LOGPULSE_TEST_DATABASE_URL=postgres://user:pass@localhost:5432/db?sslmode=disable go test ./...
//
// 每个测试在独立的临时 schema 中执行迁移，结束后删除，不影响库中已有数据。
package dbtest

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eloats/logpulse/backend/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

var seq atomic.Int64

// Pool 返回连接到新建临时 schema 的连接池，并执行到 migration 指定的版本（空表示全部）。
// 未设置 LOGPULSE_TEST_DATABASE_URL 时跳过测试。
func Pool(t testing.TB, migration string) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("LOGPULSE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("未设置 LOGPULSE_TEST_DATABASE_URL，跳过数据库集成测试")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	// 扩展是全库共享的：先装到 public，否则会装进第一个创建它的临时 schema，
	// 随该 schema 一起被删除，导致并行运行的其他测试找不到 gin_trgm_ops
	if _, err := admin.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("lp_test_%d_%d", time.Now().UnixNano(), seq.Add(1))
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	if err := db.MigrateTo(ctx, pool, migration); err != nil {
		t.Fatal(err)
	}
	return pool
}
