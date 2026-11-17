package torrent

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-quicktest/qt"

	"github.com/anacrolix/torrent/internal/testutil"
)

// After a transfer, the leecher's connection to the seeder reports its request state and transport,
// and the client lock hook has seen holds with the place they were taken.
func TestPeerConnDiagnostics(t *testing.T) {
	greetingTempDir, mi := testutil.GreetingTestTorrent()
	defer os.RemoveAll(greetingTempDir)
	cfg := TestingConfig(t)
	cfg.Seed = true
	cfg.DropMutuallyCompletePeers = false
	cfg.DataDir = greetingTempDir
	seeder, err := NewClient(cfg)
	qt.Assert(t, qt.IsNil(err))
	defer seeder.Close()
	seederTorrent, _, _ := seeder.AddTorrentSpec(TorrentSpecFromMetaInfo(mi))
	<-seederTorrent.Complete().On()

	var mu sync.Mutex
	var holds []string
	cfg = TestingConfig(t)
	cfg.DropMutuallyCompletePeers = false
	cfg.DataDir = t.TempDir()
	cfg.LockHoldThreshold = 0
	cfg.LockHoldCallback = func(held time.Duration, lockedAt string) {
		mu.Lock()
		holds = append(holds, lockedAt)
		mu.Unlock()
	}
	leecher, err := NewClient(cfg)
	qt.Assert(t, qt.IsNil(err))
	defer leecher.Close()
	leecherTorrent, _, err := leecher.AddTorrentSpec(TorrentSpecFromMetaInfo(mi))
	qt.Assert(t, qt.IsNil(err))
	leecherTorrent.AddClientPeer(seeder)
	r := leecherTorrent.NewReader()
	defer r.Close()
	assertReadAllGreeting(t, r)
	<-leecherTorrent.Complete().On()

	pcs := leecherTorrent.PeerConns()
	qt.Assert(t, qt.Not(qt.HasLen(pcs, 0)))
	// The pair may hold more than one connection; check the one that delivered the data.
	pc := pcs[0]
	for _, c := range pcs {
		if st := c.Stats(); st.BytesReadUsefulData.Int64() > 0 {
			pc = c
		}
	}
	st := pc.RequestStats()
	qt.Check(t, qt.Equals(st.Outstanding, 0))
	qt.Check(t, qt.Equals(st.Cancelled, 0))
	qt.Check(t, qt.IsTrue(st.MaxRequests >= 1))
	qt.Check(t, qt.IsTrue(st.Outgoing))
	qt.Check(t, qt.IsFalse(st.LastUsefulChunk.IsZero()))
	qt.Check(t, qt.Equals(st.UTP, pc.utp()))
	qt.Check(t, qt.IsNotNil(pc.NetConn()))

	mu.Lock()
	defer mu.Unlock()
	qt.Assert(t, qt.Not(qt.HasLen(holds, 0)))
	qt.Check(t, qt.IsTrue(strings.Contains(holds[0], "torrent")), qt.Commentf("lock site: %q", holds[0]))
}
