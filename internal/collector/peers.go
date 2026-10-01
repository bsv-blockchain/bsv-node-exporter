package collector

// Peer holds the getpeerinfo fields the exporter reads.
type Peer struct {
	Inbound bool   `json:"inbound"`
	PeerID  string `json:"peerid"`
}

// PeerCounts is the number of connected peers by kind.
type PeerCounts struct {
	Inbound  int
	Outbound int
	P2P      int
}

// CountPeers classifies peers. A non-empty PeerID marks a Teranode libp2p
// peer, whose inbound flag means "connected" rather than direction, so it
// counts as P2P. Other peers count by their inbound flag; Teranode omits
// inbound=false, so a missing flag counts as outbound.
func CountPeers(peers []Peer) PeerCounts {
	var c PeerCounts
	for _, p := range peers {
		c.add(p)
	}
	return c
}

func (c *PeerCounts) add(p Peer) {
	switch {
	case p.PeerID != "":
		c.P2P++
	case p.Inbound:
		c.Inbound++
	default:
		c.Outbound++
	}
}
