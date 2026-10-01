// Package config loads exporter settings from the environment.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Collector names accepted in BSV_COLLECTORS, and values of BSV_MEMPOOL_SOURCE.
const (
	CollectorBlockchain = "blockchain"
	CollectorPeers      = "peers"
	CollectorMempool    = "mempool"
	CollectorChaintips  = "chaintips"

	MempoolSourceMempoolInfo     = "mempoolinfo"
	MempoolSourceMiningCandidate = "miningcandidate"
)

// MaxRPCTimeout bounds BSV_RPC_TIMEOUT. Server, client and shutdown deadlines
// add a few seconds to it, so it must stay far from time.Duration's maximum.
const MaxRPCTimeout = 5 * time.Minute

var allCollectors = []string{CollectorBlockchain, CollectorPeers, CollectorMempool, CollectorChaintips}

// Config is the validated exporter configuration.
type Config struct {
	RPCURL        string
	RPCUser       string
	RPCPassword   string
	RPCTimeout    time.Duration
	MempoolSource string
	Collectors    map[string]bool
	ListenAddr    string
}

// Load reads the configuration through getenv and readFile, which are
// os.Getenv and os.ReadFile in production.
func Load(getenv func(string) string, readFile func(string) ([]byte, error)) (Config, error) {
	cfg := Config{
		RPCUser:       getenv("BSV_RPC_USER"),
		RPCPassword:   getenv("BSV_RPC_PASSWORD"),
		RPCTimeout:    5 * time.Second,
		MempoolSource: MempoolSourceMempoolInfo,
		ListenAddr:    ":9480",
	}

	raw := getenv("BSV_RPC_URL")
	if raw == "" {
		return Config{}, errors.New("BSV_RPC_URL is required")
	}
	// The parse error is deliberately dropped: it quotes the input, which may
	// contain credentials.
	u, err := url.Parse(raw)
	if err != nil {
		return Config{}, errors.New("BSV_RPC_URL is not a valid URL")
	}
	if u.User != nil {
		return Config{}, errors.New("BSV_RPC_URL must not contain credentials; use BSV_RPC_USER and BSV_RPC_PASSWORD")
	}
	// Query strings and fragments often carry tokens, and the URL is logged.
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return Config{}, errors.New("BSV_RPC_URL must not contain a query string or fragment")
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Config{}, errors.New("BSV_RPC_URL must be an http or https URL with a host")
	}
	cfg.RPCURL = u.String()

	if path := getenv("BSV_RPC_PASSWORD_FILE"); path != "" {
		b, err := readFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("reading BSV_RPC_PASSWORD_FILE: %w", err)
		}
		cfg.RPCPassword = strings.TrimRight(string(b), "\r\n")
	}

	if v := getenv("BSV_RPC_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 || d > MaxRPCTimeout {
			return Config{}, fmt.Errorf("BSV_RPC_TIMEOUT must be a positive duration up to %v, got %q", MaxRPCTimeout, v)
		}
		cfg.RPCTimeout = d
	}

	if v := getenv("BSV_MEMPOOL_SOURCE"); v != "" {
		if v != MempoolSourceMempoolInfo && v != MempoolSourceMiningCandidate {
			return Config{}, fmt.Errorf("BSV_MEMPOOL_SOURCE must be %q or %q, got %q",
				MempoolSourceMempoolInfo, MempoolSourceMiningCandidate, v)
		}
		cfg.MempoolSource = v
	}

	cfg.Collectors, err = parseCollectors(getenv("BSV_COLLECTORS"))
	if err != nil {
		return Config{}, err
	}

	if v := getenv("LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	return cfg, nil
}

func parseCollectors(v string) (map[string]bool, error) {
	if strings.TrimSpace(v) == "" && !strings.Contains(v, ",") {
		v = strings.Join(allCollectors, ",")
	}
	known := make(map[string]bool, len(allCollectors))
	for _, c := range allCollectors {
		known[c] = true
	}
	out := map[string]bool{}
	for _, name := range strings.Split(v, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !known[name] {
			return nil, fmt.Errorf("BSV_COLLECTORS: unknown collector %q (known: %s)", name, strings.Join(allCollectors, ", "))
		}
		out[name] = true
	}
	if len(out) == 0 {
		return nil, errors.New("BSV_COLLECTORS: no collectors enabled")
	}
	return out, nil
}

// LogValue keeps the password out of logs.
func (c Config) LogValue() slog.Value {
	names := make([]string, 0, len(c.Collectors))
	for n := range c.Collectors {
		names = append(names, n)
	}
	sort.Strings(names)
	return slog.GroupValue(
		slog.String("rpc_endpoint", endpoint(c.RPCURL)),
		slog.Bool("rpc_user_set", c.RPCUser != ""),
		slog.Bool("rpc_password_set", c.RPCPassword != ""),
		slog.Duration("rpc_timeout", c.RPCTimeout),
		slog.String("mempool_source", c.MempoolSource),
		slog.String("collectors", strings.Join(names, ",")),
		slog.String("listen_addr", c.ListenAddr),
	)
}

// endpoint is the URL's scheme and host only: the path may carry a token, and
// the URL is logged.
func endpoint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
