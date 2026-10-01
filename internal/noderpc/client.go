// Package noderpc is a minimal JSON-RPC 1.0 client for SV Node and Teranode,
// restricted to an allowlist of the methods the exporter needs.
package noderpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxResponseBytes is the largest per-method response budget.
const MaxResponseBytes = 16 << 20

// ErrMethodNotAllowed is returned for methods outside the allowlist.
var ErrMethodNotAllowed = errors.New("rpc method not allowed")

// allowedMethods is the allowlist, with each method's response budget in bytes.
// Object results are small; only the two array results get room to grow.
var allowedMethods = map[string]int{
	"getblockchaininfo":  1 << 20,
	"getmempoolinfo":     1 << 20,
	"getminingcandidate": 1 << 20,
	"getpeerinfo":        8 << 20,
	"getchaintips":       MaxResponseBytes,
}

// Error is an error returned by the node in the JSON-RPC envelope.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// maxErrorMessage bounds how much of a node-supplied message reaches logs.
const maxErrorMessage = 120

// Error renders the code and a bounded, sanitised prefix of the node's
// message: enough for "Work queue depth exceeded", not enough to flood logs.
func (e *Error) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, sanitizeMessage(e.Message))
}

func sanitizeMessage(s string) string { return Bound(s, maxErrorMessage) }

// Bound returns s with control and format characters replaced by spaces and at
// most limit bytes long, including the "..." it ends with when s was cut. Every
// rune is checked for its encoded size, so the cap holds for multibyte text.
func Bound(s string, limit int) string {
	const suffix = "..."
	var b strings.Builder
	fit := 0 // length of b at the last rune boundary that leaves room for suffix
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			r = ' '
		}
		if b.Len()+utf8.RuneLen(r) > limit {
			return b.String()[:fit] + suffix
		}
		b.WriteRune(r)
		if b.Len() <= limit-len(suffix) {
			fit = b.Len()
		}
	}
	return b.String()
}

// NoRedirects is an http.Client CheckRedirect that refuses to follow redirects.
func NoRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// Client calls a single node's RPC endpoint.
type Client struct {
	url      string
	user     string
	password string
	hc       *http.Client
}

// New returns a client for url. hc should set CheckRedirect to NoRedirects.
func New(url, user, password string, hc *http.Client) *Client {
	return &Client{url: url, user: user, password: password, hc: hc}
}

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type response struct {
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

// Call invokes method with no parameters and decodes the result into out.
// Errors name the method and HTTP status but never include credentials or
// the response body.
func (c *Client) Call(ctx context.Context, method string, out any) error {
	budget, ok := allowedMethods[method]
	if !ok {
		return fmt.Errorf("%w: %s", ErrMethodNotAllowed, method)
	}
	body, err := json.Marshal(request{JSONRPC: "1.0", ID: "bsv-node-exporter", Method: method, Params: []any{}})
	if err != nil {
		return fmt.Errorf("%s: encoding request: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: building request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.user != "" || c.password != "" {
		req.SetBasicAuth(c.user, c.password)
	}

	resp, err := c.hc.Do(req) //nolint:gosec // G704: the URL is operator configuration (BSV_RPC_URL), never request input.
	if err != nil {
		// *url.Error quotes the full request URL, path included; keep only its cause.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%s: unauthorized (HTTP %d)", method, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(budget)+1))
	if err != nil {
		return fmt.Errorf("%s: reading response: %w", method, err)
	}
	if len(data) > budget {
		return fmt.Errorf("%s: response exceeds %d bytes", method, budget)
	}

	var r response
	if err := json.Unmarshal(data, &r); err != nil {
		return fmt.Errorf("%s: HTTP %d, undecodable response", method, resp.StatusCode)
	}
	if r.Error != nil {
		redacted := &Error{Code: r.Error.Code, Message: c.redact(r.Error.Message)}
		return fmt.Errorf("%s: HTTP %d: %w", method, resp.StatusCode, redacted)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", method, resp.StatusCode)
	}
	if len(r.Result) == 0 || string(r.Result) == "null" {
		return fmt.Errorf("%s: empty result", method)
	}
	if err := json.Unmarshal(r.Result, out); err != nil {
		return fmt.Errorf("%s: %s", method, describeDecodeError(err))
	}
	return nil
}

// describeDecodeError classifies a result decode failure without any node data.
// json.UnmarshalTypeError carries the offending literal, which the node controls
// and which can be megabytes long.
func describeDecodeError(err error) string {
	var typeErr *json.UnmarshalTypeError
	var syntaxErr *json.SyntaxError
	switch {
	case errors.As(err, &typeErr):
		return fmt.Sprintf("result has an unexpected or out-of-range value for %s at offset %d", typeErr.Type, typeErr.Offset)
	case errors.As(err, &syntaxErr):
		return fmt.Sprintf("result is not valid JSON at offset %d", syntaxErr.Offset)
	default:
		return "result does not match the expected shape"
	}
}

// redact removes the configured credentials from a node-supplied message: a
// compromised node receives them on every call and could echo them back into
// our logs. Longest first, so the Basic token goes before its parts.
func (c *Client) redact(msg string) string {
	var secrets []string
	if c.user != "" || c.password != "" {
		pair := c.user + ":" + c.password
		secrets = append(secrets, base64.StdEncoding.EncodeToString([]byte(pair)), pair)
	}
	for _, s := range []string{c.password, c.user} {
		if s != "" {
			secrets = append(secrets, s)
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, s := range secrets {
		msg = strings.ReplaceAll(msg, s, "[redacted]")
	}
	return msg
}
