package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bsv-blockchain/bsv-node-exporter/internal/config"
)

// fakeNode serves fixture results in a JSON-RPC envelope, or 401 when wantPassword
// does not match.
func fakeNode(t *testing.T, dir, wantPassword string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, p, _ := r.BasicAuth(); p != wantPassword {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			Method string `json:"method"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		result, err := os.ReadFile(filepath.Join(dir, req.Method+".json"))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"result":null,"error":{"code":-32601,"message":"Method not found"},"id":"x"}`))
			return
		}
		_, _ = w.Write([]byte(`{"result":` + string(result) + `,"error":null,"id":"x"}`))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func scrape(t *testing.T, cfg config.Config, logger *slog.Logger) string {
	t.Helper()
	ts := httptest.NewServer(buildServer(cfg, logger).Handler)
	defer ts.Close()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/metrics", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func load(t *testing.T, env map[string]string) config.Config {
	t.Helper()
	cfg, err := config.Load(func(k string) string { return env[k] }, os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestEndToEndSVNode(t *testing.T) {
	node := fakeNode(t, "../../internal/collector/testdata/svnode", "pw")
	cfg := load(t, map[string]string{"BSV_RPC_URL": node.URL, "BSV_RPC_USER": "u", "BSV_RPC_PASSWORD": "pw"})
	out := scrape(t, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, want := range []string{"bsv_blocks 34346", `bsv_peers{kind="outbound"} 2`, `bsv_rpc_up{method="getchaintips"} 1`} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
	if strings.Contains(out, "go_goroutines") || strings.Contains(out, "process_") {
		t.Error("runtime/process collectors must not be registered")
	}
}

func TestEndToEndNoCredentialsInLogs(t *testing.T) {
	node := fakeNode(t, "../../internal/collector/testdata/svnode", "right-pw")
	cfg := load(t, map[string]string{"BSV_RPC_URL": node.URL, "BSV_RPC_USER": "u", "BSV_RPC_PASSWORD": "wrong-s3cret"})
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	logger.Info("starting", "config", cfg)
	out := scrape(t, cfg, logger)
	if !strings.Contains(out, `bsv_rpc_up{method="getblockchaininfo"} 0`) {
		t.Errorf("expected rpc_up 0 on auth failure:\n%s", out)
	}
	if strings.Contains(logs.String(), "wrong-s3cret") || strings.Contains(out, "wrong-s3cret") {
		t.Fatalf("password leaked:\n%s\n%s", logs.String(), out)
	}
	if !strings.Contains(logs.String(), "unauthorized (HTTP 401)") {
		t.Errorf("auth failure not logged: %s", logs.String())
	}
}

func TestRPCHTTPClientIgnoresEnvironmentProxy(t *testing.T) {
	hc := newRPCHTTPClient(5 * time.Second)
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T, want an explicit *http.Transport (the default one honours HTTP_PROXY)", hc.Transport)
	}
	if tr.Proxy != nil {
		t.Error("Transport.Proxy is set: Basic auth could be sent through an environment proxy")
	}
	if hc.CheckRedirect == nil {
		t.Error("CheckRedirect not set: redirects would be followed")
	}
	if hc.Timeout != 6*time.Second {
		t.Errorf("Timeout = %v, want RPC timeout + 1s", hc.Timeout)
	}
}

func TestShutdownGraceCoversAnInFlightScrape(t *testing.T) {
	for _, rpc := range []time.Duration{5 * time.Second, 30 * time.Second} {
		if got := shutdownGrace(rpc); got != rpc+5*time.Second {
			t.Errorf("shutdownGrace(%v) = %v, want %v", rpc, got, rpc+5*time.Second)
		}
	}
}
