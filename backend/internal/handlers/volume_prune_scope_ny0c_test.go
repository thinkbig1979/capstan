package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakePruneScope struct {
	all bool
	err error
}

func (f fakePruneScope) VolumePruneRemovesAllUnused(context.Context) (bool, error) {
	return f.all, f.err
}

// TestGetVolumePruneScope (agent-os-ny0c): the Volumes page reads this to warn
// about the default prune on an API below 1.42. A failed read must be an
// error, never removesAllUnused:false, which would skip the warning.
func TestGetVolumePruneScope(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scope    volumePruneScoper
		wantCode int
		wantBody string
	}{
		{"old API", fakePruneScope{all: true}, http.StatusOK, `{"removesAllUnused":true}`},
		{"current API", fakePruneScope{all: false}, http.StatusOK, `{"removesAllUnused":false}`},
		{"read fails", fakePruneScope{err: errors.New("ping failed")}, http.StatusInternalServerError, ""},
		{"no Docker", nil, http.StatusServiceUnavailable, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestResourcesHandler(t)
			h.pruneScope = tc.scope
			w := httptest.NewRecorder()
			setupResourcesRouter(h).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/resources/volumes/prune-scope", nil))
			require.Equal(t, tc.wantCode, w.Code, w.Body.String())
			if tc.wantBody != "" {
				assert.JSONEq(t, tc.wantBody, w.Body.String())
			} else {
				assert.NotContains(t, w.Body.String(), "removesAllUnused")
			}
		})
	}
}
