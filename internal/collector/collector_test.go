package collector

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bsv-blockchain/bsv-node-exporter/internal/config"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type fakeCaller struct {
	dir   string
	errs  map[string]error
	block map[string]bool
}

func (f fakeCaller) Call(ctx context.Context, method string, out any) error {
	if f.block[method] {
		<-ctx.Done()
		return ctx.Err()
	}
	if err, ok := f.errs[method]; ok {
		return err
	}
	b, err := os.ReadFile(filepath.Join(f.dir, method+".json"))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func allEnabled() map[string]bool {
	return map[string]bool{
		config.CollectorBlockchain: true, config.CollectorPeers: true,
		config.CollectorMempool: true, config.CollectorChaintips: true,
	}
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Everything except bsv_rpc_duration_seconds, whose values vary.
var stableNames = []string{
	"bsv_rpc_up", "bsv_blocks", "bsv_headers", "bsv_difficulty", "bsv_peers",
	"bsv_mempool_txs", "bsv_mempool_bytes", "bsv_mining_candidate_txs", "bsv_chaintips", "bsv_chaintip_forks",
}

const svnodeExpected = `
# HELP bsv_blocks Height of the active chain, from getblockchaininfo.
# TYPE bsv_blocks gauge
bsv_blocks 34346
# HELP bsv_chaintip_forks Non-active chain tips at most window blocks below the active tip (none above it), by branch length (single: 1, long: more than 1).
# TYPE bsv_chaintip_forks gauge
bsv_chaintip_forks{length="long",window="10000"} 1
bsv_chaintip_forks{length="long",window="144"} 0
bsv_chaintip_forks{length="single",window="10000"} 2
bsv_chaintip_forks{length="single",window="144"} 2
# HELP bsv_chaintips Known chain tips by status.
# TYPE bsv_chaintips gauge
bsv_chaintips{status="active"} 1
bsv_chaintips{status="headers-only"} 0
bsv_chaintips{status="invalid"} 0
bsv_chaintips{status="other"} 0
bsv_chaintips{status="valid-fork"} 3
bsv_chaintips{status="valid-headers"} 0
# HELP bsv_difficulty Current proof-of-work difficulty, from getblockchaininfo.
# TYPE bsv_difficulty gauge
bsv_difficulty 1
# HELP bsv_headers Number of validated headers, from getblockchaininfo.
# TYPE bsv_headers gauge
bsv_headers 34346
# HELP bsv_mempool_bytes Mempool size in bytes, from getmempoolinfo.bytes.
# TYPE bsv_mempool_bytes gauge
bsv_mempool_bytes 734211
# HELP bsv_mempool_txs Transactions in the mempool, from getmempoolinfo.size.
# TYPE bsv_mempool_txs gauge
bsv_mempool_txs 1520
# HELP bsv_peers Connected peers by kind: inbound or outbound legacy peers, or p2p for Teranode libp2p peers.
# TYPE bsv_peers gauge
bsv_peers{kind="inbound"} 1
bsv_peers{kind="outbound"} 2
bsv_peers{kind="p2p"} 0
# HELP bsv_rpc_up Whether the RPC call for this method succeeded during this scrape (1) or failed (0).
# TYPE bsv_rpc_up gauge
bsv_rpc_up{method="getblockchaininfo"} 1
bsv_rpc_up{method="getchaintips"} 1
bsv_rpc_up{method="getmempoolinfo"} 1
bsv_rpc_up{method="getpeerinfo"} 1
`

func TestCollectSVNode(t *testing.T) {
	c := New(fakeCaller{dir: "testdata/svnode"}, Options{
		Timeout: time.Second, MempoolSource: config.MempoolSourceMempoolInfo, Enabled: allEnabled(),
	}, discard())
	if err := testutil.CollectAndCompare(c, strings.NewReader(svnodeExpected), stableNames...); err != nil {
		t.Fatal(err)
	}
}

const teranodeExpected = `
# HELP bsv_blocks Height of the active chain, from getblockchaininfo.
# TYPE bsv_blocks gauge
bsv_blocks 969139
# HELP bsv_chaintip_forks Non-active chain tips at most window blocks below the active tip (none above it), by branch length (single: 1, long: more than 1).
# TYPE bsv_chaintip_forks gauge
bsv_chaintip_forks{length="long",window="10000"} 2
bsv_chaintip_forks{length="long",window="144"} 0
bsv_chaintip_forks{length="single",window="10000"} 10
bsv_chaintip_forks{length="single",window="144"} 4
# HELP bsv_chaintips Known chain tips by status.
# TYPE bsv_chaintips gauge
bsv_chaintips{status="active"} 1
bsv_chaintips{status="headers-only"} 0
bsv_chaintips{status="invalid"} 0
bsv_chaintips{status="other"} 0
bsv_chaintips{status="valid-fork"} 0
bsv_chaintips{status="valid-headers"} 12
# HELP bsv_difficulty Current proof-of-work difficulty, from getblockchaininfo.
# TYPE bsv_difficulty gauge
bsv_difficulty 2.9549431076597343e+10
# HELP bsv_headers Number of validated headers, from getblockchaininfo.
# TYPE bsv_headers gauge
bsv_headers 969139
# HELP bsv_mining_candidate_txs Transactions in the current mining candidate, coinbase included, from getminingcandidate.num_tx.
# TYPE bsv_mining_candidate_txs gauge
bsv_mining_candidate_txs 58
# HELP bsv_peers Connected peers by kind: inbound or outbound legacy peers, or p2p for Teranode libp2p peers.
# TYPE bsv_peers gauge
bsv_peers{kind="inbound"} 0
bsv_peers{kind="outbound"} 2
bsv_peers{kind="p2p"} 3
# HELP bsv_rpc_up Whether the RPC call for this method succeeded during this scrape (1) or failed (0).
# TYPE bsv_rpc_up gauge
bsv_rpc_up{method="getblockchaininfo"} 1
bsv_rpc_up{method="getchaintips"} 1
bsv_rpc_up{method="getminingcandidate"} 1
bsv_rpc_up{method="getpeerinfo"} 1
`

func TestCollectTeranodeMiningCandidate(t *testing.T) {
	c := New(fakeCaller{dir: "testdata/teranode"}, Options{
		Timeout: time.Second, MempoolSource: config.MempoolSourceMiningCandidate, Enabled: allEnabled(),
	}, discard())
	if err := testutil.CollectAndCompare(c, strings.NewReader(teranodeExpected), stableNames...); err != nil {
		t.Fatal(err)
	}
}

func TestCollectPartialFailure(t *testing.T) {
	c := New(fakeCaller{dir: "testdata/svnode", errs: map[string]error{"getpeerinfo": errors.New("boom")}}, Options{
		Timeout: time.Second, MempoolSource: config.MempoolSourceMempoolInfo, Enabled: allEnabled(),
	}, discard())
	if n := testutil.CollectAndCount(c, "bsv_peers"); n != 0 {
		t.Errorf("bsv_peers emitted %d series after failure, want 0", n)
	}
	if n := testutil.CollectAndCount(c, "bsv_blocks"); n != 1 {
		t.Errorf("bsv_blocks = %d series, want 1", n)
	}
	const want = `
# HELP bsv_rpc_up Whether the RPC call for this method succeeded during this scrape (1) or failed (0).
# TYPE bsv_rpc_up gauge
bsv_rpc_up{method="getblockchaininfo"} 1
bsv_rpc_up{method="getchaintips"} 1
bsv_rpc_up{method="getmempoolinfo"} 1
bsv_rpc_up{method="getpeerinfo"} 0
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(want), "bsv_rpc_up"); err != nil {
		t.Fatal(err)
	}
}

func TestCollectNodeUnreachable(t *testing.T) {
	down := errors.New("connection refused")
	c := New(fakeCaller{errs: map[string]error{
		"getblockchaininfo": down, "getpeerinfo": down, "getmempoolinfo": down, "getchaintips": down,
	}}, Options{Timeout: time.Second, MempoolSource: config.MempoolSourceMempoolInfo, Enabled: allEnabled()}, discard())
	if n := testutil.CollectAndCount(c); n != 8 {
		t.Errorf("got %d series, want 8 (bsv_rpc_up and bsv_rpc_duration_seconds for 4 methods)", n)
	}
}

func TestCollectTimeoutBoundsScrape(t *testing.T) {
	c := New(fakeCaller{dir: "testdata/svnode", block: map[string]bool{"getchaintips": true}}, Options{
		Timeout: 100 * time.Millisecond, MempoolSource: config.MempoolSourceMempoolInfo, Enabled: allEnabled(),
	}, discard())
	start := time.Now()
	n := testutil.CollectAndCount(c, "bsv_blocks")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("scrape took %v with a 100ms timeout", elapsed)
	}
	if n != 1 {
		t.Errorf("bsv_blocks missing while getchaintips hung")
	}
	if n := testutil.CollectAndCount(c, "bsv_chaintips"); n != 0 {
		t.Errorf("bsv_chaintips emitted %d series for a timed-out call", n)
	}
}

func TestCollectMissingFieldIsFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "getblockchaininfo.json"), []byte(`{"headers":5,"difficulty":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c := New(fakeCaller{dir: dir}, Options{
		Timeout: time.Second, Enabled: map[string]bool{config.CollectorBlockchain: true},
	}, discard())
	if n := testutil.CollectAndCount(c, "bsv_blocks", "bsv_headers"); n != 0 {
		t.Errorf("emitted %d series from a response missing blocks, want 0", n)
	}
	const want = `
# HELP bsv_rpc_up Whether the RPC call for this method succeeded during this scrape (1) or failed (0).
# TYPE bsv_rpc_up gauge
bsv_rpc_up{method="getblockchaininfo"} 0
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(want), "bsv_rpc_up"); err != nil {
		t.Fatal(err)
	}
}

func TestCollectOnlyEnabled(t *testing.T) {
	c := New(fakeCaller{dir: "testdata/teranode"}, Options{
		Timeout: time.Second, MempoolSource: config.MempoolSourceMiningCandidate,
		Enabled: map[string]bool{config.CollectorBlockchain: true, config.CollectorPeers: true},
	}, discard())
	if n := testutil.CollectAndCount(c, "bsv_chaintips", "bsv_chaintip_forks", "bsv_mempool_txs", "bsv_mining_candidate_txs"); n != 0 {
		t.Errorf("disabled collectors emitted %d series", n)
	}
	if n := testutil.CollectAndCount(c, "bsv_rpc_up"); n != 2 {
		t.Errorf("bsv_rpc_up = %d series, want 2", n)
	}
}

func TestCollectLint(t *testing.T) {
	c := New(fakeCaller{dir: "testdata/svnode"}, Options{
		Timeout: time.Second, MempoolSource: config.MempoolSourceMempoolInfo, Enabled: allEnabled(),
	}, discard())
	problems, err := testutil.CollectAndLint(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Errorf("lint: %s: %s", p.Metric, p.Text)
	}
}

// collectFrom runs one collector against a single hand-written response.
func collectFrom(t *testing.T, collectorName, method, body string) *Collector {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, method+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return New(fakeCaller{dir: dir}, Options{Timeout: time.Second, Enabled: map[string]bool{collectorName: true}}, discard())
}

func TestCollectMalformedArrayElementsAreFailures(t *testing.T) {
	cases := []struct {
		name, collector, method, body, series string
	}{
		{"null peer", config.CollectorPeers, "getpeerinfo", `[{"inbound":true},null]`, "bsv_peers"},
		{"tip without height", config.CollectorChaintips, "getchaintips",
			`[{"height":10,"hash":"a","branchlen":0,"status":"active"},{"hash":"b","branchlen":1,"status":"valid-fork"}]`, "bsv_chaintip_forks"},
		{"tip without branchlen", config.CollectorChaintips, "getchaintips",
			`[{"height":10,"hash":"a","branchlen":0,"status":"active"},{"height":9,"hash":"b","status":"valid-fork"}]`, "bsv_chaintip_forks"},
		{"tip without status", config.CollectorChaintips, "getchaintips",
			`[{"height":10,"hash":"a","branchlen":0,"status":"active"},{"height":9,"hash":"b","branchlen":1}]`, "bsv_chaintip_forks"},
		{"null tip", config.CollectorChaintips, "getchaintips",
			`[{"height":10,"hash":"a","branchlen":0,"status":"active"},null]`, "bsv_chaintips"},
		{"object instead of peer array", config.CollectorPeers, "getpeerinfo", `{}`, "bsv_peers"},
		{"object instead of tip array", config.CollectorChaintips, "getchaintips", `{}`, "bsv_chaintips"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := collectFrom(t, tc.collector, tc.method, tc.body)
			if n := testutil.CollectAndCount(c, tc.series); n != 0 {
				t.Errorf("%s emitted %d series from malformed data, want 0", tc.series, n)
			}
			want := "\n# HELP bsv_rpc_up Whether the RPC call for this method succeeded during this scrape (1) or failed (0).\n# TYPE bsv_rpc_up gauge\nbsv_rpc_up{method=\"" + tc.method + "\"} 0\n"
			if err := testutil.CollectAndCompare(c, strings.NewReader(want), "bsv_rpc_up"); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestCollectConcurrentScrapes(t *testing.T) {
	// The server allows two scrapes at once, so one Collector is gathered concurrently.
	c := New(fakeCaller{dir: "testdata/svnode"}, Options{
		Timeout: time.Second, MempoolSource: config.MempoolSourceMempoolInfo, Enabled: allEnabled(),
	}, discard())
	var wg sync.WaitGroup
	counts := make([]int, 8)
	for i := range counts {
		wg.Go(func() { counts[i] = testutil.CollectAndCount(c) })
	}
	wg.Wait()
	for i, n := range counts {
		if n != counts[0] || n == 0 {
			t.Fatalf("scrape %d returned %d series, scrape 0 returned %d", i, n, counts[0])
		}
	}
}

type panickingCaller struct{ fakeCaller }

func (p panickingCaller) Call(ctx context.Context, method string, out any) error {
	if method == "getpeerinfo" {
		panic("boom")
	}
	return p.fakeCaller.Call(ctx, method, out)
}

func TestCollectSurvivesAPanickingCollector(t *testing.T) {
	// A panic in a worker goroutine bypasses net/http's recovery and kills the process.
	c := New(panickingCaller{fakeCaller{dir: "testdata/svnode"}}, Options{
		Timeout: time.Second, MempoolSource: config.MempoolSourceMempoolInfo, Enabled: allEnabled(),
	}, discard())
	const want = `
# HELP bsv_rpc_up Whether the RPC call for this method succeeded during this scrape (1) or failed (0).
# TYPE bsv_rpc_up gauge
bsv_rpc_up{method="getblockchaininfo"} 1
bsv_rpc_up{method="getchaintips"} 1
bsv_rpc_up{method="getmempoolinfo"} 1
bsv_rpc_up{method="getpeerinfo"} 0
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(want), "bsv_rpc_up"); err != nil {
		t.Fatal(err)
	}
}
