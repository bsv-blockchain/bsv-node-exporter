// Command bsv-node-exporter exposes SV Node / Teranode RPC data as Prometheus metrics.
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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func buildServer(cfg config.Config, logger *slog.Logger) *http.Server {
	hc := &http.Client{
		Timeout:       cfg.RPCTimeout + time.Second,
		CheckRedirect: noderpc.NoRedirects,
	}
	client := noderpc.New(cfg.RPCURL, cfg.RPCUser, cfg.RPCPassword, hc)
	coll := collector.New(client, collector.Options{
		Timeout:       cfg.RPCTimeout,
		MempoolSource: cfg.MempoolSource,
		Enabled:       cfg.Collectors,
	}, logger)
	reg := prometheus.NewRegistry()
	reg.MustRegister(coll)
	return server.New(cfg.ListenAddr, reg, cfg.RPCTimeout+5*time.Second, logger)
}
