package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpdateContainer_StackLookupFaultRefuses is agent-os-z91e.37: when the
// stack lookup fails with an error other than not-found, the update cannot
// know whose lock to take, so it is refused with a 5xx and nothing is queued.
// Before the fix the error was logged, stackID stayed empty, and
// lockStackForUpdate's empty-ID shortcut let the update run with no stack
// lock (safe-defaults rules 4 and 10).
func TestUpdateContainer_StackLookupFaultRefuses(t *testing.T) {
	h := newUpdateContainerFixture(t)
	router := setupResourcesRouter(h)
	require.NoError(t, h.db.Close()) // every query now fails with "sql: database is closed"

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/resources/containers/solo/update", nil))

	assert.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "INTERNAL_ERROR", decodeBody(t, w)["code"])
	assert.Empty(t, h.jobManager.List(), "an update whose stack could not be looked up must not be queued")
}
