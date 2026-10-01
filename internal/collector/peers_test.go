package collector

import "testing"

func TestPeerCountsAdd(t *testing.T) {
	var got PeerCounts
	for _, p := range []Peer{
		{Inbound: true},                     // legacy inbound
		{Inbound: false},                    // legacy outbound
		{},                                  // Teranode legacy peer: inbound omitted means outbound
		{Inbound: true, PeerID: "12D3KooA"}, // Teranode libp2p peer: inbound means "connected"
		{PeerID: "12D3KooB"},
	} {
		got.add(p)
	}
	if want := (PeerCounts{Inbound: 1, Outbound: 2, P2P: 2}); got != want {
		t.Errorf("PeerCounts = %+v, want %+v", got, want)
	}
}
