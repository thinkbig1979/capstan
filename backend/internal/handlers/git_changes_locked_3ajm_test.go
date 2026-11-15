package handlers

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// secret3ajm is a stack .env value under a sensitive key.
const secret3ajm = "AJM3-file-name-secret-5d2"

// TestLockedGitChanges_FileNamesMasked_3ajm (agent-os-3ajm, review F2): a
// locked /git/changes listed a working-tree file whose name holds a stack
// secret verbatim, and /git/changes/diff named it in its path and headers.
// Locked, the list masks the name; a diff is served only for a name the
// locked list shows unmasked, and every other request (the masked name, the
// real name guessed) gets the same 400, so a guess cannot tell a hit from a
// miss. Unlocked, both routes are unchanged.
func TestLockedGitChanges_FileNamesMasked_3ajm(t *testing.T) {
	e := newGitWriteEnv(t, false)
	secretName := "note-" + secret3ajm + ".txt"
	k81pWrite(t, filepath.Join(e.top, ".env"), "DB_PASSWORD="+secret3ajm+"\n")
	k81pWrite(t, filepath.Join(e.top, secretName), "one\n")
	k81pWrite(t, filepath.Join(e.top, "plain.txt"), "one\n")
	k81pGit(t, e.top, "add", "-A")
	k81pGit(t, e.top, "commit", "-q", "-m", "seed files")
	k81pWrite(t, filepath.Join(e.top, secretName), "two\n")
	k81pWrite(t, filepath.Join(e.top, "plain.txt"), "two\n")

	diffPath := func(p string) string {
		return "/api/git/changes/diff?stackId=s-app&path=" + url.QueryEscape(p)
	}

	// Locked list: the name is masked.
	code, body := getW6wx(t, e.router, "/api/git/changes?stackId=s-app")
	require.Equal(t, http.StatusOK, code, body)
	assert.NotContains(t, body, secret3ajm, "locked /git/changes carries the secret")
	assert.Contains(t, body, "plain.txt")

	// Locked diff: the masked name and the guessed real name answer alike.
	codeMasked, bodyMasked := getW6wx(t, e.router, diffPath("note-***.txt"))
	codeGuess, bodyGuess := getW6wx(t, e.router, diffPath(secretName))
	assert.Equal(t, http.StatusBadRequest, codeMasked, bodyMasked)
	assert.Equal(t, http.StatusBadRequest, codeGuess, bodyGuess)
	assert.Equal(t, bodyMasked, bodyGuess, "a guessed real name must answer exactly as the masked name does")
	assert.NotContains(t, bodyGuess, secret3ajm)

	// Locked diff of a plain name is served.
	code, body = getW6wx(t, e.router, diffPath("plain.txt"))
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, "+two")

	// Unlocked: unchanged.
	unlocked := unlockedRouterW6wx(t, e)
	code, body = getW6wx(t, unlocked, "/api/git/changes?stackId=s-app")
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, secretName)
	code, body = getW6wx(t, unlocked, diffPath(secretName))
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, secretName)
}

// TestLockedGitChanges_RenameOrigPathMasked_3ajm (review R2-1 i): a staged
// rename of a secret-named file. Locked, the list masks origPath, and the
// diff of the new (plain) name masks the old name in its rename header.
func TestLockedGitChanges_RenameOrigPathMasked_3ajm(t *testing.T) {
	e := newGitWriteEnv(t, false)
	secretName := "note-" + secret3ajm + ".txt"
	k81pWrite(t, filepath.Join(e.top, ".env"), "DB_PASSWORD="+secret3ajm+"\n")
	k81pWrite(t, filepath.Join(e.top, secretName), "one\ntwo\nthree\n")
	k81pGit(t, e.top, "add", "-A")
	k81pGit(t, e.top, "commit", "-q", "-m", "seed")
	k81pGit(t, e.top, "mv", secretName, "renamed.txt")

	code, body := getW6wx(t, e.router, "/api/git/changes?stackId=s-app")
	require.Equal(t, http.StatusOK, code, body)
	assert.NotContains(t, body, secret3ajm, "locked origPath carries the secret")
	assert.Contains(t, body, `"origPath":"note-***.txt"`)

	code, body = getW6wx(t, e.router, "/api/git/changes/diff?stackId=s-app&path=renamed.txt")
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, "rename from note-***.txt", "the rename header is there, masked")
	assert.NotContains(t, body, secret3ajm, "locked rename header carries the secret")

	code, body = getW6wx(t, unlockedRouterW6wx(t, e), "/api/git/changes/diff?stackId=s-app&path=renamed.txt")
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, "rename from "+secretName, "unlocked is unchanged")
}

// TestLockedGitChanges_AmbiguousMaskedNameRefused_3ajm (review R2-1 ii): a
// file literally named "x-***.txt" and a file whose secret name masks to the
// same text. The locked request for "x-***.txt" is ambiguous and gets the
// fixed 400, never the diff of either file. The secret starts with "!" so
// its file sorts first and the literal one last.
func TestLockedGitChanges_AmbiguousMaskedNameRefused_3ajm(t *testing.T) {
	const secret = "!AJM3-amb-secret-9z"
	e := newGitWriteEnv(t, false)
	k81pWrite(t, filepath.Join(e.top, ".env"), "DB_PASSWORD="+secret+"\n")
	k81pWrite(t, filepath.Join(e.top, "x-"+secret+".txt"), "one\n")
	k81pWrite(t, filepath.Join(e.top, "x-***.txt"), "one\n")
	k81pGit(t, e.top, "add", "-A")
	k81pGit(t, e.top, "commit", "-q", "-m", "seed")
	k81pWrite(t, filepath.Join(e.top, "x-"+secret+".txt"), "two\n")
	k81pWrite(t, filepath.Join(e.top, "x-***.txt"), "two\n")

	code, body := getW6wx(t, e.router, "/api/git/changes?stackId=s-app")
	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, 2, strings.Count(body, `"path":"x-***.txt"`), "both changes show under the same name: %s", body)

	code, body = getW6wx(t, e.router, "/api/git/changes/diff?stackId=s-app&path="+url.QueryEscape("x-***.txt"))
	assert.Equal(t, http.StatusBadRequest, code, body)
	assert.Contains(t, body, "its name can only be read unlocked")
}
