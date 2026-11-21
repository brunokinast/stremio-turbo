package torrent

import (
	"io"
	"net"
	"os"
	"testing"
	"time"

	qt "github.com/go-quicktest/qt"

	"github.com/anacrolix/torrent/internal/testutil"
)

// A failed outgoing attempt reports each transport's dial, the timeout it used and the half-open
// counts when it started; offered addresses report whether they were new.
func TestOutgoingConnResultDials(t *testing.T) {
	cfg := TestingConfig(t)
	results := make(chan OutgoingConnResultEvent, 4)
	cfg.Callbacks.OutgoingConnResult = append(cfg.Callbacks.OutgoingConnResult, func(ev OutgoingConnResultEvent) {
		select { // called with the client lock held: never block
		case results <- ev:
		default:
		}
	})
	var added []PeerAddedEvent
	cfg.Callbacks.PeerAdded = append(cfg.Callbacks.PeerAdded, func(ev PeerAddedEvent) {
		added = append(added, ev) // called with the client lock held
	})
	cl, err := NewClient(cfg)
	qt.Assert(t, qt.IsNil(err))
	defer cl.Close()
	tor, _ := cl.AddTorrentInfoHash(testingTorrentInfoHash)
	// Nothing listens on port 1, so the dials fail quickly.
	addr := ipPortAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
	tor.AddPeers([]PeerInfo{{Addr: addr, Source: PeerSourceDirect}})
	select {
	case ev := <-results:
		qt.Check(t, qt.IsNotNil(ev.Err))
		qt.Check(t, qt.IsTrue(ev.DialTimeout > 0))
		qt.Check(t, qt.IsTrue(ev.HalfOpenTorrent >= 1))
		qt.Check(t, qt.IsTrue(ev.HalfOpenClient >= 1))
		qt.Assert(t, qt.Equals(len(ev.Dials), len(cl.dialers)))
		for _, d := range ev.Dials {
			qt.Check(t, qt.Not(qt.Equals(d.Network, "")))
			qt.Check(t, qt.IsNotNil(d.Err))
		}
		qt.Check(t, qt.HasLen(ev.Handshakes, 0))
	case <-time.After(30 * time.Second):
		t.Fatal("no OutgoingConnResult event")
	}
	cl.lock()
	defer cl.unlock()
	qt.Assert(t, qt.HasLen(added, 1))
	qt.Check(t, qt.Equals(added[0].Outcome, PeerAddedNew))
	qt.Check(t, qt.Equals(added[0].Peer.Source, PeerSourceDirect))
	qt.Check(t, qt.Equals(added[0].Torrent, tor))
	stats := tor.supplyStatsLocked()
	qt.Check(t, qt.IsTrue(stats.MaxEstablished > 0))
	qt.Check(t, qt.IsTrue(stats.DialTimeout >= cfg.MinDialTimeout))
}

// A connection that closes reports why, when its handshake completed and what it moved.
func TestPeerConnClosedReason(t *testing.T) {
	seederDataDir, mi := testutil.GreetingTestTorrent()
	defer os.RemoveAll(seederDataDir)
	cfg := TestingConfig(t)
	cfg.Seed = true
	cfg.DataDir = seederDataDir
	closed := make(chan PeerConnClosedEvent, 64)
	cfg.Callbacks.PeerConnClosedReason = append(cfg.Callbacks.PeerConnClosedReason, func(ev PeerConnClosedEvent) {
		select { // called with the client lock held: never block
		case closed <- ev:
		default:
		}
	})
	seeder, err := NewClient(cfg)
	qt.Assert(t, qt.IsNil(err))
	defer seeder.Close()
	seederTorrent, _, _ := seeder.AddTorrentSpec(TorrentSpecFromMetaInfo(mi))
	seederTorrent.VerifyData()

	cfg = TestingConfig(t)
	cfg.DataDir = t.TempDir()
	leecher, err := NewClient(cfg)
	qt.Assert(t, qt.IsNil(err))
	defer leecher.Close()
	leecherTorrent, _, _ := leecher.AddTorrentSpec(TorrentSpecFromMetaInfo(mi))
	leecherTorrent.AddClientPeer(seeder)
	r := leecherTorrent.NewReader()
	_, err = io.ReadAll(r)
	r.Close()
	qt.Assert(t, qt.IsNil(err))
	seederTorrent.Drop()
	// Every close has a reason, and the connection that carried the data reports its handshake and
	// bytes.
	deadline := time.After(30 * time.Second)
	for {
		select {
		case ev := <-closed:
			qt.Check(t, qt.Not(qt.Equals(ev.Reason, "unspecified")))
			qt.Check(t, qt.Not(qt.Equals(ev.Reason, "")))
			// Others (a dial that reached this client itself, a duplicate transport) carry nothing.
			if ev.Uploaded == 0 {
				continue
			}
			qt.Check(t, qt.IsFalse(ev.Handshaken.IsZero()), qt.Commentf("reason %q", ev.Reason))
			return
		case <-deadline:
			t.Fatal("no PeerConnClosedReason event for the connection that carried the data")
		}
	}
}
