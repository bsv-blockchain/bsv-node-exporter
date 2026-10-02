package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bsv-blockchain/bsv-node-exporter/internal/collector"
	"github.com/bsv-blockchain/bsv-node-exporter/internal/config"
	"github.com/bsv-blockchain/bsv-node-exporter/internal/noderpc"
	"github.com/bsv-blockchain/bsv-node-exporter/internal/server"
	"github.com/prometheus/client_golang/prometheus"
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
	return scrapeReleasing(t, cfg, logger, func() {})
}

// scrapeReleasing scrapes with its own 10s deadline and calls release before
// closing the test server, so a scrape stuck on a fake node fails instead of
// hanging until the package timeout.
func scrapeReleasing(t *testing.T, cfg config.Config, logger *slog.Logger, release func()) string {
	t.Helper()
	ts := httptest.NewServer(buildServer(cfg, logger).Handler)
	defer ts.Close()
	defer release() // before ts.Close, which waits for in-flight handlers
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/metrics", nil)
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // G704: request to the test's own httptest server.
	if err != nil {
		t.Fatalf("scrape did not finish within 10s: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
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
	buildInfo := `bsv_exporter_build_info{goversion="` + runtime.Version() + `",version="dev"} 1`
	for _, want := range []string{"bsv_blocks 34346", `bsv_peers{kind="outbound"} 2`, `bsv_rpc_up{method="getchaintips"} 1`, buildInfo} {
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

// hungNode accepts a request and never answers until the client goes away or
// release is called. Call release before closing anything that waits on it, so a
// test whose guard is broken fails instead of deadlocking in cleanup.
func hungNode(t *testing.T) (node *httptest.Server, release func()) {
	t.Helper()
	stop := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(stop) }) }
	node = httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	t.Cleanup(func() { release(); node.Close() })
	return node, release
}

func assertAllDown(t *testing.T, out string) {
	t.Helper()
	for _, m := range []string{"getblockchaininfo", "getpeerinfo", "getmempoolinfo", "getchaintips"} {
		if want := `bsv_rpc_up{method="` + m + `"} 0`; !strings.Contains(out, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}

// Guard 1: the per-call context. The HTTP client here has no timeouts at all,
// so only collector.Options.Timeout can end the call.
func TestHungNodeBoundedByPerCallContext(t *testing.T) {
	node, release := hungNode(t)
	client := noderpc.New(node.URL, "", "", &http.Client{CheckRedirect: noderpc.NoRedirects})
	coll := collector.New(client, collector.Options{
		Timeout: 300 * time.Millisecond, MempoolSource: config.MempoolSourceMempoolInfo,
		Enabled: map[string]bool{config.CollectorBlockchain: true, config.CollectorPeers: true, config.CollectorMempool: true, config.CollectorChaintips: true},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	reg := prometheus.NewRegistry()
	reg.MustRegister(coll)
	start := time.Now()
	ts := httptest.NewServer(server.New(":0", reg, 10*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler)
	defer ts.Close()
	defer release() // runs before ts.Close, which would otherwise wait on the hung call
	// The test's own deadline, so a lost guard fails fast instead of hanging the suite.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/metrics", nil)
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // G704: request to the test's own httptest server.
	if err != nil {
		t.Fatalf("scrape did not finish within 5s with a 300ms per-call timeout: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("scrape took %v with a 300ms per-call timeout and no client timeouts", elapsed)
	}
	assertAllDown(t, string(b))
}

// Guard 2: the transport's ResponseHeaderTimeout, which ends a call to a node
// that accepted the request but never sent headers, independent of the context.
func TestRPCTransportBoundsResponseHeaders(t *testing.T) {
	tr := newRPCHTTPClient(500 * time.Millisecond).Transport.(*http.Transport)
	if tr.ResponseHeaderTimeout != 500*time.Millisecond {
		t.Errorf("ResponseHeaderTimeout = %v, want the RPC timeout", tr.ResponseHeaderTimeout)
	}
	node, _ := hungNode(t)
	// The test's own deadline, so a lost guard fails fast instead of hanging the suite.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, node.URL, strings.NewReader("{}"))
	start := time.Now()
	resp, err := (&http.Client{Transport: tr}).Do(req) //nolint:gosec // G704: request to the test's own httptest server.
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected a timeout from a node that never answers")
	}
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Fatalf("transport waited %v for headers with a 500ms ResponseHeaderTimeout", elapsed)
	}
}

// Both guards together, through buildServer.
func TestEndToEndHungNodeBoundsScrape(t *testing.T) {
	node, release := hungNode(t)
	cfg := load(t, map[string]string{"BSV_RPC_URL": node.URL, "BSV_RPC_TIMEOUT": "500ms"})
	start := time.Now()
	out := scrapeReleasing(t, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), release)
	if elapsed := time.Since(start); elapsed > 1200*time.Millisecond {
		t.Fatalf("scrape took %v against a hung node with a 500ms RPC timeout", elapsed)
	}
	assertAllDown(t, out)
}

func TestRPCTransportBoundsResponseHeaderSize(t *testing.T) {
	// Review of #4: headers fell outside every response budget (Go's default
	// allows 10 MiB), so a 9 MiB header on a 1 MiB call allocated ~59 MiB.
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("X-Pad", strings.Repeat("a", 1<<20))
		_, _ = w.Write([]byte(`{"result":{"blocks":1,"headers":1,"difficulty":1},"error":null,"id":"x"}`))
	}))
	t.Cleanup(node.Close)
	client := noderpc.New(node.URL, "", "", newRPCHTTPClient(5*time.Second))
	var out map[string]any
	if err := client.Call(t.Context(), "getblockchaininfo", &out); err == nil {
		t.Fatal("a 1 MiB response header must be rejected")
	}
}

func TestStdlibLogNeverCarriesNodeBytes(t *testing.T) {
	// Review of #4: net/http writes through the standard log package, e.g.
	// "Unsolicited response received on idle HTTP channel starting with %q",
	// quoting up to ~4 KiB the node sent after a valid response.
	var logs lockedBuffer // net/http logs from its own goroutine
	restore := routeStdlibLog(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer restore()

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		body := `{"result":{"blocks":1,"headers":1,"difficulty":1},"error":null,"id":"x"}`
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
		time.Sleep(200 * time.Millisecond) // the connection is idle in the pool now
		_, _ = conn.Write([]byte("s3cret-pw trailing bytes"))
		time.Sleep(300 * time.Millisecond)
	}()

	client := noderpc.New("http://"+ln.Addr().String(), "u", "s3cret-pw", newRPCHTTPClient(5*time.Second))
	var out map[string]any
	if err := client.Call(t.Context(), "getblockchaininfo", &out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline) && !strings.Contains(logs.String(), "net/http logged"); {
		time.Sleep(20 * time.Millisecond)
	}
	if strings.Contains(logs.String(), "s3cret") {
		t.Fatalf("log output carries node bytes: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "net/http logged a message") {
		t.Errorf("expected one fixed warning in place of net/http's message, got: %q", logs.String())
	}
}

// lockedBuffer is a bytes.Buffer safe for concurrent writers and readers.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRunRoutesStdlibLogFirst(t *testing.T) {
	// Review of #4: deleting the routeStdlibLog call from run() went unnoticed.
	prevOut, prevFlags := log.Writer(), log.Flags()
	defer func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) }()
	t.Setenv("BSV_RPC_URL", "") // config fails at once, after logging is set up
	if err := run(slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("run should fail without BSV_RPC_URL")
	}
	if _, ok := log.Writer().(stdlibSink); !ok {
		t.Errorf("run() did not route the stdlib logger: writer is %T", log.Writer())
	}
}
