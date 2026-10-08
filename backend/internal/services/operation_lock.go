package services

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Operation kinds name what holds a stack's lock. They appear in the 409 a
// busy stack answers ("<kind> in progress since <time>"), so they are written
// for a person reading that message.
const (
	OpKindStart   = "start"
	OpKindStop    = "stop"
	OpKindRestart = "restart"
	OpKindPull    = "pull"
	OpKindCreate  = "create"
	OpKindDelete  = "delete"
	OpKindBackup  = "backup"
	OpKindRestore = "restore"
	OpKindUpdate  = "update"
	OpKindGitPull = "git pull"
	OpKindCompose = "compose file write"
	OpKindEnv     = "env file write"
	// OpKindContainer is a single-container start, stop, restart or delete
	// from the Resources page on a container a managed stack owns.
	OpKindContainer = "container action"
	// OpKindContainerPrune is the cross-stack container prune. It is the one
	// operation that takes every stack's turn at once (AcquireExclusive).
	OpKindContainerPrune = "container prune"
)

// OperationLock is a per-stack try-lock: Acquire fails fast instead of waiting
// (agent-os-0br relies on that). Each acquisition gets its own token, and only
// that token releases it, so a late or duplicate Release from a finished
// operation cannot free a lock a newer operation now holds (agent-os-a1ye.4).
//
// An operation that touches containers of every stack at once (a container
// prune) takes AcquireExclusive instead: it waits for no stack and no stack
// waits for it, and while it is held every Acquire fails fast too.
type OperationLock struct {
	mu        sync.Mutex
	locks     map[string]*lockHolder
	exclusive *lockHolder
}

type lockHolder struct {
	token     string
	kind      string
	startedAt time.Time
}

func NewOperationLock() *OperationLock {
	return &OperationLock{
		locks: make(map[string]*lockHolder),
	}
}

// Acquire takes stackID's lock for an operation of the given kind and returns
// the token that releases it. When the stack is already held it returns an
// error naming the holder's kind and start time.
func (o *OperationLock) Acquire(stackID, kind string) (string, error) {
	var b [16]byte
	// crypto/rand.Read never returns an error: on failure it crashes the
	// program (read in go1.27.1's src/crypto/rand/rand.go, Read's doc comment
	// and its fatal() call), so there is no failure branch to handle.
	_, _ = rand.Read(b[:])
	token := hex.EncodeToString(b[:])

	o.mu.Lock()
	defer o.mu.Unlock()
	if o.exclusive != nil {
		return "", inProgress(o.exclusive)
	}
	if h, held := o.locks[stackID]; held {
		return "", inProgress(h)
	}
	o.locks[stackID] = &lockHolder{token: token, kind: kind, startedAt: time.Now()}
	return token, nil
}

func inProgress(h *lockHolder) error {
	return fmt.Errorf("%s in progress since %s", h.kind, h.startedAt.UTC().Format(time.RFC3339))
}

// AcquireExclusive takes every stack's turn for an operation that must not
// overlap any stack operation (agent-os-qags.27: a container prune removes
// `created` containers, which a `compose up` holds for as long as a health
// check wait). It fails fast, like Acquire: while any stack lock is held, or
// another exclusive holder exists, it returns the error naming that holder.
// While it is held, Acquire fails with "<kind> in progress since <time>".
// ReleaseExclusive with the returned token frees it.
func (o *OperationLock) AcquireExclusive(kind string) (string, error) {
	var b [16]byte
	// Same crypto/rand.Read reasoning as Acquire: it cannot return an error.
	_, _ = rand.Read(b[:])
	token := hex.EncodeToString(b[:])

	o.mu.Lock()
	defer o.mu.Unlock()
	if o.exclusive != nil {
		return "", inProgress(o.exclusive)
	}
	// Name the longest-running holder so the message is stable, not whichever
	// entry the map yields first.
	var oldest *lockHolder
	for _, h := range o.locks {
		if oldest == nil || h.startedAt.Before(oldest.startedAt) {
			oldest = h
		}
	}
	if oldest != nil {
		return "", inProgress(oldest)
	}
	o.exclusive = &lockHolder{token: token, kind: kind, startedAt: time.Now()}
	return token, nil
}

// ReleaseExclusive frees the exclusive hold if token is the one
// AcquireExclusive handed out; any other token is stale and changes nothing,
// as in Release.
func (o *OperationLock) ReleaseExclusive(token string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.exclusive == nil || o.exclusive.token != token {
		slog.Warn("Ignoring stale exclusive operation lock release", "held", o.exclusive != nil)
		return
	}
	o.exclusive = nil
}

// Release frees stackID's lock if token is the one Acquire handed out for the
// current holder. Any other token is a stale release (the operation it belonged
// to already released, and someone else may hold the lock now), so it changes
// nothing and is logged.
func (o *OperationLock) Release(stackID, token string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	h, held := o.locks[stackID]
	if !held || h.token != token {
		slog.Warn("Ignoring stale operation lock release", "stack_id", stackID, "held", held)
		return
	}
	delete(o.locks, stackID)
}
