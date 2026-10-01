package config

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func noFile(string) ([]byte, error) { return nil, errors.New("no file") }

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"BSV_RPC_URL": "http://rpc:9292"}), noFile)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RPCURL != "http://rpc:9292" {
		t.Errorf("RPCURL = %q", cfg.RPCURL)
	}
	if cfg.RPCTimeout != 5*time.Second {
		t.Errorf("RPCTimeout = %v", cfg.RPCTimeout)
	}
	if cfg.MempoolSource != MempoolSourceMempoolInfo {
		t.Errorf("MempoolSource = %q", cfg.MempoolSource)
	}
	if cfg.ListenAddr != ":9480" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
	for _, c := range []string{CollectorBlockchain, CollectorPeers, CollectorMempool, CollectorChaintips} {
		if !cfg.Collectors[c] {
			t.Errorf("collector %q not enabled by default", c)
		}
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"BSV_RPC_URL":        "https://node.example.com:8332/",
		"BSV_RPC_USER":       "u",
		"BSV_RPC_PASSWORD":   "p",
		"BSV_RPC_TIMEOUT":    "3s",
		"BSV_MEMPOOL_SOURCE": "miningcandidate",
		"BSV_COLLECTORS":     " blockchain , peers ",
		"LISTEN_ADDR":        "127.0.0.1:9999",
	}), noFile)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RPCUser != "u" || cfg.RPCPassword != "p" {
		t.Errorf("credentials not loaded")
	}
	if cfg.RPCTimeout != 3*time.Second || cfg.MempoolSource != MempoolSourceMiningCandidate || cfg.ListenAddr != "127.0.0.1:9999" {
		t.Errorf("overrides not applied: %+v", cfg)
	}
	if len(cfg.Collectors) != 2 || !cfg.Collectors[CollectorBlockchain] || !cfg.Collectors[CollectorPeers] {
		t.Errorf("Collectors = %v", cfg.Collectors)
	}
}

func TestLoadPasswordFileTakesPrecedence(t *testing.T) {
	read := func(path string) ([]byte, error) {
		if path != "/run/secrets/rpc" {
			t.Fatalf("unexpected path %q", path)
		}
		return []byte("from-file\n"), nil
	}
	cfg, err := Load(env(map[string]string{
		"BSV_RPC_URL":           "http://rpc:9292",
		"BSV_RPC_PASSWORD":      "from-env",
		"BSV_RPC_PASSWORD_FILE": "/run/secrets/rpc",
	}), read)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RPCPassword != "from-file" {
		t.Errorf("RPCPassword = %q, want trailing newline trimmed file value", cfg.RPCPassword)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]map[string]string{
		"missing url":        {},
		"bad scheme":         {"BSV_RPC_URL": "ftp://rpc:21"},
		"no host":            {"BSV_RPC_URL": "http://"},
		"bad timeout":        {"BSV_RPC_URL": "http://rpc:9292", "BSV_RPC_TIMEOUT": "soon"},
		"zero timeout":       {"BSV_RPC_URL": "http://rpc:9292", "BSV_RPC_TIMEOUT": "0s"},
		"bad mempool source": {"BSV_RPC_URL": "http://rpc:9292", "BSV_MEMPOOL_SOURCE": "magic"},
		"unknown collector":  {"BSV_RPC_URL": "http://rpc:9292", "BSV_COLLECTORS": "blockchain,utxo"},
		"no collectors":      {"BSV_RPC_URL": "http://rpc:9292", "BSV_COLLECTORS": " , "},
		"unreadable pw file": {"BSV_RPC_URL": "http://rpc:9292", "BSV_RPC_PASSWORD_FILE": "/nope"},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(env(m), noFile); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestLoadRejectsURLCredentialsWithoutEcho(t *testing.T) {
	_, err := Load(env(map[string]string{"BSV_RPC_URL": "http://user:s3cret-pw@rpc:9292"}), noFile)
	if err == nil {
		t.Fatal("expected error for URL with credentials")
	}
	if strings.Contains(err.Error(), "s3cret-pw") || strings.Contains(err.Error(), "user:") {
		t.Fatalf("error echoes credentials: %v", err)
	}
	// An unparseable URL must not be echoed either: url.Parse errors quote the input.
	_, err = Load(env(map[string]string{"BSV_RPC_URL": "http://user:s3cret-pw@rpc:92 92"}), noFile)
	if err == nil || strings.Contains(err.Error(), "s3cret-pw") {
		t.Fatalf("parse error echoes credentials or is nil: %v", err)
	}
}

func TestConfigLogValueRedactsPassword(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"BSV_RPC_URL":      "http://rpc:9292",
		"BSV_RPC_USER":     "rpcuser",
		"BSV_RPC_PASSWORD": "s3cret-pw",
	}), noFile)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("starting", "config", cfg)
	out := buf.String()
	if strings.Contains(out, "s3cret-pw") {
		t.Fatalf("log line contains password: %s", out)
	}
	for _, want := range []string{`"rpc_url":"http://rpc:9292"`, `"rpc_password_set":true`, `"collectors":"blockchain,chaintips,mempool,peers"`} {
		if !strings.Contains(out, want) {
			t.Errorf("log line missing %s: %s", want, out)
		}
	}
}

func TestLoadRejectsURLQueryAndFragmentWithoutEcho(t *testing.T) {
	for _, u := range []string{"http://rpc:9292/?token=s3cret-tok", "http://rpc:9292/#s3cret-tok", "http://rpc:9292/?"} {
		_, err := Load(env(map[string]string{"BSV_RPC_URL": u}), noFile)
		if err == nil {
			t.Errorf("%q: expected error", u)
			continue
		}
		if strings.Contains(err.Error(), "s3cret-tok") {
			t.Errorf("%q: error echoes the URL: %v", u, err)
		}
	}
}
