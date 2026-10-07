package middleware

import (
	"testing"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/config"
)

// processLimiters lists the seven goroutine-owning limiters InitRateLimiters
// builds: three login layers, three password-check layers, and the API limiter.
func processLimiters(t *testing.T) []*RateLimiter {
	t.Helper()
	if authLimiters == nil || passwordCheckLimiters == nil || apiRateLimiter == nil {
		t.Fatal("InitRateLimiters has not built the process limiters")
	}
	return []*RateLimiter{
		authLimiters.perIP, authLimiters.perIPAccount, authLimiters.perAccount,
		passwordCheckLimiters.perIP, passwordCheckLimiters.perIPAccount, passwordCheckLimiters.perAccount,
		apiRateLimiter,
	}
}

func cleanupRunning(rl *RateLimiter) bool {
	select {
	case <-rl.cleanupDone:
		return false
	default:
		return true
	}
}

func waitCleanupExit(t *testing.T, rl *RateLimiter, what string) {
	t.Helper()
	select {
	case <-rl.cleanupDone:
	case <-time.After(2 * time.Second):
		t.Errorf("%s: cleanup goroutine still running 2s after Stop", what)
	}
}

// Stop must end the cleanup goroutine (safe-defaults rule 3). The "running
// before Stop" half is the control: without it a goroutine that never started
// would pass the "exited after Stop" half.
func TestRateLimiter_StopEndsCleanup_z91e17(t *testing.T) {
	rl := NewRateLimiter(time.Minute, 10)
	t.Cleanup(rl.Stop)

	if !cleanupRunning(rl) {
		t.Fatal("cleanup goroutine was not running before Stop")
	}
	rl.Stop()
	waitCleanupExit(t, rl, "Stop")

	rl.Stop() // idempotent: a second close would panic
	if !rl.check("203.0.113.5") {
		t.Error("a stopped limiter must keep counting requests, not refuse them")
	}
}

func TestStopRateLimiters_StopsAllSeven_z91e17(t *testing.T) {
	InitRateLimiters(config.DefaultAPIRateLimitPerMin)
	t.Cleanup(StopRateLimiters)

	limiters := processLimiters(t)
	if len(limiters) != 7 {
		t.Fatalf("expected 7 process limiters, got %d", len(limiters))
	}
	for i, rl := range limiters {
		if !cleanupRunning(rl) {
			t.Fatalf("limiter %d was not running before StopRateLimiters", i)
		}
	}

	StopRateLimiters()
	for i, rl := range limiters {
		waitCleanupExit(t, rl, "StopRateLimiters limiter "+string(rune('0'+i)))
	}

	StopRateLimiters() // idempotent
}

// A second InitRateLimiters replaces the set; the first set's goroutines must
// not be orphaned.
func TestInitRateLimiters_ReInitStopsPrevious_z91e17(t *testing.T) {
	InitRateLimiters(config.DefaultAPIRateLimitPerMin)
	t.Cleanup(StopRateLimiters)
	previous := processLimiters(t)

	InitRateLimiters(config.DefaultAPIRateLimitPerMin)
	for i, rl := range previous {
		waitCleanupExit(t, rl, "re-Init previous limiter "+string(rune('0'+i)))
	}
	for i, rl := range processLimiters(t) {
		if !cleanupRunning(rl) {
			t.Errorf("new limiter %d is not running after re-Init", i)
		}
	}
}

func TestStopRateLimiters_BeforeInitIsSafe_z91e17(t *testing.T) {
	savedAuth, savedPw, savedAPI := authLimiters, passwordCheckLimiters, apiRateLimiter
	authLimiters, passwordCheckLimiters, apiRateLimiter = nil, nil, nil
	t.Cleanup(func() { authLimiters, passwordCheckLimiters, apiRateLimiter = savedAuth, savedPw, savedAPI })

	StopRateLimiters()
}
