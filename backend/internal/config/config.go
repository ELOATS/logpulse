// Package config 从环境变量读取服务配置。
package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Addr            string        // HTTP 监听地址
	DatabaseURL     string        // PostgreSQL 连接串
	AllowedOrigin   string        // CORS 允许的来源
	MaxUploadMB     int64         // 上传文件大小上限
	AnalyzeInterval time.Duration // 后台异常检测周期，0 表示关闭
	Bucket          time.Duration // 时间桶大小
	BaselineBuckets int           // 基线窗口包含的桶数
	ZThreshold      float64       // 触发异常的 z-score 阈值
	MinCount        int           // 触发异常所需的最小计数
	AnalyzeLookback time.Duration // 每次分析回溯的时间范围

	// 大模型异常摘要（设置 ANTHROPIC_API_KEY 后启用）
	LLMEnabled       bool
	LLMModel         string
	LLMEffort        string // low | medium | high | xhigh | max
	LLMAutoSummarize bool   // 自动为新的中/高危异常生成摘要
	LLMAutoPerRun    int    // 每轮检测最多自动生成的摘要数，控制成本
	LLMSampleLines   int    // 每个异常附带的样例日志行数
}

func Load() Config {
	return Config{
		Addr:            env("ADDR", ":8080"),
		DatabaseURL:     env("DATABASE_URL", "postgres://loganalyzer:loganalyzer@localhost:5432/loganalyzer?sslmode=disable"),
		AllowedOrigin:   env("ALLOWED_ORIGIN", "*"),
		MaxUploadMB:     int64(envInt("MAX_UPLOAD_MB", 50)),
		AnalyzeInterval: envDuration("ANALYZE_INTERVAL", time.Minute),
		Bucket:          envDuration("ANALYZE_BUCKET", time.Minute),
		BaselineBuckets: envInt("ANALYZE_BASELINE_BUCKETS", 60),
		ZThreshold:      envFloat("ANALYZE_Z_THRESHOLD", 5),
		MinCount:        envInt("ANALYZE_MIN_COUNT", 5),
		AnalyzeLookback: envDuration("ANALYZE_LOOKBACK", 24*time.Hour),

		LLMEnabled:       os.Getenv("ANTHROPIC_API_KEY") != "" && env("LLM_ENABLED", "true") != "false",
		LLMModel:         env("LLM_MODEL", "claude-opus-5-5"),
		LLMEffort:        env("LLM_EFFORT", "medium"),
		LLMAutoSummarize: env("LLM_AUTO_SUMMARIZE", "true") != "false",
		LLMAutoPerRun:    envInt("LLM_AUTO_PER_RUN", 3),
		LLMSampleLines:   envInt("LLM_SAMPLE_LINES", 30),
	}
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return v
	}
	return def
}
