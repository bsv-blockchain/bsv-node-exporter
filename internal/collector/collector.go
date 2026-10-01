// Package collector exposes SV Node / Teranode RPC data as Prometheus metrics.
package collector

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/bsv-blockchain/bsv-node-exporter/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

// Caller performs one JSON-RPC call. *rpc.Client implements it.
type Caller interface {
	Call(ctx context.Context, method string, out any) error
}

// Options configures a Collector.
type Options struct {
	Timeout       time.Duration
	MempoolSource string
	Enabled       map[string]bool
}

var (
	descRPCUp = prometheus.NewDesc("bsv_rpc_up",
		"Whether the RPC call for this method succeeded during this scrape (1) or failed (0).", []string{"method"}, nil)
	descRPCDuration = prometheus.NewDesc("bsv_rpc_duration_seconds",
		"Wall time of the RPC call made during this scrape.", []string{"method"}, nil)
	descBlocks = prometheus.NewDesc("bsv_blocks",
		"Height of the active chain, from getblockchaininfo.", nil, nil)
	descHeaders = prometheus.NewDesc("bsv_headers",
		"Number of validated headers, from getblockchaininfo.", nil, nil)
	descDifficulty = prometheus.NewDesc("bsv_difficulty",
		"Current proof-of-work difficulty, from getblockchaininfo.", nil, nil)
	descPeers = prometheus.NewDesc("bsv_peers",
		"Connected peers by kind: inbound or outbound legacy peers, or p2p for Teranode libp2p peers.", []string{"kind"}, nil)
	descMempoolTxs = prometheus.NewDesc("bsv_mempool_txs",
		"Transactions in the mempool, from getmempoolinfo.size or getminingcandidate.num_tx.", nil, nil)
	descMempoolBytes = prometheus.NewDesc("bsv_mempool_bytes",
		"Mempool size in bytes, from getmempoolinfo.bytes.", nil, nil)
	descChaintips = prometheus.NewDesc("bsv_chaintips",
		"Known chain tips by status.", []string{"status"}, nil)
	descForks = prometheus.NewDesc("bsv_chaintip_forks",
		"Non-active chain tips at most window blocks below the active tip, by branch length (single: 1, long: more than 1).",
		[]string{"window", "length"}, nil)

	allDescs = []*prometheus.Desc{descRPCUp, descRPCDuration, descBlocks, descHeaders, descDifficulty,
		descPeers, descMempoolTxs, descMempoolBytes, descChaintips, descForks}
)

type task struct {
	method string
	run    func(ctx context.Context) ([]prometheus.Metric, error)
}

// Collector runs the enabled RPC calls on every scrape.
type Collector struct {
	caller Caller
	opts   Options
	logger *slog.Logger
	tasks  []task
}

// New builds a Collector for the collectors enabled in opts.
func New(caller Caller, opts Options, logger *slog.Logger) *Collector {
	c := &Collector{caller: caller, opts: opts, logger: logger}
	if opts.Enabled[config.CollectorBlockchain] {
		c.tasks = append(c.tasks, task{"getblockchaininfo", c.blockchain})
	}
	if opts.Enabled[config.CollectorPeers] {
		c.tasks = append(c.tasks, task{"getpeerinfo", c.peers})
	}
	if opts.Enabled[config.CollectorMempool] {
		if opts.MempoolSource == config.MempoolSourceMiningCandidate {
			c.tasks = append(c.tasks, task{"getminingcandidate", c.miningCandidate})
		} else {
			c.tasks = append(c.tasks, task{"getmempoolinfo", c.mempoolInfo})
		}
	}
	if opts.Enabled[config.CollectorChaintips] {
		c.tasks = append(c.tasks, task{"getchaintips", c.chaintips})
	}
	return c
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range allDescs {
		ch <- d
	}
}

// Collect implements prometheus.Collector. Calls run concurrently, each under
// opts.Timeout. A failed call reports bsv_rpc_up 0 and omits its series.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	var wg sync.WaitGroup
	for _, t := range c.tasks {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), c.opts.Timeout)
			defer cancel()
			start := time.Now()
			metrics, err := t.run(ctx)
			elapsed := time.Since(start).Seconds()
			up := 1.0
			if err != nil {
				up = 0
				c.logger.Warn("rpc call failed", "method", t.method, "error", err.Error())
			} else {
				for _, m := range metrics {
					ch <- m
				}
			}
			ch <- gauge(descRPCUp, up, t.method)
			ch <- gauge(descRPCDuration, elapsed, t.method)
		})
	}
	wg.Wait()
}

func gauge(d *prometheus.Desc, v float64, labels ...string) prometheus.Metric {
	return prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
}

var errMissingField = errors.New("response is missing a required field")

func (c *Collector) blockchain(ctx context.Context) ([]prometheus.Metric, error) {
	var r struct {
		Blocks     *float64 `json:"blocks"`
		Headers    *float64 `json:"headers"`
		Difficulty *float64 `json:"difficulty"`
	}
	if err := c.caller.Call(ctx, "getblockchaininfo", &r); err != nil {
		return nil, err
	}
	if r.Blocks == nil || r.Headers == nil || r.Difficulty == nil {
		return nil, errMissingField
	}
	return []prometheus.Metric{
		gauge(descBlocks, *r.Blocks),
		gauge(descHeaders, *r.Headers),
		gauge(descDifficulty, *r.Difficulty),
	}, nil
}

func (c *Collector) peers(ctx context.Context) ([]prometheus.Metric, error) {
	var peers []Peer
	if err := c.caller.Call(ctx, "getpeerinfo", &peers); err != nil {
		return nil, err
	}
	n := CountPeers(peers)
	return []prometheus.Metric{
		gauge(descPeers, float64(n.Inbound), "inbound"),
		gauge(descPeers, float64(n.Outbound), "outbound"),
		gauge(descPeers, float64(n.P2P), "p2p"),
	}, nil
}

func (c *Collector) mempoolInfo(ctx context.Context) ([]prometheus.Metric, error) {
	var r struct {
		Size  *float64 `json:"size"`
		Bytes *float64 `json:"bytes"`
	}
	if err := c.caller.Call(ctx, "getmempoolinfo", &r); err != nil {
		return nil, err
	}
	if r.Size == nil || r.Bytes == nil {
		return nil, errMissingField
	}
	return []prometheus.Metric{gauge(descMempoolTxs, *r.Size), gauge(descMempoolBytes, *r.Bytes)}, nil
}

func (c *Collector) miningCandidate(ctx context.Context) ([]prometheus.Metric, error) {
	var r struct {
		NumTx *float64 `json:"num_tx"`
	}
	if err := c.caller.Call(ctx, "getminingcandidate", &r); err != nil {
		return nil, err
	}
	if r.NumTx == nil {
		return nil, errMissingField
	}
	return []prometheus.Metric{gauge(descMempoolTxs, *r.NumTx)}, nil
}

func (c *Collector) chaintips(ctx context.Context) ([]prometheus.Metric, error) {
	var tips []ChainTip
	if err := c.caller.Call(ctx, "getchaintips", &tips); err != nil {
		return nil, err
	}
	s := SummarizeTips(tips)
	out := make([]prometheus.Metric, 0, len(Statuses)+2*len(ForkWindows))
	for _, st := range Statuses {
		out = append(out, gauge(descChaintips, float64(s.ByStatus[st]), st))
	}
	for _, w := range ForkWindows {
		fc := s.Forks[w]
		win := strconv.FormatInt(w, 10)
		out = append(out, gauge(descForks, float64(fc.Single), win, "single"), gauge(descForks, float64(fc.Long), win, "long"))
	}
	return out, nil
}
