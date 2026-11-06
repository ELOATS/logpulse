// Package api 提供 REST 接口。
package api

import (
	"bufio"
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
	"time"

	"github.com/eloats/logpulse/backend/internal/analyzer"
	"github.com/eloats/logpulse/backend/internal/parser"
	"github.com/eloats/logpulse/backend/internal/store"
	"github.com/jackc/pgx/v5"
)

const ingestBatchSize = 2000

type Server struct {
	Store         *store.Store
	Analyzer      *analyzer.Analyzer
	Log           *slog.Logger
	AllowedOrigin string
	MaxUploadMB   int64
	StaticDir     string // 可选：同时托管前端构建产物
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("POST /api/logs", s.ingest)
	mux.HandleFunc("POST /api/logs/upload", s.upload)
	mux.HandleFunc("GET /api/logs", s.searchLogs)
	mux.HandleFunc("GET /api/stats/overview", s.overview)
	mux.HandleFunc("GET /api/stats/timeseries", s.timeseries)
	mux.HandleFunc("GET /api/services", s.services)
	mux.HandleFunc("GET /api/patterns", s.patterns)
	mux.HandleFunc("GET /api/anomalies", s.anomalies)
	mux.HandleFunc("POST /api/anomalies/{id}/ack", s.ackAnomaly)
	mux.HandleFunc("POST /api/analyze", s.analyze)
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
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	total := store.IngestResult{}
	batch := make([]parser.Entry, 0, ingestBatchSize)
	flush := func() error {
		res, err := s.Store.Ingest(r.Context(), batch)
		if err != nil {
			return err
		}
		total.Accepted += res.Accepted
		total.NewPatterns += res.NewPatterns
		batch = batch[:0]
		return nil
	}
	for sc.Scan() {
		if e, ok := p.ParseLine(sc.Text()); ok {
			batch = append(batch, e)
			if len(batch) >= ingestBatchSize {
				if err := flush(); err != nil {
					s.serverError(w, err)
					return
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if err := flush(); err != nil {
		s.serverError(w, err)
		return
	}
	s.Analyzer.Trigger()
	writeJSON(w, http.StatusOK, total)
}

func (s *Server) ingestEntries(w http.ResponseWriter, r *http.Request, entries []parser.Entry) {
	total := store.IngestResult{}
	for i := 0; i < len(entries); i += ingestBatchSize {
		end := min(i+ingestBatchSize, len(entries))
		res, err := s.Store.Ingest(r.Context(), entries[i:end])
		if err != nil {
			s.serverError(w, err)
			return
		}
		total.Accepted += res.Accepted
		total.NewPatterns += res.NewPatterns
	}
	s.Analyzer.Trigger()
	writeJSON(w, http.StatusOK, total)
}

func (s *Server) searchLogs(w http.ResponseWriter, r *http.Request) {
	f, err := logFilterFromQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	page, err := s.Store.SearchLogs(r.Context(), f)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	f, err := logFilterFromQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	o, err := s.Store.Overview(r.Context(), f.From, f.To)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *Server) timeseries(w http.ResponseWriter, r *http.Request) {
	f, err := logFilterFromQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	bucket := time.Minute
	if b := r.URL.Query().Get("bucket"); b != "" {
		if bucket, err = time.ParseDuration(b); err != nil {
			writeError(w, http.StatusBadRequest, "invalid bucket")
			return
		}
	}
	pts, err := s.Store.Timeseries(r.Context(), f, bucket)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pts)
}

func (s *Server) services(w http.ResponseWriter, r *http.Request) {
	svcs, err := s.Store.Services(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, svcs)
}

func (s *Server) patterns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	ps, err := s.Store.Patterns(r.Context(), store.PatternFilter{
		Service: q.Get("service"), Level: q.Get("level"), Query: q.Get("q"), Sort: q.Get("sort"), Limit: limit,
	})
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

func (s *Server) anomalies(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	f := store.AnomalyFilter{Kind: q.Get("kind"), Severity: q.Get("severity"), Service: q.Get("service"), Limit: limit}
	if v := q.Get("acknowledged"); v != "" {
		b := v == "true"
		f.Acknowledged = &b
	}
	as, err := s.Store.Anomalies(r.Context(), f)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, as)
}

func (s *Server) ackAnomaly(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	body := struct {
		Acknowledged *bool `json:"acknowledged"`
	}{}
	_ = json.NewDecoder(r.Body).Decode(&body)
	ack := true
	if body.Acknowledged != nil {
		ack = *body.Acknowledged
	}
	if err := s.Store.AckAnomaly(r.Context(), id, ack); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "anomaly not found")
			return
		}
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "acknowledged": ack})
}

func (s *Server) analyze(w http.ResponseWriter, r *http.Request) {
	res, err := s.Analyzer.Run(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---------- helpers ----------

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
