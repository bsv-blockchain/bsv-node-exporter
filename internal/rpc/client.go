// Package rpc is a minimal JSON-RPC 1.0 client for SV Node and Teranode,
// restricted to the read-only methods the exporter needs.
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// MaxResponseBytes bounds how much of a response body is read.
const MaxResponseBytes = 32 << 20

// ErrMethodNotAllowed is returned for methods outside the allowlist.
var ErrMethodNotAllowed = errors.New("rpc method not allowed")

var allowedMethods = map[string]bool{
	"getblockchaininfo":  true,
	"getpeerinfo":        true,
	"getmempoolinfo":     true,
	"getminingcandidate": true,
	"getchaintips":       true,
}

// Error is an error returned by the node in the JSON-RPC envelope.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

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
	if !allowedMethods[method] {
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

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%s: unauthorized (HTTP %d)", method, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("%s: reading response: %w", method, err)
	}
	if len(data) > MaxResponseBytes {
		return fmt.Errorf("%s: response exceeds %d bytes", method, MaxResponseBytes)
	}

	var r response
	if err := json.Unmarshal(data, &r); err != nil {
		return fmt.Errorf("%s: HTTP %d, undecodable response", method, resp.StatusCode)
	}
	if r.Error != nil {
		return fmt.Errorf("%s: %w", method, r.Error)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", method, resp.StatusCode)
	}
	if len(r.Result) == 0 || string(r.Result) == "null" {
		return fmt.Errorf("%s: empty result", method)
	}
	if err := json.Unmarshal(r.Result, out); err != nil {
		return fmt.Errorf("%s: decoding result: %w", method, err)
	}
	return nil
}
