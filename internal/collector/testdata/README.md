# RPC fixtures

`result` payloads of the JSON-RPC calls, captured from real nodes on 2026-10-01 and scrubbed. Network addresses use the documentation ranges (`192.0.2.0/24`, `198.51.100.0/24`) or `example.com`, and libp2p peer IDs are replaced. Block hashes and heights are public chain data.

| File | Node | Network | Notes |
|---|---|---|---|
| `svnode/getblockchaininfo.json` | SV Node | teratestnet | `softforks` trimmed |
| `svnode/getchaintips.json` | SV Node | teratestnet | 4 of 40 tips kept |
| `svnode/getpeerinfo.json` | SV Node | teratestnet | fields trimmed; one inbound peer added so all directions are covered |
| `svnode/getmempoolinfo.json` | SV Node | teratestnet | `size` and `bytes` set non-zero (the captured mempool was empty) |
| `svnode/getminingcandidate.json` | SV Node | teratestnet | `id` replaced |
| `teranode/getblockchaininfo.json` | Teranode | mainnet | as captured |
| `teranode/getminingcandidate.json` | Teranode | mainnet | `merkleProof` trimmed |
| `teranode/getpeerinfo.json` | Teranode | mainnet | 5 of 12 peers kept (2 legacy, 3 libp2p), addresses and peer IDs scrubbed |
| `teranode/getchaintips.json` | Teranode | teratestnet | full response; mainnet `getchaintips` does not return |

The Teranode set therefore mixes networks: chain tips come from teratestnet (active height 34345), while `getblockchaininfo` and the rest come from mainnet (969139). The collector tests treat each file on its own, so the mix doesn't affect what they check.
