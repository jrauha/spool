package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/spool-reader/spool/internal/config"
)

func New(cfg config.Config, log *slog.Logger) *http.Server {
	mux := NewMux(log)
	return &http.Server{
		Addr:    cfg.Addr,
		Handler: mux,
	}
}

func NewMux(log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.Default()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("Spool\n"))
	})

	return requestLogger(log, mux)
}

func healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func requestLogger(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Debug("request", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
