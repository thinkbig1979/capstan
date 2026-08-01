package services

import (
	"fmt"
	"sync"
)

type OperationLock struct {
	mu    sync.Mutex
	locks map[string]*stackLock
}

type stackLock struct {
	mu      sync.Mutex
	count   int
	ownerID string
}

func NewOperationLock() *OperationLock {
	return &OperationLock{
		locks: make(map[string]*stackLock),
	}
}

func (o *OperationLock) Acquire(stackID string) (string, error) {
	o.mu.Lock()
	sl, exists := o.locks[stackID]
	if !exists {
		sl = &stackLock{}
		o.locks[stackID] = sl
	}
	o.mu.Unlock()

	sl.mu.Lock()
	if sl.count > 0 {
		// Read the owner while still holding sl.mu: Acquire and Release both
		// write ownerID under the same lock, so formatting the message after
		// unlocking races them and can name a stale or empty holder.
		owner := sl.ownerID
		sl.mu.Unlock()
		return "", fmt.Errorf("operation already in progress for stack %s (started by %s)", stackID, owner)
	}
	sl.count++
	sl.ownerID = stackID
	sl.mu.Unlock()

	return stackID, nil
}

func (o *OperationLock) Release(stackID string) {
	o.mu.Lock()
	sl, exists := o.locks[stackID]
	o.mu.Unlock()

	if !exists {
		return
	}

	sl.mu.Lock()
	if sl.count > 0 {
		sl.count--
		sl.ownerID = ""
	}
	sl.mu.Unlock()
}
