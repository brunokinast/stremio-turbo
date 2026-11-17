package storage

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	g "github.com/anacrolix/generics"
	"github.com/go-quicktest/qt"

	"github.com/anacrolix/torrent/metainfo"
)

func writeClassic(t *testing.T, io *classicFileIo, name string, b []byte, off int64) {
	t.Helper()
	w, err := io.openForWrite(name, 0)
	qt.Assert(t, qt.IsNil(err))
	_, err = w.WriteAt(b, off)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsNil(w.Close()))
}

// Reads of a file being written go through the cached write handle instead of opening it again.
func TestClassicFileIoSharedReadUsesWriterHandle(t *testing.T) {
	name := filepath.Join(t.TempDir(), "a")
	fio := newClassicFileIo().(*classicFileIo)
	defer fio.Close()
	writeClassic(t, fio, name, []byte("hello"), 3)
	r, err := fio.openForSharedRead(name)
	qt.Assert(t, qt.IsNil(err))
	_, borrowed := r.(*classicBorrowedFileReader)
	qt.Check(t, qt.IsTrue(borrowed))
	b := make([]byte, 5)
	_, err = r.ReadAt(b, 3)
	qt.Check(t, qt.IsNil(err))
	qt.Check(t, qt.Equals(string(b), "hello"))
	qt.Check(t, qt.IsNil(r.Close()))
	qt.Check(t, qt.IsNil(r.Close())) // releasing twice is harmless
}

// A borrowed handle holds no lock: other files and the same file can be opened, written and flushed
// while it is out.
func TestClassicFileIoBorrowHoldsNoLock(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	fio := newClassicFileIo().(*classicFileIo)
	defer fio.Close()
	writeClassic(t, fio, a, []byte("x"), 0)
	r, err := fio.openForRead(a)
	qt.Assert(t, qt.IsNil(err))
	done := make(chan struct{})
	go func() {
		defer close(done)
		writeClassic(t, fio, a, []byte("y"), 1)
		writeClassic(t, fio, b, []byte("z"), 0)
		qt.Check(t, qt.IsNil(fio.flush(a, 0, 2)))
		r2, err := fio.openForSharedRead(a)
		qt.Check(t, qt.IsNil(err))
		qt.Check(t, qt.IsNil(r2.Close()))
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked while a handle was borrowed")
	}
	qt.Check(t, qt.IsNil(r.Close()))
}

// Closing the cached handles waits for operations still using them, so a read in progress never sees
// a closed file.
func TestClassicFileIoCloseWaitsForBorrowers(t *testing.T) {
	name := filepath.Join(t.TempDir(), "a")
	fio := newClassicFileIo().(*classicFileIo)
	writeClassic(t, fio, name, []byte("data"), 0)
	r, err := fio.openForSharedRead(name)
	qt.Assert(t, qt.IsNil(err))
	closed := make(chan error)
	go func() {
		_, _, err := fio.closeWriters()
		closed <- err
	}()
	select {
	case <-closed:
		t.Fatal("closeWriters returned while a reader was using the handle")
	case <-time.After(50 * time.Millisecond):
	}
	b := make([]byte, 4)
	_, err = r.ReadAt(b, 0)
	qt.Check(t, qt.IsNil(err))
	qt.Check(t, qt.Equals(string(b), "data"))
	qt.Check(t, qt.IsNil(r.Close()))
	qt.Check(t, qt.IsNil(<-closed))
	// Gone from the cache: a new read opens the file itself.
	r, err = fio.openForSharedRead(name)
	qt.Assert(t, qt.IsNil(err))
	_, borrowed := r.(*classicBorrowedFileReader)
	qt.Check(t, qt.IsFalse(borrowed))
	qt.Check(t, qt.IsNil(r.Close()))
	qt.Check(t, qt.IsNil(fio.Close()))
	_, err = fio.openForRead(name)
	qt.Check(t, qt.IsNotNil(err))
}

// A file io whose flush blocks until released, to show who waits for it.
type blockingFlushFileIo struct {
	checkpointCountingFileIo
	flushing chan struct{}
	release  chan struct{}
}

func (me *blockingFlushFileIo) flush(name string, off, n int64) error {
	me.flushing <- struct{}{}
	<-me.release
	return me.checkpointCountingFileIo.flush(name, off, n)
}

// MarkComplete no longer waits for the checkpoint's file sync; closing the torrent waits for one in
// progress and persists what is still pending.
func TestCheckpointRunsInBackground(t *testing.T) {
	info := &metainfo.Info{
		Files:       []metainfo.FileInfo{{Path: []string{"a.bin"}, Length: 2}},
		Pieces:      make([]byte, 40),
		PieceLength: 1,
	}
	pc := newBufferedPieceCompletion(&recordingPieceCompletion{})
	checkpointer := pc.(PieceCompletionCheckpointer)
	fio := &blockingFlushFileIo{flushing: make(chan struct{}), release: make(chan struct{})}
	torrent := &fileTorrentImpl{
		info:              info,
		files:             []fileExtra{{safeOsPath: "a.bin"}},
		metainfoFileInfos: info.UpvertedFiles(),
		segmentLocater:    info.FileSegmentsIndex(),
		infoHash:          metainfo.HashBytes([]byte("bg")),
		io:                fio,
		client: &fileClientImpl{opts: NewFileClientOpts{
			PieceCompletion: pc,
			UsePartFiles:    g.Some(false),
		}},
	}
	torrent.checkpoint = newTorrentCheckpointBuffer(torrent, checkpointer)
	torrent.checkpoint.lastCheckpoint = time.Time{} // due at once
	piece := func(i int) *filePieceImpl { return &filePieceImpl{t: torrent, p: info.Piece(i)} }

	marked := make(chan error)
	go func() { marked <- piece(0).MarkComplete() }()
	select {
	case err := <-marked:
		qt.Assert(t, qt.IsNil(err))
	case <-time.After(5 * time.Second):
		t.Fatal("MarkComplete waited for the checkpoint")
	}
	<-fio.flushing // the background checkpoint is now syncing, and stays blocked

	qt.Assert(t, qt.IsNil(piece(1).MarkComplete()))
	closed := make(chan error)
	go func() { closed <- torrent.Close() }()
	select {
	case <-closed:
		t.Fatal("Close returned during a checkpoint")
	case <-time.After(50 * time.Millisecond):
	}
	close(fio.release) // the in-flight checkpoint finishes; Close's final one syncs at once
	go func() {
		for range fio.flushing {
		}
	}()
	qt.Assert(t, qt.IsNil(<-closed))
	close(fio.flushing)
	for i := range 2 {
		c, err := pc.Get(metainfo.PieceKey{InfoHash: torrent.infoHash, Index: i})
		qt.Assert(t, qt.IsNil(err))
		qt.Check(t, qt.IsTrue(c.Ok && c.Complete), qt.Commentf("piece %d", i))
	}
	qt.Check(t, qt.HasLen(fio.flushes, 2))
}

var _ io.ReaderAt = (*classicBorrowedFileReader)(nil)

// Counts completion lookups.
type countingPieceCompletion struct {
	PieceCompletion
	gets atomic.Int64
}

func (me *countingPieceCompletion) Persistent() bool {
	return pieceCompletionIsPersistent(me.PieceCompletion)
}

func (me *countingPieceCompletion) Get(pk metainfo.PieceKey) (Completion, error) {
	me.gets.Add(1)
	return me.PieceCompletion.Get(pk)
}

// Downloading in order promotes the part file when the last piece completes; marking the pieces of a
// file that's already complete, with no part file, scans its completion once, not once per piece.
func TestMarkCompletePartFilePromotion(t *testing.T) {
	const n = 64
	dir := t.TempDir()
	pc := &countingPieceCompletion{PieceCompletion: NewMapPieceCompletion()}
	cl := NewFileOpts(NewFileClientOpts{ClientBaseDir: dir, PieceCompletion: pc, DisableMmap: true})
	defer cl.Close()
	info := &metainfo.Info{Name: "f.bin", Length: n, PieceLength: 1, Pieces: make([]byte, 20*n)}
	ts, err := cl.OpenTorrent(context.Background(), info, metainfo.Hash{2})
	qt.Assert(t, qt.IsNil(err))
	defer ts.Close()
	final := filepath.Join(dir, "f.bin")
	for i := range n {
		p := ts.Piece(info.Piece(i))
		_, err := p.WriteAt([]byte{byte(i)}, 0)
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.IsNil(p.MarkComplete()))
		_, statErr := os.Stat(final)
		qt.Assert(t, qt.Equals(statErr == nil, i == n-1), qt.Commentf("piece %d: final file stat %v", i, statErr))
	}
	_, err = os.Stat(final + ".part")
	qt.Check(t, qt.ErrorIs(err, fs.ErrNotExist))

	// A recheck marks every piece again.
	pc.gets.Store(0)
	for i := range n {
		qt.Assert(t, qt.IsNil(ts.Piece(info.Piece(i)).MarkComplete()))
	}
	qt.Check(t, qt.IsTrue(pc.gets.Load() <= 2*n), qt.Commentf("%d completion lookups for %d pieces", pc.gets.Load(), n))
}
