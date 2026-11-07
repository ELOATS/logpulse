// 日志智能分析器服务入口。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/eloats/logpulse/backend/internal/analyzer"
	"github.com/eloats/logpulse/backend/internal/api"
	"github.com/eloats/logpulse/backend/internal/config"
	"github.com/eloats/logpulse/backend/internal/db"
	"github.com/eloats/logpulse/backend/internal/llm"
	"github.com/eloats/logpulse/backend/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("database connection failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		log.Error("migration failed", "err", err)
		os.Exit(1)
	}

	st := store.New(pool)
	an := analyzer.New(st, analyzer.Options{
		Bucket:          cfg.Bucket,
		BaselineBuckets: cfg.BaselineBuckets,
		ZThreshold:      cfg.ZThreshold,
		MinCount:        cfg.MinCount,
		Lookback:        cfg.AnalyzeLookback,
	}, log)
	var insights *llm.Service
	if cfg.LLMEnabled {
		insights = &llm.Service{
			Store: st, Log: log, SampleLines: cfg.LLMSampleLines, AutoPerRun: cfg.LLMAutoPerRun,
			Summarizer: llm.New(cfg.LLMModel, cfg.LLMEffort),
		}
		if cfg.LLMAutoSummarize {
			an.AfterRun = insights.AutoGenerate
		}
		log.Info("ai insight enabled", "model", cfg.LLMModel, "effort", cfg.LLMEffort, "auto", cfg.LLMAutoSummarize)
	}
	go an.Loop(ctx, cfg.AnalyzeInterval)
	if cfg.RetentionDays > 0 {
		go retentionLoop(ctx, st, cfg.RetentionDays, log)
	}

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: (&api.Server{
			Store: st, Analyzer: an, Log: log,
			AllowedOrigin: cfg.AllowedOrigin, MaxUploadMB: cfg.MaxUploadMB,
			StaticDir: cfg.StaticDir, Insights: insights,
		}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Info("server listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	log.Info("server stopped")
}

// retentionLoop 启动时及之后每小时清理一次超出保留期的数据。
func retentionLoop(ctx context.Context, st *store.Store, days int, log *slog.Logger) {
	run := func() {
		res, err := st.ApplyRetention(ctx, time.Now().AddDate(0, 0, -days))
		if err != nil {
			log.Error("retention failed", "err", err)
			return
		}
		if len(res.DroppedPartitions) > 0 || res.DeletedPatterns > 0 {
			log.Info("retention applied", "keep_days", days, "dropped", res.DroppedPartitions,
				"patterns", res.DeletedPatterns, "anomalies", res.DeletedAnomalies)
		}
	}
	run()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
