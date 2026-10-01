package noderpc

import (
	"context"
	"encoding/base64"
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
	if len(msg) > maxErrorMessage {
		t.Errorf("message part is %d bytes including the suffix, cap is %d: %q", len(msg), maxErrorMessage, msg)
	}
	if !strings.HasSuffix(msg, "...") {
		t.Errorf("truncated message should end with ...: %q", msg)
	}
}

func TestDecodeErrorDoesNotEchoNodeData(t *testing.T) {
	// Internal security audit, finding 2: Go's typed decode error keeps the
	// offending literal, so a node could put ~32 MiB of digits into one log line.
	// Under getblockchaininfo's 1 MiB budget, so the decode path is what runs.
	huge := "1" + strings.Repeat("0", 512<<10)
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":{"blocks":` + huge + `},"error":null,"id":"x"}`))
	})
	var out struct {
		Blocks *float64 `json:"blocks"`
	}
	err := New(srv.URL, "", "", srv.Client()).Call(context.Background(), "getblockchaininfo", &out)
	if err == nil {
		t.Fatal("expected a decode error for an out-of-range number")
	}
	if strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("hit the size budget instead of the decode path: %v", err)
	}
	if len(err.Error()) > 200 || strings.Contains(err.Error(), "000000") {
		t.Fatalf("decode error is %d bytes or echoes the literal: %.120q", len(err.Error()), err.Error())
	}
	if !strings.Contains(err.Error(), "getblockchaininfo") {
		t.Errorf("error should name the method: %q", err.Error())
	}
}

func TestRPCErrorRedactsReflectedCredentials(t *testing.T) {
	// Internal security audit, finding 5: a compromised node receives our Basic
	// credentials and can echo them in error.message, which we log.
	token := base64.StdEncoding.EncodeToString([]byte("rpcuser:s3cret-pw"))
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"result":null,"error":{"code":-1,"message":"auth rpcuser s3cret-pw Basic ` + token + `"},"id":"x"}`))
	})
	err := New(srv.URL, "rpcuser", "s3cret-pw", srv.Client()).Call(context.Background(), "getpeerinfo", &[]any{})
	if err == nil {
		t.Fatal("expected an RPC error")
	}
	for _, secret := range []string{"s3cret-pw", token, "rpcuser"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error contains %q: %q", secret, err.Error())
		}
	}
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != -1 {
		t.Errorf("code must survive redaction: %v", err)
	}
}

func TestTransportErrorOmitsURL(t *testing.T) {
	// Internal security audit, finding 3: *url.Error quotes the request URL,
	// path included.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL + "/s3cret-path-token/"
	srv.Close() // nothing listens: the call fails in the transport
	err := New(url, "", "", &http.Client{}).Call(context.Background(), "getpeerinfo", &[]any{})
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if strings.Contains(err.Error(), "s3cret-path-token") {
		t.Fatalf("transport error contains the URL path: %q", err.Error())
	}
}

func TestPerMethodResponseBudgets(t *testing.T) {
	// Internal security audit, finding 4: object results are small, so their
	// budget is much tighter than the array results'.
	pad := strings.Repeat("a", 2<<20)
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":{"blocks":1,"pad":"` + pad + `"},"error":null,"id":"x"}`))
	})
	c := New(srv.URL, "", "", srv.Client())
	var out struct {
		Blocks int `json:"blocks"`
	}
	for _, m := range []string{"getblockchaininfo", "getmempoolinfo", "getminingcandidate"} {
		if err := c.Call(context.Background(), m, &out); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Errorf("%s: a 2 MiB response must exceed its budget, got %v", m, err)
		}
	}
	var arr json.RawMessage
	for _, m := range []string{"getpeerinfo", "getchaintips"} {
		if err := c.Call(context.Background(), m, &arr); err != nil && strings.Contains(err.Error(), "exceeds") {
			t.Errorf("%s: a 2 MiB response must fit its budget, got %v", m, err)
		}
	}
}

func TestBound(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},  // fits: no suffix
		{"elevenchars", 10, "elevenc..."}, // cut: suffix counted in the cap
		{"a\nb\x1bc", 10, "a b c"},        // controls become spaces
		{"ab\u202ecd", 10, "ab cd"},       // bidi override (Cf) becomes a space
		{"😀😀😀", 10, "😀..."},               // never splits a rune
	}
	for _, c := range cases {
		if got := Bound(c.in, c.max); got != c.want || len(got) > c.max {
			t.Errorf("Bound(%q, %d) = %q (%d bytes), want %q", c.in, c.max, got, len(got), c.want)
		}
	}
}

func TestRedactionNeverReintroducesOrExpands(t *testing.T) {
	cases := []struct{ name, user, password, message string }{
		// "[redacted]" would put a username of "redacted" back into the output.
		{"credential inside the marker", "redacted", "s3cret-pw", "login failed for redacted"},
		// Substituting a short credential everywhere multiplies the message.
		{"dense repeats", "rpcuser", "aaaa", strings.Repeat("a", 1<<20)},
		// A credential split across the 120-byte cut must not leave a partial copy.
		{"across the cut", "rpcuser", "s3cret-pw", strings.Repeat("x", maxErrorMessage-4) + "s3cret-pw"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"result": nil, "id": "x", "error": map[string]any{"code": -1, "message": tc.message}})
			srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write(body)
			})
			err := New(srv.URL, tc.user, tc.password, srv.Client()).Call(context.Background(), "getchaintips", &[]any{})
			if err == nil {
				t.Fatal("expected an RPC error")
			}
			got := err.Error()
			for _, secret := range []string{tc.user, tc.password, tc.password[:4]} {
				if strings.Contains(got, secret) {
					t.Errorf("error contains %q: %.160q", secret, got)
				}
			}
			if len(got) > 200 {
				t.Errorf("error is %d bytes", len(got))
			}
		})
	}
}
