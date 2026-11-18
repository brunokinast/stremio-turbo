// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"
)

func newLoopbackDHT(t *testing.T) *dht.Server {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := dht.NewDefaultServerConfig()
	cfg.Conn = pc
	cfg.NoSecurity = true
	cfg.StartingNodes = func() ([]dht.Addr, error) { return nil, nil }
	s, err := dht.NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func testNode(i byte, ip string) krpc.NodeInfo {
	var id [20]byte
	id[0], id[19] = i, i
	return krpc.NodeInfo{ID: id, Addr: krpc.NodeAddr{IP: net.ParseIP(ip), Port: 6881 + int(i)}}
}

// A saved table loads into a fresh server, IPv6 nodes stay out of an IPv4
// server, and an empty table leaves the last file alone.
func TestDHTNodesSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dht-nodes.dat")
	a := newLoopbackDHT(t)
	for i := byte(1); i <= 5; i++ {
		if err := a.AddNode(testNode(i, "10.0.0."+string('0'+rune(i)))); err != nil {
			t.Fatal(err)
		}
	}
	n, err := saveDHTNodes([]*dht.Server{a}, path)
	if err != nil || n != 5 {
		t.Fatalf("saved %d, err %v", n, err)
	}
	// Add an IPv6 node to the file by hand: an IPv4 server must skip it.
	nodes, err := dht.ReadNodesFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := dht.WriteNodesToFile(append(nodes, testNode(9, "2001:db8::9")), path); err != nil {
		t.Fatal(err)
	}
	b := newLoopbackDHT(t)
	added, err := loadDHTNodes([]*dht.Server{b}, path)
	if err != nil || added != 5 || b.NumNodes() != 5 {
		t.Fatalf("added %d (table %d), err %v", added, b.NumNodes(), err)
	}
	empty := newLoopbackDHT(t)
	if n, err := saveDHTNodes([]*dht.Server{empty}, path); err != nil || n != 0 {
		t.Fatalf("empty save = %d, %v", n, err)
	}
	if nodes, err := dht.ReadNodesFromFile(path); err != nil || len(nodes) != 6 {
		t.Fatalf("file after empty save: %d nodes, err %v", len(nodes), err)
	}
}

func TestDHTNodesMissingOrCorruptFile(t *testing.T) {
	dir := t.TempDir()
	s := newLoopbackDHT(t)
	if _, err := loadDHTNodes([]*dht.Server{s}, filepath.Join(dir, "none.dat")); !os.IsNotExist(err) {
		t.Fatalf("missing file: err %v", err)
	}
	bad := filepath.Join(dir, "bad.dat")
	if err := os.WriteFile(bad, []byte("not nodes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if added, _ := loadDHTNodes([]*dht.Server{s}, bad); added != 0 || s.NumNodes() != 0 {
		t.Fatalf("corrupt file added %d", added)
	}
}
