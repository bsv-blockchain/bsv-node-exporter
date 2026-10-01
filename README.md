# bsv-node-exporter

A Prometheus exporter for BSV nodes: [SV Node](https://github.com/bitcoin-sv/bitcoin-sv) and [Teranode](https://github.com/bsv-blockchain/teranode). On every scrape it makes up to four JSON-RPC calls from a fixed allowlist, and turns them into about 30 node-health, fork and mempool series per node. It is scrape-only and stateless.

## Configuration

Configuration is read from environment variables only.

| Variable | Default | Meaning |
|---|---|---|
| `BSV_RPC_URL` | required | e.g. `http://rpc:9292`. Must not contain credentials |
| `BSV_RPC_USER` / `BSV_RPC_PASSWORD` | empty | RPC credentials |
| `BSV_RPC_PASSWORD_FILE` | empty | Read the password from this file instead; takes precedence over `BSV_RPC_PASSWORD` |
| `BSV_RPC_TIMEOUT` | `5s` | Timeout for each RPC call. Keep it below the scraper's `scrape_timeout` (default 10s) |
| `BSV_MEMPOOL_SOURCE` | `mempoolinfo` | `mempoolinfo` or `miningcandidate`. Teranode needs `miningcandidate`: its `getmempoolinfo` is unimplemented |
| `BSV_COLLECTORS` | `blockchain,peers,mempool,chaintips` | Comma-separated collectors to enable |
| `LISTEN_ADDR` | `:9480` | HTTP listen address |

The exporter serves `GET /metrics` and `GET /healthz`. `/healthz` never calls the node. At most two `/metrics` scrapes run at once; further concurrent scrapes get HTTP 503, so the exporter cannot multiply load on the node. Node RPC never goes through `HTTP_PROXY` / `HTTPS_PROXY`.

## Metrics

| Series | Type | Source |
|---|---|---|
| `bsv_rpc_up{method}` | gauge 0/1 | 1 if the call succeeded during this scrape |
| `bsv_rpc_duration_seconds{method}` | gauge | wall time of the call during this scrape |
| `bsv_blocks` | gauge | `getblockchaininfo.blocks` |
| `bsv_headers` | gauge | `getblockchaininfo.headers` |
| `bsv_difficulty` | gauge | `getblockchaininfo.difficulty` |
| `bsv_peers{kind}` | gauge | `getpeerinfo`, counted as `inbound` / `outbound` / `p2p` |
| `bsv_mempool_txs` | gauge | `getmempoolinfo.size`; with `BSV_MEMPOOL_SOURCE=mempoolinfo` |
| `bsv_mining_candidate_txs` | gauge | `getminingcandidate.num_tx`, coinbase included; with `BSV_MEMPOOL_SOURCE=miningcandidate` |
| `bsv_mempool_bytes` | gauge | `getmempoolinfo.bytes`; with `BSV_MEMPOOL_SOURCE=mempoolinfo` |
| `bsv_chaintips{status}` | gauge | `getchaintips`, counted by `status` |
| `bsv_chaintip_forks{window,length}` | gauge | see below |
| `bsv_exporter_build_info{version,goversion}` | gauge | always 1 |

A failed call sets `bsv_rpc_up{method}` to 0 and omits that call's series. A scrape takes at most about `BSV_RPC_TIMEOUT`, so as long as that is below the scraper's `scrape_timeout`, one hung call costs only its own series. The rest of the scrape is still served, with HTTP 200. Alert on `bsv_rpc_up == 0` for "node unreachable", and on the scraper's own `up` for "exporter down".

**Peer kinds.** A peer with a non-empty `peerid` is a Teranode libp2p peer and counts as `p2p`. Teranode sets `inbound` on those peers to mean "connected", not direction, so direction is ignored for them. Every other peer counts as `inbound` when `inbound` is true, and as `outbound` when it is false or missing. Teranode omits `inbound: false` from legacy peers. All three kinds are always emitted.

**Statuses.** The five statuses SV Node and Teranode return (`active`, `valid-fork`, `valid-headers`, `headers-only`, `invalid`) are always emitted, at 0 when absent. Any other value counts as `other`, so a node cannot create arbitrary label values.

**Fork buckets.**

- The active height is the highest `height` among tips with status `active`.
- A fork is a non-`active` tip with `0 <= active height − height <= window`, for `window` 144 and 10000. Tips above the active height are headers ahead of validation, not forks, and are not counted (they still count in `bsv_chaintips{status}`).
- `length="single"` counts tips with `branchlen == 1`.
- `length="long"` counts tips with `branchlen > 1`.

All four buckets are always emitted, at 0 when empty. With no `active` tip, all four are 0.

The exporter adds no deployment labels such as network, host or node type. Attach them in the scrape configuration, so a single build works everywhere.

## Teranode notes

Recommended Teranode settings:

```sh
BSV_MEMPOOL_SOURCE=miningcandidate
BSV_COLLECTORS=blockchain,peers,mempool
```

- `miningcandidate` is required, because Teranode does not implement `getmempoolinfo`.
- Leave the `chaintips` collector off for Teranode. On long chains its `getchaintips` may not return at all, so every scrape would start another expensive call that only ends at `BSV_RPC_TIMEOUT`.
- `getminingcandidate` is not strictly read-only: the node builds and caches a candidate. At normal scrape intervals that is harmless.

## Running

### Docker

```sh
docker run --read-only -p 9480:9480 \
  -e BSV_RPC_URL=http://node:8332 \
  -e BSV_RPC_USER=exporter \
  -e BSV_RPC_PASSWORD_FILE=/run/secrets/rpc-password \
  -v "$PWD/rpc-password:/run/secrets/rpc-password:ro" \
  ghcr.io/bsv-blockchain/bsv-node-exporter:<version>
```

The image is distroless, runs as UID 65532 and needs no writable filesystem.

### systemd

`/etc/bsv-node-exporter.env` holds the non-secret settings:

```sh
BSV_RPC_URL=http://127.0.0.1:8332
BSV_RPC_USER=exporter
LISTEN_ADDR=127.0.0.1:9480
```

`/etc/systemd/system/bsv-node-exporter.service`:

```ini
[Unit]
Description=BSV node Prometheus exporter
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/bsv-node-exporter
EnvironmentFile=/etc/bsv-node-exporter.env
LoadCredential=rpc-password:/etc/bsv-node-exporter/rpc-password
Environment=BSV_RPC_PASSWORD_FILE=%d/rpc-password
DynamicUser=yes
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
RestrictAddressFamilies=AF_INET AF_INET6
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

### Scraping

With Prometheus, attach deployment labels on the target:

```yaml
scrape_configs:
  - job_name: bsv-node
    scrape_interval: 60s
    static_configs:
      - targets: ["localhost:9480"]
        labels:
          network: mainnet
```

With the OpenTelemetry Collector (contrib), add a `prometheus` receiver to a metrics pipeline. A `resource` processor in the same pipeline can attach deployment attributes:

```yaml
receivers:
  prometheus/bsv-node:
    config:
      scrape_configs:
        - job_name: bsv-node
          scrape_interval: 60s
          static_configs:
            - targets: ["localhost:9480"]
```

## Security

- Credentials never appear in logs, metric labels or error messages, and a `BSV_RPC_URL` containing credentials is rejected.
- Only `getblockchaininfo`, `getpeerinfo`, `getmempoolinfo`, `getminingcandidate` and `getchaintips` can be called. There is no RPC passthrough.
- RPC calls never follow redirects, and responses larger than 32 MiB are rejected.
- The HTTP server sets read, write and header timeouts, and serves only `/metrics` and `/healthz`.
- The only direct dependencies are the Go standard library and `prometheus/client_golang`.
- CI runs `govulncheck` and `golangci-lint`, and release images are scanned with trivy.

Report vulnerabilities privately through GitHub's private vulnerability reporting on this repository, not in a public issue.

## License

Apache-2.0. See [LICENSE](LICENSE).
