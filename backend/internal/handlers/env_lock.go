package handlers

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
)

// defaultUnlockRecheckInterval is how often a long-lived connection
// re-checks the env unlock it was opened under.
const defaultUnlockRecheckInterval = 5 * time.Second

// unlockRecheckOverride, when not zero, replaces the interval. Only tests set
// it, and atomically: a connection goroutine of an earlier test can still be
// reading it while the next test shortens it.
var unlockRecheckOverride atomic.Int64

// unlockRecheckInterval is the interval in use.
func unlockRecheckInterval() time.Duration {
	if d := unlockRecheckOverride.Load(); d != 0 {
		return time.Duration(d)
	}
	return defaultUnlockRecheckInterval
}

// unlockWatch is envUnlocked for a connection that outlives its request
// (safe-defaults rule 15): it keeps the token that unlocked the upgrade and
// re-checks it with the same check middleware.EnvUnlock made, so a token
// that expires or is revoked locks the connection too (agent-os-rbmi).
//
// It fails closed: a request that never passed through middleware.EnvUnlock
// has no check, and reads as locked.
type unlockWatch struct {
	mu    sync.Mutex
	token string
	check middleware.EnvUnlockCheck
}

func newUnlockWatch(c *gin.Context) *unlockWatch {
	w := &unlockWatch{token: c.GetString(middleware.CtxEnvUnlockToken)}
	if v, ok := c.Get(middleware.CtxEnvUnlockCheck); ok {
		w.check, _ = v.(middleware.EnvUnlockCheck)
	}
	return w
}

// unlocked reports whether the connection's unlock is still live.
func (w *unlockWatch) unlocked() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.check != nil && w.check(w.token)
}

// renew swaps in token when it is a live unlock token for the same user, the
// rule the header path applies, and reports whether it did. A token that
// fails the check changes nothing: it can never extend the connection.
func (w *unlockWatch) renew(token string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.check == nil || token == "" || !w.check(token) {
		return false
	}
	w.token = token
	return true
}
