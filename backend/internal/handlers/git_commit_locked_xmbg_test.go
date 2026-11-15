package handlers

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestGitCommit_LockedRefused_xmbg: since agent-os-xmbg a locked log or diff
// masks a commit message with the stack's secrets, so a locked commit of a
// message holding guesses would come back with the secret's position starred
// (the agent-os-ly6i echo oracle). A locked commit is refused before it runs
// and HEAD does not move; an unlocked commit still lands.
func TestGitCommit_LockedRefused_xmbg(t *testing.T) {
	e := newGitWriteEnv(t, false)
	k81pWrite(t, filepath.Join(e.top, "compose.yaml"), "services: {a: {image: x}}\n")
	before := e.head(t)
	const body = `{"stackId":"s-app","message":"guess1 guess2","files":["compose.yaml"]}`

	w := e.do(t, http.MethodPost, "/api/git/commit", body)
	requireGitCode(t, w, http.StatusForbidden, models.ErrEnvLocked)
	require.Equal(t, before, e.head(t), "a locked commit moved HEAD")

	w = e.doUnlocked(t, http.MethodPost, "/api/git/commit", body)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotEqual(t, before, e.head(t), "an unlocked commit must land")
}
