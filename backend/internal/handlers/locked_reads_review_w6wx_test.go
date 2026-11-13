package handlers

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-w6wx review (sec D2), through a real `git diff`: removing a line
// that starts with "-- " (an SQL comment) prints it as "--- ...". The masker
// took that for a file header, ended the hunk, and printed every line after
// it unmasked.
func TestLockedReads_RemovedSQLCommentDoesNotUnmaskTheRest_w6wx(t *testing.T) {
	e := newGitWriteEnv(t, false)
	k81pWrite(t, filepath.Join(e.top, "init.sql"), "-- setup\nexport API_TOKEN=W6WX-SQL-SECRET\nALTER ROLE app SET x=1;\n")
	k81pGit(t, e.top, "add", "init.sql")
	k81pGit(t, e.top, "commit", "-q", "-m", "add init.sql")
	k81pWrite(t, filepath.Join(e.top, "init.sql"), "++ added\nexport API_TOKEN=W6WX-SQL-SECRET\nALTER ROLE app SET x=2;\n")
	const path = "/api/git/changes/diff?stackId=s-app&path=init.sql"

	code, body := getW6wx(t, unlockedRouterW6wx(t, e), path)
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `--- setup`, "precondition: git prints the removed comment as \"--- setup\"")
	require.Contains(t, body, `+++ added`, "precondition: git prints the added line as \"+++ added\"")
	require.Contains(t, body, "W6WX-SQL-SECRET", "unlocked shows the file")

	code, body = getW6wx(t, e.router, path)
	require.Equal(t, http.StatusOK, code, body)
	assert.NotContains(t, body, "W6WX-SQL-SECRET", "locked")
	assert.Contains(t, body, "API_TOKEN=***")
	assert.Contains(t, body, "x=2", "the real change is still shown")
}
