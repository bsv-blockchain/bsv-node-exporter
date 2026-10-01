// Command bsv-node-exporter exposes SV Node / Teranode RPC data as Prometheus metrics.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/bsv-blockchain/bsv-node-exporter/internal/collector"
	"github.com/bsv-blockchain/bsv-node-exporter/internal/config"
	"github.com/bsv-blockchain/bsv-node-exporter/internal/noderpc"
	"github.com/bsv-blockchain/bsv-node-exporter/internal/server"
	"github.com/prometheus/client_golang/prometheus"
)

var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("exiting", "error", err.Error())
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load(os.Getenv, os.ReadFile)
	if err != nil {
		return err
	}
	srv := buildServer(cfg, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	logger.Info("starting", "version", version, "config", cfg)

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace(cfg.RPCTimeout))
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func buildServer(cfg config.Config, logger *slog.Logger) *http.Server {
	client := noderpc.New(cfg.RPCURL, cfg.RPCUser, cfg.RPCPassword, newRPCHTTPClient(cfg.RPCTimeout))
	coll := collector.New(client, collector.Options{
		Timeout:       cfg.RPCTimeout,
		MempoolSource: cfg.MempoolSource,
		Enabled:       cfg.Collectors,
	}, logger)
	reg := prometheus.NewRegistry()
	reg.MustRegister(coll, prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name:        "bsv_exporter_build_info",
		Help:        "Always 1; labels carry the exporter version and the Go version it was built with.",
		ConstLabels: prometheus.Labels{"version": version, "goversion": runtime.Version()},
	}, func() float64 { return 1 }))
	return server.New(cfg.ListenAddr, reg, cfg.RPCTimeout+5*time.Second, logger)
}

// newRPCHTTPClient builds the node RPC client. Its transport has no proxy:
// http.DefaultTransport honours HTTP_PROXY, which would send the node's Basic
// auth credentials through whatever proxy the environment names.
func newRPCHTTPClient(rpcTimeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       rpcTimeout + time.Second,
		CheckRedirect: noderpc.NoRedirects,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: rpcTimeout,
			MaxIdleConns:          4,
			MaxConnsPerHost:       8,
			IdleConnTimeout:       90 * time.Second,
		},
	}
}

// shutdownGrace is how long Shutdown waits for in-flight scrapes: the same
// bound as the server's write timeout, so SIGTERM mid-scrape still exits 0.
func shutdownGrace(rpcTimeout time.Duration) time.Duration { return rpcTimeout + 5*time.Second }
