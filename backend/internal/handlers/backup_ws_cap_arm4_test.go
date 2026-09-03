package handlers

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestConnectionManager_UnmeteredRegistrationDoesNotInflateCap is arm 4, the
// landmine the bead exists to close: closeMatching (CloseForSession/
// CloseForUser) decrements userCounts unconditionally for every connection it
// matches, exactly like Remove. A naive AddUnmetered that inserts into
// cm.connections without incrementing userCounts gets DECREMENTED by both
// Remove and closeMatching, silently inflating every user's effective cap.
//
// This test cannot be "seen failing" against pre-fix code — AddUnmetered does
// not exist there. It was seen failing against a deliberately-reproduced naive
// AddUnmetered (insert into cm.connections only, no metered bookkeeping) before
// the real fix landed: after registering 3 metered + 2 unmetered connections
// for one user and revoking the unmetered ones via CloseForSession, CountByUser
// read 1 instead of the correct 3, and 9 further metered Adds were admitted
// against a cap of 10 (12 live metered connections total) instead of the
// correct 7. See the commit body for the exact transcript.
func TestConnectionManager_UnmeteredRegistrationDoesNotInflateCap(t *testing.T) {
	cm := NewConnectionManager(10)

	// 3 metered connections occupy real slots and must never be revoked here.
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("metered-keep-%d", i)
		require.NoError(t, cm.Add(id, &Connection{ID: id, UserID: "u1", SessionID: "keep"}))
	}
	require.Equal(t, 3, cm.CountByUser("u1"))

	// 2 unmetered connections must not touch userCounts.
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("unmetered-revoke-%d", i)
		cm.AddUnmetered(id, &Connection{ID: id, UserID: "u1", SessionID: "revoke"})
	}
	require.Equal(t, 3, cm.CountByUser("u1"), "an unmetered Add must not consume a cap slot")

	// Revoke the unmetered ones via the exact path production revocation uses.
	cm.CloseForSession("revoke")

	require.Equal(t, 3, cm.CountByUser("u1"),
		"closeMatching must not decrement userCounts for a connection that never incremented it")

	_, present := cm.Get("unmetered-revoke-0")
	require.False(t, present, "the revoked unmetered connection must actually be gone from the manager")
	_, present = cm.Get("unmetered-revoke-1")
	require.False(t, present)
	_, present = cm.Get("metered-keep-0")
	require.True(t, present, "the kept metered connection (different session) must still be live")

	// A correct manager allows exactly 7 more metered connections up to the
	// cap of 10 (3 already live). A manager whose accounting was inflated by
	// the landmine would wrongly admit more.
	for i := 0; i < 7; i++ {
		id := fmt.Sprintf("metered-fill-%d", i)
		require.NoError(t, cm.Add(id, &Connection{ID: id, UserID: "u1"}))
	}
	require.Equal(t, 10, cm.CountByUser("u1"))

	err := cm.Add("metered-overflow", &Connection{ID: "metered-overflow", UserID: "u1"})
	require.Error(t, err, "a metered Add at the cap must still refuse")
}
