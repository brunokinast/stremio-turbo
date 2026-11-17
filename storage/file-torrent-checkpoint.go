package storage

import (
	"errors"
	"io/fs"
	"log/slog"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

const (
	defaultBufferedCheckpointInterval = time.Second
	defaultBufferedCheckpointBytes    = 128 << 20
)

type fileCheckpointTarget struct {
	primary  string
	fallback string
	length   int64
}

type torrentCheckpointBuffer struct {
	t            *fileTorrentImpl
	checkpointer PieceCompletionCheckpointer

	mu sync.Mutex

	pendingPieces map[metainfo.PieceKey]struct{}
	pendingFiles  map[string]fileCheckpointTarget
	pendingBytes  int64

	lastCheckpoint time.Time

	// Checkpoints run on a background goroutine, so MarkComplete (which readers of the piece wait
	// for) never waits for a file sync. wake asks it for one; stop ends it.
	wake    chan struct{}
	stop    chan struct{}
	stopped chan struct{}
	// Guards against starting the goroutine twice or after close.
	runOnce   sync.Once
	closeOnce sync.Once
}

func newTorrentCheckpointBuffer(
	t *fileTorrentImpl,
	checkpointer PieceCompletionCheckpointer,
) *torrentCheckpointBuffer {
	return &torrentCheckpointBuffer{
		t:              t,
		checkpointer:   checkpointer,
		pendingPieces:  make(map[metainfo.PieceKey]struct{}),
		pendingFiles:   make(map[string]fileCheckpointTarget),
		lastCheckpoint: time.Now(),
		wake:           make(chan struct{}, 1),
		stop:           make(chan struct{}),
		stopped:        make(chan struct{}),
	}
}

// Queues a completed piece for the next checkpoint and, when one is due, asks the background
// goroutine for it. Syncing the files took up to hundreds of milliseconds on a multi-GB file, and
// was done here, inline in MarkComplete, once a second.
func (me *torrentCheckpointBuffer) queuePiece(
	key metainfo.PieceKey,
	length int64,
	targets []fileCheckpointTarget,
) error {
	me.mu.Lock()
	if _, ok := me.pendingPieces[key]; !ok {
		me.pendingPieces[key] = struct{}{}
		me.pendingBytes += length
	}
	for _, target := range targets {
		me.pendingFiles[target.primary] = target
	}
	shouldCheckpoint := me.shouldCheckpointLocked()
	me.mu.Unlock()
	if shouldCheckpoint {
		me.runOnce.Do(func() { go me.run() })
		select {
		case me.wake <- struct{}{}:
		default:
		}
	}
	return nil
}

func (me *torrentCheckpointBuffer) run() {
	defer close(me.stopped)
	for {
		select {
		case <-me.stop:
			return
		case <-me.wake:
		}
		me.mu.Lock()
		due := me.shouldCheckpointLocked()
		me.mu.Unlock()
		if !due {
			continue
		}
		if err := me.checkpoint(); err != nil {
			me.logger().Warn("error checkpointing completed pieces", "err", err)
		}
	}
}

func (me *torrentCheckpointBuffer) logger() *slog.Logger {
	if me.t.client != nil && me.t.client.opts.Logger != nil {
		return me.t.client.opts.Logger
	}
	return slog.Default()
}

func (me *torrentCheckpointBuffer) shouldCheckpointLocked() bool {
	if len(me.pendingPieces) == 0 {
		return false
	}
	if me.pendingBytes >= defaultBufferedCheckpointBytes {
		return true
	}
	return time.Since(me.lastCheckpoint) >= defaultBufferedCheckpointInterval
}

func (me *torrentCheckpointBuffer) checkpoint() error {
	keys, targets := me.takePending()
	if len(keys) == 0 {
		return nil
	}

	for _, target := range targets {
		if err := me.flushTarget(target); err != nil {
			me.restorePending(keys, targets)
			return err
		}
	}
	if err := me.checkpointer.Checkpoint(keys); err != nil {
		me.restorePending(keys, targets)
		return err
	}
	me.mu.Lock()
	me.lastCheckpoint = time.Now()
	me.mu.Unlock()
	return nil
}

// Stops the background goroutine, waiting for a checkpoint in progress, then checkpoints whatever is
// still pending.
func (me *torrentCheckpointBuffer) close() error {
	me.closeOnce.Do(func() {
		close(me.stop)
		started := true
		me.runOnce.Do(func() { started = false })
		if started {
			<-me.stopped
		}
	})
	return me.checkpoint()
}

func (me *torrentCheckpointBuffer) flushTarget(target fileCheckpointTarget) error {
	err := me.t.io.flush(target.primary, 0, target.length)
	if err == nil {
		return nil
	}
	if target.fallback != "" && errors.Is(err, fs.ErrNotExist) {
		return me.t.io.flush(target.fallback, 0, target.length)
	}
	return err
}

func (me *torrentCheckpointBuffer) takePending() ([]metainfo.PieceKey, []fileCheckpointTarget) {
	me.mu.Lock()
	defer me.mu.Unlock()
	if len(me.pendingPieces) == 0 {
		return nil, nil
	}
	keys := make([]metainfo.PieceKey, 0, len(me.pendingPieces))
	for key := range me.pendingPieces {
		keys = append(keys, key)
	}
	targets := make([]fileCheckpointTarget, 0, len(me.pendingFiles))
	for _, target := range me.pendingFiles {
		targets = append(targets, target)
	}
	clear(me.pendingPieces)
	clear(me.pendingFiles)
	me.pendingBytes = 0
	return keys, targets
}

func (me *torrentCheckpointBuffer) restorePending(
	keys []metainfo.PieceKey,
	targets []fileCheckpointTarget,
) {
	me.mu.Lock()
	defer me.mu.Unlock()
	for _, key := range keys {
		if _, ok := me.pendingPieces[key]; !ok {
			me.pendingPieces[key] = struct{}{}
			me.pendingBytes += int64(me.t.info.Piece(key.Index).Length())
		}
	}
	for _, target := range targets {
		me.pendingFiles[target.primary] = target
	}
}
