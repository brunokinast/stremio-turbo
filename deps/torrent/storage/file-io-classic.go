package storage

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"sort"
	"sync"
)

// Holds one cached read-write handle per file. The mutex guards the map only: opening a file, syncing
// it and reading from it happen outside it. Each of those took a few milliseconds on Windows (an open
// is ~5 ms there, mostly the antivirus), and every chunk write of every peer connection waits for the
// mutex, so holding it across them made writes 15 times slower under a streaming read load, and a
// sync of a multi-GB file stalled all writes for up to a few hundred milliseconds.
type classicFileIo struct {
	mu      sync.Mutex
	writers map[string]*classicHandle
	closed  bool
}

// A cached handle and the reads and syncs using it. Users are added under classicFileIo.mu while the
// handle is in the map; it is removed from the map before waiting for them, so no user can be added
// once the wait starts.
type classicHandle struct {
	f     *os.File
	users sync.WaitGroup
}

// Returns the cached handle for name with a user added, or nil. Call with me.mu held.
func (me *classicFileIo) borrowLocked(name string) *classicHandle {
	h := me.writers[name]
	if h != nil {
		h.users.Add(1)
	}
	return h
}

func newClassicFileIo() fileIo {
	return &classicFileIo{}
}

func (me *classicFileIo) Close() error {
	me.mu.Lock()
	defer me.mu.Unlock()
	_, _, err := me.closeWritersLocked()
	me.closed = true
	return err
}

func (me *classicFileIo) closeWriters() (closedPaths []string, remaining int, err error) {
	me.mu.Lock()
	defer me.mu.Unlock()
	if err := me.closedErr(); err != nil {
		return nil, 0, err
	}
	return me.closeWritersLocked()
}

func (me *classicFileIo) rename(from, to string) error {
	me.mu.Lock()
	defer me.mu.Unlock()
	if err := me.closedErr(); err != nil {
		return err
	}
	// Nothing to rename: leave the handles alone. onFileNotComplete renames every file of a piece
	// that failed its hash back to its part file; for a file still downloading that's a no-op, and
	// closing its cached writer under in-flight chunk writes failed them ("file already closed"),
	// which disables data download for the whole torrent.
	if _, err := os.Lstat(from); err != nil {
		return err
	}
	err := me.closeWriterLocked(from)
	if renameErr := renameNoReplace(from, to); renameErr != nil {
		// Nothing was moved onto to, so its cached writer is still good, and pieces could be
		// writing through it right now.
		return errors.Join(err, renameErr)
	}
	return errors.Join(err, me.closeWriterLocked(to))
}

// Borrows the cached handle for name, or reports that there is none. Fails once closed.
func (me *classicFileIo) borrow(name string) (*classicHandle, error) {
	me.mu.Lock()
	defer me.mu.Unlock()
	if err := me.closedErr(); err != nil {
		return nil, err
	}
	return me.borrowLocked(name), nil
}

func (me *classicFileIo) flush(name string, offset, nbytes int64) error {
	h, err := me.borrow(name)
	if err != nil {
		return err
	}
	if h != nil {
		defer h.users.Done()
		return h.f.Sync()
	}
	return fsync(name)
}

// Reads use the cached write handle when there is one (ReadAt is safe alongside writes), which also
// saves an open per read.
func (me *classicFileIo) openForSharedRead(name string) (sharableReader, error) {
	h, err := me.borrow(name)
	if err != nil {
		return nil, err
	}
	if h != nil {
		return &classicBorrowedFileReader{h: h}, nil
	}
	return os.Open(name)
}

func (me *classicFileIo) openForRead(name string) (fileReader, error) {
	h, err := me.borrow(name)
	if err != nil {
		return nil, err
	}
	if h != nil {
		return &classicBorrowedFileReader{h: h}, nil
	}
	f, err := os.Open(name)
	return classicFileReader{f}, err
}

// Writers don't borrow the handle (closing them is optional, as before): a write racing a close of
// the cache fails with a closed-file error rather than delaying the close.
func (me *classicFileIo) openForWrite(p string, size int64) (f fileWriter, err error) {
	me.mu.Lock()
	if err := me.closedErr(); err != nil {
		me.mu.Unlock()
		return nil, err
	}
	h := me.writers[p]
	me.mu.Unlock()
	if h != nil {
		return classicFileWriter{h.f}, nil
	}
	// Open outside the lock, then cache it unless another writer got there first.
	opened, err := openFileExtra(p, os.O_RDWR)
	if err != nil {
		return nil, err
	}
	me.mu.Lock()
	defer me.mu.Unlock()
	if err := me.closedErr(); err != nil {
		opened.Close()
		return nil, err
	}
	if h := me.writers[p]; h != nil {
		opened.Close()
		return classicFileWriter{h.f}, nil
	}
	if me.writers == nil {
		me.writers = make(map[string]*classicHandle)
	}
	me.writers[p] = &classicHandle{f: opened}
	return classicFileWriter{opened}, nil
}

// Removes the cached handle, waits for the operations using it, and closes it. Waiting with me.mu
// held is safe: users release their handle without taking it.
func (me *classicFileIo) closeWriterLocked(name string) error {
	if me.writers == nil {
		return nil
	}
	h, ok := me.writers[name]
	if !ok {
		return nil
	}
	delete(me.writers, name)
	h.users.Wait()
	return h.f.Close()
}

func (me *classicFileIo) closeWritersLocked() (closedPaths []string, remaining int, err error) {
	if me.writers == nil {
		return nil, 0, nil
	}
	closedPaths = make([]string, 0, len(me.writers))
	for name := range me.writers {
		closedPaths = append(closedPaths, name)
	}
	sort.Strings(closedPaths)
	for _, name := range closedPaths {
		err = errors.Join(err, me.closeWriterLocked(name))
	}
	return closedPaths, len(me.writers), err
}

func (me *classicFileIo) closedErr() error {
	if me.closed {
		return fs.ErrClosed
	}
	return nil
}

type classicFileWriter struct {
	*os.File
}

func (classicFileWriter) Close() error {
	return nil
}

type classicFileReader struct {
	*os.File
}

func (c classicFileReader) writeToN(w io.Writer, n int64) (written int64, err error) {
	lw := limitWriter{
		rem: n,
		w:   w,
	}
	written, err = c.File.WriteTo(&lw)
	// The limit stops the copy at n bytes; that is not a failed write.
	if errors.Is(err, io.ErrShortWrite) && lw.rem == 0 {
		err = nil
	}
	return
}

func (c classicFileReader) seekDataOrEof(offset int64) (ret int64, err error) {
	ret, err = seekData(c.File, offset)
	if err == io.EOF {
		ret, err = c.File.Seek(0, io.SeekEnd)
	}
	return
}

// Reads through the cached writer handle instead of opening the file again. The handle can't be
// closed until Close releases it, so callers must close it promptly (every caller closes it right
// after its read); a reader held open would delay a rename or close of that file.
type classicBorrowedFileReader struct {
	h        *classicHandle
	pos      int64
	released bool
}

func (c *classicBorrowedFileReader) Close() error {
	if !c.released {
		c.released = true
		c.h.users.Done()
	}
	return nil
}

func (c *classicBorrowedFileReader) ReadAt(p []byte, off int64) (int, error) {
	return c.h.f.ReadAt(p, off)
}

func (c *classicBorrowedFileReader) Read(p []byte) (n int, err error) {
	n, err = c.h.f.ReadAt(p, c.pos)
	c.pos += int64(n)
	if err == io.EOF && n > 0 {
		err = nil
	}
	return
}

func (c *classicBorrowedFileReader) writeToN(w io.Writer, n int64) (written int64, err error) {
	written, err = io.Copy(w, io.NewSectionReader(c.h.f, c.pos, n))
	c.pos += written
	return
}

func (c *classicBorrowedFileReader) seekDataOrEof(offset int64) (ret int64, err error) {
	ret, err = seekData(c.h.f, offset)
	if err == io.EOF {
		var fi os.FileInfo
		fi, err = c.h.f.Stat()
		if err != nil {
			return c.pos, err
		}
		ret = fi.Size()
		err = nil
	}
	if err != nil {
		return c.pos, err
	}
	c.pos = ret
	return
}
