// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"net"
	"os"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"
	"github.com/anacrolix/torrent"

	"github.com/rivulet-kodi/rivulet-engine/internal/logging"
)

// The DHT routing table is kept across restarts (RIVULET_DHT_NODES_FILE).
// Started empty, the DHT needs one to three seconds of bootstrap traversal
// before a torrent's get_peers finds anyone, and on a 4K swarm the DHT is where
// the productive peers come from (research/findings/16). The
// launcher force-stops the server, so the table is saved periodically rather
// than at exit.
const (
	dhtNodesFirstSave = time.Minute
	dhtNodesSaveEvery = 2 * time.Minute
	dhtNodesMax       = 1000
)

// dhtServers returns the client's anacrolix DHT servers (IPv4 and IPv6).
func (m *manager) dhtServers() []*dht.Server {
	var servers []*dht.Server
	for _, s := range m.client.DhtServers() {
		if w, ok := s.(torrent.AnacrolixDhtServerWrapper); ok {
			servers = append(servers, w.Server)
		}
	}
	return servers
}

// keepDHTNodes loads the saved routing table into the DHT servers, then saves
// it after dhtNodesFirstSave and every dhtNodesSaveEvery until the manager
// closes. An empty path disables it.
func (m *manager) keepDHTNodes(path string) {
	servers := m.dhtServers()
	if path == "" || len(servers) == 0 {
		return
	}
	added, err := loadDHTNodes(servers, path)
	switch {
	case os.IsNotExist(err):
		logging.For("engine").Info("no saved dht nodes yet", "file", path)
	case err != nil:
		logging.For("engine").Warn("saved dht nodes unreadable", "file", path, "err", err)
	default:
		logging.For("engine").Info("loaded saved dht nodes", "file", path, "added", added)
	}
	go func() {
		t := time.NewTimer(dhtNodesFirstSave)
		defer t.Stop()
		for {
			select {
			case <-m.done:
				return
			case <-t.C:
				if n, err := saveDHTNodes(servers, path); err != nil {
					logging.For("engine").Warn("saving dht nodes failed", "file", path, "err", err)
				} else {
					logging.For("engine").Debug("saved dht nodes", "file", path, "nodes", n)
				}
				t.Reset(dhtNodesSaveEvery)
			}
		}
	}()
}

func isIPv4Server(s *dht.Server) bool {
	ua, ok := s.Addr().(*net.UDPAddr)
	return !ok || ua.IP.To4() != nil
}

// loadDHTNodes adds the nodes saved at path to the servers of their address
// family, and reports how many were added.
func loadDHTNodes(servers []*dht.Server, path string) (int, error) {
	nodes, err := dht.ReadNodesFromFile(path)
	if err != nil {
		return 0, err
	}
	added := 0
	for _, s := range servers {
		v4 := isIPv4Server(s)
		for _, n := range nodes {
			if (n.Addr.IP.To4() != nil) == v4 && s.AddNode(n) == nil {
				added++
			}
		}
	}
	return added, nil
}

// saveDHTNodes writes the servers' non-bad nodes, then the previous file's
// nodes not among them, to path (at most dhtNodesMax, atomically), and reports
// how many were written. Merging keeps a young process (its table still
// filling) from replacing a fuller table saved earlier: in the lab a 60 s save
// cut a file of ~200 nodes to 48. An empty table leaves the file alone.
func saveDHTNodes(servers []*dht.Server, path string) (int, error) {
	var nodes []krpc.NodeInfo
	for _, s := range servers {
		nodes = append(nodes, s.Nodes()...)
	}
	if len(nodes) == 0 {
		return 0, nil
	}
	seen := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		seen[n.Addr.String()] = true
	}
	if old, err := dht.ReadNodesFromFile(path); err == nil {
		for _, n := range old {
			if !seen[n.Addr.String()] {
				seen[n.Addr.String()] = true
				nodes = append(nodes, n)
			}
		}
	}
	nodes = nodes[:min(len(nodes), dhtNodesMax)]
	if err := writeFileAtomic(path, func(tmp string) error { return dht.WriteNodesToFile(nodes, tmp) }); err != nil {
		return 0, err
	}
	return len(nodes), nil
}
