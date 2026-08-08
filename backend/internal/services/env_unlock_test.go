package services

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withClock returns a store whose clock the test controls, so expiry is provable
// without sleeping for five minutes.
func withClock(t *testing.T) (*EnvUnlockStore, func(time.Duration)) {
	t.Helper()
	base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	offset := time.Duration(0)
	store := NewEnvUnlockStore()
	store.now = func() time.Time { return base.Add(offset) }
	return store, func(d time.Duration) { offset += d }
}

func TestEnvUnlockStore_MintedTokenIsValidForItsUser(t *testing.T) {
	store, _ := withClock(t)

	token, ttl, err := store.Mint("user-a")
	require.NoError(t, err)
	require.NotEmpty(t, token)
	assert.Equal(t, EnvUnlockTTL, ttl)

	assert.True(t, store.Valid(token, "user-a"))
}

func TestEnvUnlockStore_TokenIsBoundToItsUser(t *testing.T) {
	store, _ := withClock(t)

	token, _, err := store.Mint("user-a")
	require.NoError(t, err)

	assert.False(t, store.Valid(token, "user-b"),
		"a token minted for one account must not unlock another's secrets")
}

func TestEnvUnlockStore_TokenExpires(t *testing.T) {
	store, advance := withClock(t)

	token, _, err := store.Mint("user-a")
	require.NoError(t, err)

	advance(EnvUnlockTTL - time.Second)
	assert.True(t, store.Valid(token, "user-a"), "should still be live just before the TTL")

	advance(2 * time.Second)
	assert.False(t, store.Valid(token, "user-a"), "should be dead just after the TTL")
}

func TestEnvUnlockStore_RejectsUnknownAndEmpty(t *testing.T) {
	store, _ := withClock(t)

	token, _, err := store.Mint("user-a")
	require.NoError(t, err)

	assert.False(t, store.Valid("not-a-token", "user-a"))
	assert.False(t, store.Valid("", "user-a"))
	assert.False(t, store.Valid(token, ""), "an unauthenticated caller has no userID to bind to")
}

func TestEnvUnlockStore_TokensAreReusableWithinTheWindow(t *testing.T) {
	store, advance := withClock(t)

	token, _, err := store.Mint("user-a")
	require.NoError(t, err)

	// The UI prompts once and then reveals freely for the rest of the window, so
	// a single-use token would force a prompt per reveal.
	for i := 0; i < 3; i++ {
		advance(30 * time.Second)
		assert.True(t, store.Valid(token, "user-a"), "reveal %d should still be unlocked", i)
	}
}

func TestEnvUnlockStore_MintingAgainKeepsTheEarlierToken(t *testing.T) {
	store, _ := withClock(t)

	first, _, err := store.Mint("user-a")
	require.NoError(t, err)
	second, _, err := store.Mint("user-a")
	require.NoError(t, err)

	require.NotEqual(t, first, second)
	// A second browser tab unlocking must not lock the first one out.
	assert.True(t, store.Valid(first, "user-a"))
	assert.True(t, store.Valid(second, "user-a"))
}

func TestEnvUnlockStore_RevokeUser(t *testing.T) {
	store, _ := withClock(t)

	mine, _, err := store.Mint("user-a")
	require.NoError(t, err)
	theirs, _, err := store.Mint("user-b")
	require.NoError(t, err)

	store.RevokeUser("user-a")

	assert.False(t, store.Valid(mine, "user-a"))
	assert.True(t, store.Valid(theirs, "user-b"), "revoking one user must not affect another")

	store.RevokeUser("") // no-op, must not wipe the map
	assert.True(t, store.Valid(theirs, "user-b"))
}

func TestEnvUnlockStore_PrunesExpiredTokens(t *testing.T) {
	store, advance := withClock(t)

	for i := 0; i < 5; i++ {
		_, _, err := store.Mint("user-a")
		require.NoError(t, err)
	}
	advance(EnvUnlockTTL + time.Second)

	// Minting prunes, so the dead entries must not accumulate unboundedly.
	live, _, err := store.Mint("user-a")
	require.NoError(t, err)

	store.mu.Lock()
	count := len(store.tokens)
	store.mu.Unlock()

	assert.Equal(t, 1, count, "expired tokens should have been pruned on mint")
	assert.True(t, store.Valid(live, "user-a"))
}

// TestEnvUnlockStore_ConcurrentUse is the guard for "make it concurrency-safe":
// run under -race it fails if the map is touched without the mutex.
func TestEnvUnlockStore_ConcurrentUse(t *testing.T) {
	store := NewEnvUnlockStore()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, _, err := store.Mint("user-a")
			if err != nil {
				t.Error(err)
				return
			}
			store.Valid(token, "user-a")
			store.RevokeUser("user-b")
		}()
	}
	wg.Wait()
}
