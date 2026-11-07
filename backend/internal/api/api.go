// Package api 提供 REST 接口。
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eloats/logpulse/backend/internal/analyzer"
	"github.com/eloats/logpulse/backend/internal/llm"
	"github.com/eloats/logpulse/backend/internal/parser"
	"github.com/eloats/logpulse/backend/internal/store"
	"github.com/eloats/logpulse/backend/internal/timerange"
	"github.com/jackc/pgx/v5"
)

const ingestBatchSize = 2000

type Server struct {
	Store         *store.Store
	Analyzer      *analyzer.Analyzer
	Log           *slog.Logger
	AllowedOrigin string
	MaxUploadMB   int64
	StaticDir     string       // 可选：同时托管前端构建产物
	Insights      *llm.Service // 可选：未配置 ANTHROPIC_API_KEY 时为 nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("POST /api/logs", s.ingest)
	mux.HandleFunc("POST /api/logs/upload", s.upload)
	mux.HandleFunc("GET /api/logs", s.searchLogs)
	mux.HandleFunc("GET /api/stats/overview", s.overview)
	mux.HandleFunc("GET /api/stats/timeseries", s.timeseries)
	mux.HandleFunc("GET /api/stats/dashboard", s.dashboard)
	mux.HandleFunc("GET /api/services", s.services)
	mux.HandleFunc("GET /api/patterns", s.patterns)
	mux.HandleFunc("GET /api/anomalies", s.anomalies)
	mux.HandleFunc("POST /api/anomalies/{id}/ack", s.ackAnomaly)
	mux.HandleFunc("POST /api/anomalies/{id}/insight", s.generateInsight)
	mux.HandleFunc("POST /api/analyze", s.analyze)
	mux.HandleFunc("GET /api/features", s.features)
	if s.StaticDir != "" {
		mux.Handle("/", spaHandler(s.StaticDir))
	}
	return s.withMiddleware(mux)
}

func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		w.Header().Set("Access-Control-Allow-Origin", s.AllowedOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		defer func() {
			if rec := recover(); rec != nil {
				s.Log.Error("panic", "err", rec, "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			s.Log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "dur", time.Since(start))
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func spaHandler(dir string) http.Handler {
	fsrv := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		fsrv.ServeHTTP(w, r)
	})
}

// ---------- handlers ----------

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.Pool.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ingest 支持三种请求体：
//   - application/json：对象数组，或 {"logs": [...]}，或单个对象
//   - 其他（text/plain、application/x-ndjson 等）：按行解析
func (s *Server) ingest(w http.ResponseWriter, r *http.Request) {
	p := parser.New(r.URL.Query().Get("service"))
	body := http.MaxBytesReader(w, r.Body, s.MaxUploadMB<<20)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		raw, err := io.ReadAll(body)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		objs, err := decodeJSONLogs(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		entries := make([]parser.Entry, 0, len(objs))
		for _, o := range objs {
			entries = append(entries, p.FromMap(o))
		}
		s.ingestEntries(w, r, entries)
		return
	}
	s.ingestReader(w, r, p, body)
}

func decodeJSONLogs(raw []byte) ([]map[string]any, error) {
	trimmed := strings.TrimSpace(string(raw))
	var objs []map[string]any
	switch {
	case strings.HasPrefix(trimmed, "["):
		err := json.Unmarshal(raw, &objs)
		return objs, err
	case strings.HasPrefix(trimmed, "{"):
		var wrapper struct {
			Logs []map[string]any `json:"logs"`
		}
		if err := json.Unmarshal(raw, &wrapper); err == nil && wrapper.Logs != nil {
			return wrapper.Logs, nil
		}
		var one map[string]any
		err := json.Unmarshal(raw, &one)
		return []map[string]any{one}, err
	}
	return nil, errors.New("expected JSON array or object")
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.MaxUploadMB<<20)
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file: "+err.Error())
		return
	}
	defer file.Close()
	service := r.FormValue("service")
	if service == "" {
		service = strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	}
	s.ingestReader(w, r, parser.New(service), file)
}

func (s *Server) ingestReader(w http.ResponseWriter, r *http.Request, p *parser.Parser, rd io.Reader) {
	b := &batcher{ctx: r.Context(), store: s.Store}
	err := p.ParseStream(rd, b.add)
	s.finishIngest(w, b, err)
}

func (s *Server) ingestEntries(w http.ResponseWriter, r *http.Request, entries []parser.Entry) {
	b := &batcher{ctx: r.Context(), store: s.Store}
	var err error
	for _, e := range entries {
		if err = b.add(e); err != nil {
			break
		}
	}
	s.finishIngest(w, b, err)
}

// finishIngest 写入剩余条目、触发异常检测并返回结果。
func (s *Server) finishIngest(w http.ResponseWriter, b *batcher, err error) {
	if err == nil {
		err = b.flush()
	}
	switch {
	case errors.Is(err, parser.ErrRead):
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
	case err != nil:
		s.serverError(w, err)
	default:
		s.Analyzer.Trigger()
		writeJSON(w, http.StatusOK, b.total)
	}
}

// batcher 把条目攒成批次写入，并累计写入结果。
type batcher struct {
	ctx   context.Context
	store *store.Store
	batch []parser.Entry
	total store.IngestResult
}

func (b *batcher) add(e parser.Entry) error {
	b.batch = append(b.batch, e)
	if len(b.batch) >= ingestBatchSize {
		return b.flush()
	}
	return nil
}

func (b *batcher) flush() error {
	if len(b.batch) == 0 {
		return nil
	}
	res, err := b.store.Ingest(b.ctx, b.batch)
	if err != nil {
		return err
	}
	b.total.Accepted += res.Accepted
	b.total.NewPatterns += res.NewPatterns
	b.batch = b.batch[:0]
	return nil
}

func (s *Server) searchLogs(w http.ResponseWriter, r *http.Request) {
	f, ok := s.filter(w, r)
	if ok {
		page, err := s.Store.SearchLogs(r.Context(), f)
		s.reply(w, page, err)
	}
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	f, ok := s.filter(w, r)
	if !ok {
		return
	}
	if _, ok := s.applyRange(w, r, r.URL.Query().Get("range"), &f, 0); ok {
		o, err := s.Store.Overview(r.Context(), f.From, f.To)
		s.reply(w, o, err)
	}
}

func (s *Server) timeseries(w http.ResponseWriter, r *http.Request) {
	f, ok := s.filter(w, r)
	if !ok {
		return
	}
	bucket, ok := parseBucket(w, r)
	if !ok {
		return
	}
	rng, ok := s.applyRange(w, r, r.URL.Query().Get("range"), &f, bucket)
	if !ok {
		return
	}
	if rng != nil {
		bucket = rng.Bucket
	} else if bucket == 0 {
		bucket = time.Minute
	}
	pts, err := s.Store.Timeseries(r.Context(), f, bucket)
	s.reply(w, fillSeries(pts, rng), err)
}

// dashboardResponse 是总览页一次请求所需的全部统计。
type dashboardResponse struct {
	Range    *timerange.Range  `json:"range"` // 没有任何日志时为 null
	Overview store.Overview    `json:"overview"`
	Series   []store.TimePoint `json:"series"`
}

// dashboard 返回 range（默认 24h，以最新一条日志为锚点）内的总览与趋势。
// 起点、时间桶、不完整的桶都由服务端计算，客户端按结果渲染即可。
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("range")
	if key == "" {
		key = "24h"
	}
	bucket, ok := parseBucket(w, r)
	if !ok {
		return
	}
	var f store.LogFilter
	rng, ok := s.applyRange(w, r, key, &f, bucket)
	if !ok {
		return
	}
	resp := dashboardResponse{Range: rng, Series: []store.TimePoint{}}
	if rng == nil { // 还没有日志
		var err error
		resp.Overview, err = s.Store.Overview(r.Context(), nil, nil)
		s.reply(w, resp, err)
		return
	}
	// 总览与趋势互不依赖，并发查询
	var wg sync.WaitGroup
	var errO, errS error
	wg.Add(2)
	go func() { defer wg.Done(); resp.Overview, errO = s.Store.Overview(r.Context(), f.From, nil) }()
	go func() { defer wg.Done(); resp.Series, errS = s.Store.Timeseries(r.Context(), f, rng.Bucket) }()
	wg.Wait()
	resp.Series = fillSeries(resp.Series, rng)
	s.reply(w, resp, errors.Join(errO, errS))
}

// applyRange 解析范围键 key：把起点写入 f.From 并返回范围；key 为空或还没有任何日志时
// 返回 nil。解析失败时直接返回 400。
func (s *Server) applyRange(w http.ResponseWriter, r *http.Request, key string, f *store.LogFilter, bucket time.Duration) (*timerange.Range, bool) {
	if key == "" {
		return nil, true
	}
	if _, err := timerange.Duration(key); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	minTS, maxTS, err := s.Store.DataRange(r.Context())
	if err != nil {
		s.serverError(w, err)
		return nil, false
	}
	if maxTS == nil {
		return nil, true
	}
	rng, err := timerange.Resolve(key, *minTS, *maxTS, bucket)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	f.From = &rng.From
	return &rng, true
}

// parseBucket 解析 ?bucket=（如 5m）；未指定时返回 0。
func parseBucket(w http.ResponseWriter, r *http.Request) (time.Duration, bool) {
	b := r.URL.Query().Get("bucket")
	if b == "" {
		return 0, true
	}
	d, err := time.ParseDuration(b)
	if err != nil || d <= 0 {
		writeError(w, http.StatusBadRequest, "invalid bucket")
		return 0, false
	}
	return d, true
}

// maxFilledPoints 限制补零后的点数（自定义极小时间桶 + 长范围时不补零）。
const maxFilledPoints = 10000

// fillSeries 按范围补齐没有日志的时间桶（计数为 0），并标记不完整的桶。
// 数据库只返回有日志的桶；不补零时图表会把空档连成一条直线，掩盖"这段时间没有日志"。
func fillSeries(pts []store.TimePoint, rng *timerange.Range) []store.TimePoint {
	if rng == nil {
		return pts
	}
	starts := rng.Starts()
	if len(starts) > maxFilledPoints {
		for i := range pts {
			pts[i].Partial = rng.Partial(pts[i].Bucket)
		}
		return pts
	}
	byStart := make(map[int64]store.TimePoint, len(pts))
	for _, p := range pts {
		byStart[p.Bucket.Unix()] = p
	}
	out := make([]store.TimePoint, len(starts))
	for i, t := range starts {
		p, ok := byStart[t.Unix()]
		if !ok {
			p = store.TimePoint{Bucket: t}
		}
		p.Partial = rng.Partial(t)
		out[i] = p
	}
	return out
}

func (s *Server) services(w http.ResponseWriter, r *http.Request) {
	svcs, err := s.Store.Services(r.Context())
	s.reply(w, svcs, err)
}

func (s *Server) patterns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	ps, err := s.Store.Patterns(r.Context(), store.PatternFilter{
		Service: q.Get("service"), Level: q.Get("level"), Query: q.Get("q"), Sort: q.Get("sort"), Limit: limit,
	})
	s.reply(w, ps, err)
}

func (s *Server) anomalies(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	f := store.AnomalyFilter{Kind: q.Get("kind"), Severity: q.Get("severity"), Service: q.Get("service"), Sort: q.Get("sort"), Limit: limit}
	if v := q.Get("acknowledged"); v != "" {
		b := v == "true"
		f.Acknowledged = &b
	}
	as, err := s.Store.Anomalies(r.Context(), f)
	s.reply(w, as, err)
}

func (s *Server) ackAnomaly(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	body := struct {
		Acknowledged *bool `json:"acknowledged"`
	}{}
	_ = json.NewDecoder(r.Body).Decode(&body)
	ack := body.Acknowledged == nil || *body.Acknowledged
	err := s.Store.AckAnomaly(r.Context(), id, ack)
	s.reply(w, map[string]any{"id": id, "acknowledged": ack}, err)
}

func (s *Server) features(w http.ResponseWriter, _ *http.Request) {
	f := map[string]any{"ai_insight": s.Insights != nil}
	if s.Insights != nil {
		f["ai_model"] = s.Insights.Summarizer.Model()
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) generateInsight(w http.ResponseWriter, r *http.Request) {
	if s.Insights == nil {
		writeError(w, http.StatusServiceUnavailable, "未启用 AI 分析：请为后端设置 ANTHROPIC_API_KEY")
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	a, err := s.Insights.Generate(r.Context(), id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "anomaly not found")
	case errors.Is(err, llm.ErrRefused):
		writeError(w, http.StatusUnprocessableEntity, "模型拒绝分析该内容："+err.Error())
	case err != nil:
		s.Log.Error("generate insight", "id", id, "err", err)
		writeError(w, http.StatusBadGateway, "AI 分析生成失败："+err.Error())
	default:
		writeJSON(w, http.StatusOK, a)
	}
}

func (s *Server) analyze(w http.ResponseWriter, r *http.Request) {
	res, err := s.Analyzer.Run(r.Context())
	s.reply(w, res, err)
}

// ---------- helpers ----------

// reply 在 err 为空时以 200 返回 v；记录不存在返回 404，其他错误返回 500。
func (s *Server) reply(w http.ResponseWriter, v any, err error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "not found")
	case err != nil:
		s.serverError(w, err)
	default:
		writeJSON(w, http.StatusOK, v)
	}
}

// filter 解析检索条件，出错时直接返回 400。
func (s *Server) filter(w http.ResponseWriter, r *http.Request) (store.LogFilter, bool) {
	f, err := logFilterFromQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return f, false
	}
	return f, true
}

// pathID 解析路径中的 {id}，出错时直接返回 400。
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

func logFilterFromQuery(r *http.Request) (store.LogFilter, error) {
	q := r.URL.Query()
	f := store.LogFilter{Query: q.Get("q"), Service: q.Get("service"), Host: q.Get("host")}
	if lv := q.Get("level"); lv != "" {
		for _, l := range strings.Split(lv, ",") {
			if l = strings.TrimSpace(l); l != "" {
				f.Levels = append(f.Levels, parser.NormalizeLevel(l))
			}
		}
	}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	f.Offset, _ = strconv.Atoi(q.Get("offset"))
	if f.Offset < 0 {
		f.Offset = 0
	}
	if v := q.Get("pattern_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return f, fmt.Errorf("invalid pattern_id")
		}
		f.PatternID = id
	}
	for key, dst := range map[string]**time.Time{"from": &f.From, "to": &f.To} {
		if v := q.Get(key); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return f, fmt.Errorf("invalid %s: use RFC3339", key)
			}
			*dst = &t
		}
	}
	return f, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) serverError(w http.ResponseWriter, err error) {
	s.Log.Error("request failed", "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
