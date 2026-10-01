// Package server serves the exporter's HTTP endpoints.
package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MaxScrapesInFlight caps concurrent /metrics gathers. Each gather makes up to
// four node RPC calls, so unlimited scrapes would multiply node load and memory.
const MaxScrapesInFlight = 2

// New returns an HTTP server exposing GET /metrics and GET /healthz only.
// writeTimeout must exceed the RPC timeout so a slow scrape can still answer.
func New(addr string, g prometheus.Gatherer, writeTimeout time.Duration, logger *slog.Logger) *http.Server {
	errLog := slog.NewLogLogger(logger.Handler(), slog.LevelError)
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(g, promhttp.HandlerOpts{
		ErrorLog:            errLog,
		ErrorHandling:       promhttp.ContinueOnError,
		MaxRequestsInFlight: MaxScrapesInFlight,
		// Answer 503 just before the write deadline instead of dropping the connection.
		Timeout: writeTimeout - time.Second,
	}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8 << 10,
		ErrorLog:          errLog,
	}
}
