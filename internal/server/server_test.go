package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func newTestServer(t *testing.T) (*http.Server, *httptest.Server) {
	t.Helper()
	reg := prometheus.NewRegistry()
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_gauge", Help: "test"})
	g.Set(7)
	reg.MustRegister(g)
	srv := New(":0", reg, 15*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	return srv, ts
}

func get(t *testing.T, method, url string) (int, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), method, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestRoutes(t *testing.T) {
	_, ts := newTestServer(t)
	if code, body := get(t, http.MethodGet, ts.URL+"/metrics"); code != 200 || !strings.Contains(body, "test_gauge 7") {
		t.Errorf("/metrics = %d %q", code, body)
	}
	if code, body := get(t, http.MethodGet, ts.URL+"/healthz"); code != 200 || body != "ok\n" {
		t.Errorf("/healthz = %d %q", code, body)
	}
	if code, _ := get(t, http.MethodGet, ts.URL+"/"); code != http.StatusNotFound {
		t.Errorf("/ = %d, want 404", code)
	}
	if code, _ := get(t, http.MethodGet, ts.URL+"/debug/pprof/"); code != http.StatusNotFound {
		t.Errorf("/debug/pprof/ = %d, want 404", code)
	}
	if code, _ := get(t, http.MethodPost, ts.URL+"/metrics"); code != http.StatusMethodNotAllowed {
		t.Errorf("POST /metrics = %d, want 405", code)
	}
}

func TestTimeoutsSet(t *testing.T) {
	srv, _ := newTestServer(t)
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.IdleTimeout <= 0 || srv.MaxHeaderBytes <= 0 {
		t.Errorf("server limits not set: %+v", srv)
	}
	if srv.WriteTimeout != 15*time.Second {
		t.Errorf("WriteTimeout = %v, want 15s", srv.WriteTimeout)
	}
}
