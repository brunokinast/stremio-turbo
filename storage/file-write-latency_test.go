package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// Measures chunk write and MarkComplete latency under a streaming-like load: several writers (one per
// peer connection) each filling their own pieces with 16 KiB chunks at a steady combined rate, and
// marking each piece complete when it is full. It compares the classic file storage with persistent
// (bolt, checkpointed) completion, with in-memory completion (no flush), and plain os.File writes
// (the syscall alone). It writes about 1 GiB per variant, so it runs only when asked:
//
//	STORAGE_WRITE_LATENCY=1 go test -run TestFileWriteLatency -v ./storage
//
// STORAGE_WRITE_LATENCY_DIR picks the directory (default: a temp dir), so it can be run on the drive
// a client really uses.
func TestFileWriteLatency(t *testing.T) {
	if os.Getenv("STORAGE_WRITE_LATENCY") == "" {
		t.Skip("set STORAGE_WRITE_LATENCY=1 to run")
	}
	const (
		pieceLen   = 16 << 20
		numPieces  = 64
		chunk      = 16 << 10
		writers    = 16
		bytesPerS  = 30 << 20
		chunkEvery = time.Duration(int64(time.Second) * chunk * writers / bytesPerS)
	)
	base := os.Getenv("STORAGE_WRITE_LATENCY_DIR")
	if base == "" {
		base = t.TempDir()
	}
	type variant struct {
		name string
		pc   func(dir string) (PieceCompletion, error)
		// Readers of completed pieces, as the player and peer uploads do: each reads readSize
		// bytes readsPerS times a second.
		readers []reader
	}
	player := reader{64 << 10, 48}  // ~3 MiB/s, a 4K stream
	upload := reader{16 << 10, 200} // ~3 MiB/s of uploads in 16 KiB requests
	for _, v := range []variant{
		{"raw os.File", nil, nil},
		{"file, in-memory completion", func(string) (PieceCompletion, error) { return NewMapPieceCompletion(), nil }, nil},
		{"file, bolt completion", NewBoltPieceCompletion, nil},
		{"file, bolt, player+upload", NewBoltPieceCompletion, []reader{player, upload}},
	} {
		dir, err := os.MkdirTemp(base, "latency-")
		if err != nil {
			t.Fatal(err)
		}
		info := &metainfo.Info{
			Name:        "big.mkv",
			Length:      pieceLen * numPieces,
			PieceLength: pieceLen,
			Pieces:      make([]byte, 20*numPieces),
		}
		var (
			writeAt      func(piece int, b []byte, off int64) error
			readAt       func(piece int, b []byte, off int64) error
			markComplete func(piece int) error
			closeAll     func()
		)
		if v.pc == nil {
			f, err := os.OpenFile(filepath.Join(dir, "raw.bin"), os.O_CREATE|os.O_RDWR, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			writeAt = func(piece int, b []byte, off int64) error {
				_, err := f.WriteAt(b, int64(piece)*pieceLen+off)
				return err
			}
			markComplete = func(int) error { return nil }
			closeAll = func() { f.Close() }
		} else {
			pc, err := v.pc(dir)
			if err != nil {
				t.Fatal(err)
			}
			cl := NewFileOpts(NewFileClientOpts{ClientBaseDir: dir, PieceCompletion: pc, DisableMmap: true})
			ts, err := cl.OpenTorrent(context.Background(), info, metainfo.Hash{1})
			if err != nil {
				t.Fatal(err)
			}
			writeAt = func(piece int, b []byte, off int64) error {
				_, err := ts.Piece(info.Piece(piece)).WriteAt(b, off)
				return err
			}
			readAt = func(piece int, b []byte, off int64) error {
				_, err := ts.Piece(info.Piece(piece)).ReadAt(b, off)
				return err
			}
			markComplete = func(piece int) error { return ts.Piece(info.Piece(piece)).MarkComplete() }
			closeAll = func() { ts.Close(); cl.Close(); pc.Close() }
		}

		var mu sync.Mutex
		var writes, marks, reads []time.Duration
		var wg sync.WaitGroup
		start := time.Now()
		// Pieces already written, which readers pick from. The first writer's first piece is
		// written before the run so readers have something from the start.
		var completed []int
		if len(v.readers) != 0 {
			buf := make([]byte, chunk)
			for off := int64(0); off < pieceLen; off += chunk {
				if err := writeAt(0, buf, off); err != nil {
					t.Fatal(err)
				}
			}
			completed = append(completed, 0)
		}
		stopReaders := make(chan struct{})
		var readersWg sync.WaitGroup
		for i, r := range v.readers {
			readersWg.Add(1)
			go func() {
				defer readersWg.Done()
				buf := make([]byte, r.size)
				tick := time.NewTicker(time.Second / time.Duration(r.perS))
				defer tick.Stop()
				var mine []time.Duration
				for n := 0; ; n++ {
					select {
					case <-stopReaders:
						mu.Lock()
						reads = append(reads, mine...)
						mu.Unlock()
						return
					case <-tick.C:
					}
					mu.Lock()
					p := completed[(n*7+i)%len(completed)]
					mu.Unlock()
					off := int64(n*r.size) % (pieceLen - int64(r.size))
					t0 := time.Now()
					if err := readAt(p, buf, off); err != nil {
						t.Error(err)
						return
					}
					mine = append(mine, time.Since(t0))
				}
			}()
		}
		for w := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				buf := make([]byte, chunk)
				tick := time.NewTicker(chunkEvery)
				defer tick.Stop()
				var myWrites, myMarks []time.Duration
				for p := w; p < numPieces; p += writers {
					for off := int64(0); off < pieceLen; off += chunk {
						<-tick.C
						t0 := time.Now()
						if err := writeAt(p, buf, off); err != nil {
							t.Error(err)
							return
						}
						myWrites = append(myWrites, time.Since(t0))
					}
					t0 := time.Now()
					if err := markComplete(p); err != nil {
						t.Error(err)
						return
					}
					myMarks = append(myMarks, time.Since(t0))
					mu.Lock()
					completed = append(completed, p)
					mu.Unlock()
				}
				mu.Lock()
				writes = append(writes, myWrites...)
				marks = append(marks, myMarks...)
				mu.Unlock()
			}()
		}
		wg.Wait()
		wall := time.Since(start)
		close(stopReaders)
		readersWg.Wait()
		closeAll()
		os.RemoveAll(dir)
		t.Logf("%-28s wall %5.1fs  writes: %s  in writes/wall %.2f  |  MarkComplete: %s",
			v.name, wall.Seconds(), latencySummary(writes), sumDurations(writes).Seconds()/wall.Seconds(), latencySummary(marks))
		if len(reads) != 0 {
			t.Logf("%-28s reads: %s", "", latencySummary(reads))
		}
	}
}

type reader struct {
	size, perS int
}

func sumDurations(ds []time.Duration) (sum time.Duration) {
	for _, d := range ds {
		sum += d
	}
	return
}

func latencySummary(ds []time.Duration) string {
	if len(ds) == 0 {
		return "none"
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	q := func(f float64) time.Duration { return s[int(f*float64(len(s)-1))] }
	over := func(d time.Duration) (n int) {
		for _, x := range s {
			if x >= d {
				n++
			}
		}
		return
	}
	return fmt.Sprintf("n=%d mean=%v p50=%v p99=%v max=%v >=10ms:%d >=50ms:%d",
		len(s), (sumDurations(s) / time.Duration(len(s))).Round(time.Microsecond),
		q(0.5).Round(time.Microsecond), q(0.99).Round(time.Microsecond), s[len(s)-1].Round(time.Microsecond),
		over(10*time.Millisecond), over(50*time.Millisecond))
}

// The first write to the end of a new file, as a stream does when the player reads the file's tail,
// then a write at the start. Through the file storage (sparse files where the platform needs asking)
// and through a plain os.File (zero-filled on Windows). Opt-in like TestFileWriteLatency; 4 GiB file.
func TestFirstTailWriteLatency(t *testing.T) {
	if os.Getenv("STORAGE_WRITE_LATENCY") == "" {
		t.Skip("set STORAGE_WRITE_LATENCY=1 to run")
	}
	const size = 4 << 30
	base := os.Getenv("STORAGE_WRITE_LATENCY_DIR")
	if base == "" {
		base = t.TempDir()
	}
	chunk := make([]byte, 16<<10)
	timeWrites := func(name string, writeAt func(b []byte, off int64) error) {
		t0 := time.Now()
		if err := writeAt(chunk, size-int64(len(chunk))); err != nil {
			t.Fatal(err)
		}
		tail := time.Since(t0)
		t0 = time.Now()
		if err := writeAt(chunk, 0); err != nil {
			t.Fatal(err)
		}
		t.Logf("%-14s first write at the end %v, then at the start %v", name, tail, time.Since(t0))
	}

	dir, err := os.MkdirTemp(base, "tail-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	f, err := os.OpenFile(filepath.Join(dir, "plain.bin"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	timeWrites("plain os.File", func(b []byte, off int64) error { _, err := f.WriteAt(b, off); return err })
	f.Close()

	const pieceLen = 16 << 20
	info := &metainfo.Info{Name: "big.mkv", Length: size, PieceLength: pieceLen, Pieces: make([]byte, 20*(size/pieceLen))}
	cl := NewFileOpts(NewFileClientOpts{ClientBaseDir: dir, PieceCompletion: NewMapPieceCompletion(), DisableMmap: true})
	defer cl.Close()
	ts, err := cl.OpenTorrent(context.Background(), info, metainfo.Hash{3})
	if err != nil {
		t.Fatal(err)
	}
	defer ts.Close()
	timeWrites("file storage", func(b []byte, off int64) error {
		_, err := ts.Piece(info.Piece(int(off/pieceLen))).WriteAt(b, off%pieceLen)
		return err
	})
}
