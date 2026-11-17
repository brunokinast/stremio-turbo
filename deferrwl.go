package torrent

import (
	"fmt"
	"reflect"
	"runtime"
	"time"

	g "github.com/anacrolix/generics"
	"github.com/anacrolix/missinggo/v2/panicif"
	"github.com/anacrolix/sync"
)

// Runs deferred actions on Unlock. Note that actions are assumed to be the results of changes that
// would only occur with a write lock at present. The race detector should catch instances of defers
// without the write lock being held.
type lockWithDeferreds struct {
	internal      sync.RWMutex
	unlockActions []func()
	uniqueActions map[any]struct{}
	// Currently unlocking, defers should not occur?
	allowDefers bool
	client      *Client
	// Set by Lock only while Config.LockHoldCallback is set.
	lockedAt   time.Time
	lockedAtPC [1]uintptr
}

func (me *lockWithDeferreds) Lock() {
	me.internal.Lock()
	panicif.True(me.allowDefers)
	me.allowDefers = true
	if me.lockHoldCallback() != nil {
		me.lockedAt = time.Now()
		// Skip Callers, Lock and the Client.lock wrapper.
		runtime.Callers(3, me.lockedAtPC[:])
	}
}

func (me *lockWithDeferreds) lockHoldCallback() func(time.Duration, string) {
	if me.client == nil || me.client.config == nil {
		return nil
	}
	return me.client.config.LockHoldCallback
}

func (me *lockWithDeferreds) Unlock() {
	cb := me.lockHoldCallback()
	if cb == nil || me.lockedAt.IsZero() {
		me.unlock()
		return
	}
	lockedAt, pc := me.lockedAt, me.lockedAtPC[0]
	me.lockedAt = time.Time{}
	me.unlock()
	held := time.Since(lockedAt)
	if held < me.client.config.LockHoldThreshold {
		return
	}
	where := "unknown"
	if fn := runtime.FuncForPC(pc); fn != nil {
		file, line := fn.FileLine(pc)
		where = fmt.Sprintf("%s (%s:%d)", fn.Name(), file, line)
	}
	cb(held, where)
}

func (me *lockWithDeferreds) unlock() {
	// If this doesn't happen other clean up handlers will block on the lock.
	defer me.internal.Unlock()
	panicif.False(me.allowDefers)
	me.allowDefers = false
	me.client.unlockHandlers.run(me.client.slogger)
	startLen := len(me.unlockActions)
	var i int
	for i = 0; i < len(me.unlockActions); i++ {
		me.unlockActions[i]()
	}
	if i != len(me.unlockActions) {
		panic(fmt.Sprintf("num deferred changed while running: %v -> %v", startLen, len(me.unlockActions)))
	}
	// Don't let the backing array keep the actions, and what they capture, alive.
	clear(me.unlockActions)
	me.unlockActions = me.unlockActions[:0]
	me.uniqueActions = nil
}

func (me *lockWithDeferreds) RLock() {
	me.internal.RLock()
}

func (me *lockWithDeferreds) RUnlock() {
	me.internal.RUnlock()
}

// Not allowed after unlock has started.
func (me *lockWithDeferreds) Defer(action func()) {
	me.deferInner(action)
}

// Already guarded.
func (me *lockWithDeferreds) deferInner(action func()) {
	panicif.False(me.allowDefers)
	me.unlockActions = append(me.unlockActions, action)
}

// Protected from looping by once filter.
func (me *lockWithDeferreds) deferOnceInner(key any, action func()) {
	panicif.False(me.allowDefers)
	g.MakeMapIfNil(&me.uniqueActions)
	if g.MapContains(me.uniqueActions, key) {
		return
	}
	me.uniqueActions[key] = struct{}{}
	me.deferInner(action)
}

// Protected from looping by once filter. Note that if arg is the receiver of action, it should
// match the receiver type (like being a pointer if the method takes a pointer receiver).
func (me *lockWithDeferreds) DeferUniqueUnaryFunc(arg any, action func()) {
	me.deferOnceInner(unaryFuncKey(action, arg), action)
}

func unaryFuncKey(f func(), key any) funcAndArgKey {
	return funcAndArgKey{
		funcStr: reflect.ValueOf(f).String(),
		key:     key,
	}
}

type funcAndArgKey struct {
	funcStr string
	key     any
}
