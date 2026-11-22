package purego

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLiveDial dials real peers over uTP and reports what came back. It only runs when
// UTP_LIVE_PEERS names a file of host:port lines (peers known to accept uTP), so it never runs
// in normal test runs. UTP_LIVE_LISTEN sets the local port (default: any), and UTP_LIVE_OUT a
// JSON file for the per-peer results.
func TestLiveDial(t *testing.T) {
	path := os.Getenv("UTP_LIVE_PEERS")
	if path == "" {
		t.Skip("UTP_LIVE_PEERS not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	port := os.Getenv("UTP_LIVE_LISTEN")
	if port == "" {
		port = "0"
	}
	sockets := map[string]*Socket{}
	for _, n := range []string{"udp4", "udp6"} {
		addr := "0.0.0.0:" + port
		if n == "udp6" {
			addr = "[::]:" + port
		}
		s, err := NewSocket(n, addr)
		if err != nil {
			t.Logf("%s: %v", n, err)
			continue
		}
		defer s.Close()
		sockets[n] = s
		t.Logf("%s listening on %v", n, s.Addr())
	}
	type result struct {
		Addr  string
		Err   string
		Ms    int64
		Local string
	}
	var (
		mu      sync.Mutex
		results []result
		wg      sync.WaitGroup
	)
	sem := make(chan struct{}, 64)
	for _, line := range strings.Fields(string(raw)) {
		ua, err := net.ResolveUDPAddr("udp", line)
		if err != nil {
			continue
		}
		n := "udp4"
		if ua.IP.To4() == nil {
			n = "udp6"
		}
		s := sockets[n]
		if s == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			start := time.Now()
			c, err := s.DialAddrContext(ctx, ua)
			r := result{Addr: line, Ms: time.Since(start).Milliseconds(), Local: s.Addr().String()}
			if err != nil {
				r.Err = err.Error()
			} else {
				c.Close()
			}
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		}()
	}
	wg.Wait()
	byErr := map[string]int{}
	for _, r := range results {
		byErr[r.Err]++
	}
	t.Logf("results: %v", byErr)
	for n, s := range sockets {
		b, _ := json.Marshal(s.Counters())
		t.Logf("%s counters: %s", n, b)
	}
	if out := os.Getenv("UTP_LIVE_OUT"); out != "" {
		b, _ := json.MarshalIndent(results, "", " ")
		os.WriteFile(out, b, 0o644)
	}
}
