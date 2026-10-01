package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
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

func TestFailedCallLogLineIsBounded(t *testing.T) {
	// Internal security audit, finding 2 (preventive control): whatever error a
	// call returns, the logged text has one small cap and no control characters.
	long := errors.New("x\n\x1b[31m" + strings.Repeat("y", 10000))
	var buf bytes.Buffer
	c := New(fakeCaller{errs: map[string]error{"getblockchaininfo": long}}, Options{
		Timeout: time.Second, Enabled: map[string]bool{config.CollectorBlockchain: true},
	}, slog.New(slog.NewJSONHandler(&buf, nil)))
	testutil.CollectAndCount(c)
	var rec struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line: %v: %s", err, buf.String())
	}
	if len(rec.Error) > maxLoggedError+3 {
		t.Errorf("logged error is %d bytes, cap is %d", len(rec.Error), maxLoggedError)
	}
	if strings.ContainsAny(rec.Error, "\n\x1b") {
		t.Errorf("logged error contains control characters: %q", rec.Error[:40])
	}
}

// allocatedDuring reports the bytes allocated while f runs.
func allocatedDuring(f func()) uint64 {
	runtime.GC()
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

func TestDenseArraysAreRejectedWithoutAmplification(t *testing.T) {
	// Internal security audit, finding 4: millions of tiny elements under the
	// byte budget decoded into hundreds of MiB (5.7 MiB of peers -> 181.6 MiB).
	cases := []struct {
		name, collector, method, elem string
		n                             int
		maxAlloc                      uint64
	}{
		// Baseline for both: the fake reads the file and copies it once (2x body).
		{"peers", config.CollectorPeers, "getpeerinfo", `{}`, 2_000_000, 24 << 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "[" + strings.TrimSuffix(strings.Repeat(tc.elem+",", tc.n), ",") + "]"
			c := collectFrom(t, tc.collector, tc.method, body)
			var up int
			alloc := allocatedDuring(func() { up = testutil.CollectAndCount(c, "bsv_rpc_up") })
			if up != 1 {
				t.Fatalf("bsv_rpc_up series = %d", up)
			}
			want := "\n# HELP bsv_rpc_up Whether the RPC call for this method succeeded during this scrape (1) or failed (0).\n# TYPE bsv_rpc_up gauge\nbsv_rpc_up{method=\"" + tc.method + "\"} 0\n"
			if err := testutil.CollectAndCompare(c, strings.NewReader(want), "bsv_rpc_up"); err != nil {
				t.Errorf("an array over the element cap must fail the call: %v", err)
			}
			if alloc > tc.maxAlloc {
				t.Errorf("allocated %.1f MiB for a %.1f MiB body, ceiling %.1f MiB", float64(alloc)/(1<<20), float64(len(body))/(1<<20), float64(tc.maxAlloc)/(1<<20))
			}
		})
	}
}

func TestArraysAtTheCapAreAccepted(t *testing.T) {
	c := collectFrom(t, config.CollectorPeers, "getpeerinfo", "["+strings.TrimSuffix(strings.Repeat(`{},`, MaxPeers), ",")+"]")
	if n := testutil.CollectAndCount(c, "bsv_peers"); n != 3 {
		t.Errorf("exactly MaxPeers peers must be accepted, got %d bsv_peers series", n)
	}
}

func TestChainTipDecodeCostStopsAtTheCap(t *testing.T) {
	// Internal security audit, finding 4: past MaxChainTips, a larger array must
	// not cost more to process. Measured as allocation minus the fake caller's
	// own two copies of the body.
	elem := `{"height":1,"branchlen":1,"status":"valid-fork"}`
	cost := func(n int) (float64, bool) {
		body := "[" + strings.TrimSuffix(strings.Repeat(elem+",", n), ",") + "]"
		c := collectFrom(t, config.CollectorChaintips, "getchaintips", body)
		var forks int
		alloc := allocatedDuring(func() { forks = testutil.CollectAndCount(c, "bsv_chaintip_forks") })
		return (float64(alloc) - 2*float64(len(body))) / (1 << 20), forks == 0
	}
	small, rejectedSmall := cost(2 * MaxChainTips)
	large, rejectedLarge := cost(4 * MaxChainTips)
	if !rejectedSmall || !rejectedLarge {
		t.Fatal("arrays over MaxChainTips must fail the call")
	}
	if large-small > 6 {
		t.Errorf("decode cost grew from %.1f to %.1f MiB when the array doubled past the cap", small, large)
	}
}
