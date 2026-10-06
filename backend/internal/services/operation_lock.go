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
)

// OperationLock is a per-stack try-lock: Acquire fails fast instead of waiting
// (agent-os-0br relies on that). Each acquisition gets its own token, and only
// that token releases it, so a late or duplicate Release from a finished
// operation cannot free a lock a newer operation now holds (agent-os-a1ye.4).
type OperationLock struct {
	mu    sync.Mutex
	locks map[string]*lockHolder
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
	if h, held := o.locks[stackID]; held {
		return "", fmt.Errorf("%s in progress since %s", h.kind, h.startedAt.UTC().Format(time.RFC3339))
	}
	o.locks[stackID] = &lockHolder{token: token, kind: kind, startedAt: time.Now()}
	return token, nil
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
