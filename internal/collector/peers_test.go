package collector

import "testing"

func TestCountPeers(t *testing.T) {
	got := CountPeers([]Peer{
		{Inbound: true},                     // legacy inbound
		{Inbound: false},                    // legacy outbound
		{},                                  // Teranode legacy peer: inbound omitted means outbound
		{Inbound: true, PeerID: "12D3KooA"}, // Teranode libp2p peer: inbound means "connected"
		{PeerID: "12D3KooB"},
	})
	want := PeerCounts{Inbound: 1, Outbound: 2, P2P: 2}
	if got != want {
		t.Errorf("CountPeers = %+v, want %+v", got, want)
	}
}

func TestCountPeersEmpty(t *testing.T) {
	if got := CountPeers(nil); got != (PeerCounts{}) {
		t.Errorf("CountPeers(nil) = %+v", got)
	}
}
