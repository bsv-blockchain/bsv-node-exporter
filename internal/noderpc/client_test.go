package noderpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func TestCallSendsJSONRPCWithBasicAuth(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		u, p, ok := r.BasicAuth()
		if !ok || u != "user" || p != "pw" {
			t.Errorf("basic auth = %q %q %v", u, p, ok)
		}
		var req struct {
			JSONRPC string `json:"jsonrpc"`
			Method  string `json:"method"`
			Params  []any  `json:"params"`
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("request body: %v", err)
		}
		if req.JSONRPC != "1.0" || req.Method != "getblockchaininfo" || req.Params == nil || len(req.Params) != 0 {
			t.Errorf("request = %s", body)
		}
		_, _ = w.Write([]byte(`{"result":{"blocks":42},"error":null,"id":"bsv-node-exporter"}`))
	})
	var out struct {
		Blocks int `json:"blocks"`
	}
	c := New(srv.URL, "user", "pw", srv.Client())
	if err := c.Call(context.Background(), "getblockchaininfo", &out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out.Blocks != 42 {
		t.Errorf("Blocks = %d", out.Blocks)
	}
}

func TestCallRejectsMethodNotOnAllowlist(t *testing.T) {
	called := false
	srv := newServer(t, func(http.ResponseWriter, *http.Request) { called = true })
	err := New(srv.URL, "", "", srv.Client()).Call(context.Background(), "stop", &struct{}{})
	if !errors.Is(err, ErrMethodNotAllowed) {
		t.Fatalf("err = %v, want ErrMethodNotAllowed", err)
	}
	if called {
		t.Fatal("disallowed method reached the server")
	}
}

func TestCallRPCError(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		// SV Node returns RPC errors with HTTP 500 and a JSON envelope.
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"result":null,"error":{"code":-32601,"message":"Method not found"},"id":"x"}`))
	})
	err := New(srv.URL, "", "", srv.Client()).Call(context.Background(), "getmempoolinfo", &struct{}{})
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32601 {
		t.Fatalf("err = %v, want *Error code -32601", err)
	}
	if !strings.Contains(err.Error(), "getmempoolinfo") || !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("error does not name method and HTTP status: %v", err)
	}
}

func TestCallUnauthorized(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // bitcoind sends an empty body
	})
	err := New(srv.URL, "user", "s3cret-pw", srv.Client()).Call(context.Background(), "getpeerinfo", &struct{}{})
	if err == nil || !strings.Contains(err.Error(), "unauthorized (HTTP 401)") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "s3cret-pw") {
		t.Fatalf("error contains password: %v", err)
	}
}

func TestCallNonJSONBody(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html><body>upstream secret-page-content</body></html>"))
	})
	err := New(srv.URL, "", "", srv.Client()).Call(context.Background(), "getchaintips", &[]any{})
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "secret-page-content") {
		t.Fatalf("error echoes body: %v", err)
	}
}

func TestCallNullResult(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":null,"error":null,"id":"x"}`))
	})
	err := New(srv.URL, "", "", srv.Client()).Call(context.Background(), "getblockchaininfo", &struct{}{})
	if err == nil || !strings.Contains(err.Error(), "empty result") {
		t.Fatalf("err = %v", err)
	}
}

func TestCallRejectsOversizedResponse(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":"`))
		chunk := strings.Repeat("a", 1<<20)
		for i := 0; i <= MaxResponseBytes>>20; i++ {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
		_, _ = w.Write([]byte(`"}`))
	})
	err := New(srv.URL, "", "", srv.Client()).Call(context.Background(), "getpeerinfo", &[]any{})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v", err)
	}
}

func TestCallDoesNotFollowRedirects(t *testing.T) {
	target := newServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("redirect was followed")
	})
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	})
	hc := &http.Client{CheckRedirect: NoRedirects}
	if err := New(srv.URL, "u", "p", hc).Call(context.Background(), "getpeerinfo", &[]any{}); err == nil {
		t.Fatal("expected error on redirect")
	}
}

func TestCallHonoursContext(t *testing.T) {
	srv := newServer(t, func(_ http.ResponseWriter, r *http.Request) {
		// The server only notices a client disconnect once the body is consumed.
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := New(srv.URL, "", "", srv.Client()).Call(ctx, "getchaintips", &[]any{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("call outlived its context")
	}
}

func TestErrorMessageIsBoundedAndSanitised(t *testing.T) {
	e := &Error{Code: -28, Message: "Work queue\n\x1b[31m depth exceeded " + strings.Repeat("x", 5000)}
	got := e.Error()
	if len(got) > 200 {
		t.Errorf("Error() is %d bytes, want <= 200", len(got))
	}
	if !strings.HasPrefix(got, "rpc error -28: Work queue") {
		t.Errorf("Error() = %q, want code and message prefix", got)
	}
	for _, r := range got {
		if r < 0x20 || r == 0x7f {
			t.Fatalf("Error() contains control character %q: %q", r, got)
		}
	}
}

func TestErrorMessageCapCountsMultibyteRunes(t *testing.T) {
	e := &Error{Code: -1, Message: strings.Repeat("a", maxErrorMessage-1) + "😀😀"}
	msg := strings.TrimPrefix(e.Error(), "rpc error -1: ")
	if body := strings.TrimSuffix(msg, "..."); len(body) > maxErrorMessage {
		t.Errorf("message part is %d bytes, cap is %d: %q", len(body), maxErrorMessage, body)
	}
	if !strings.HasSuffix(msg, "...") {
		t.Errorf("truncated message should end with ...: %q", msg)
	}
}
