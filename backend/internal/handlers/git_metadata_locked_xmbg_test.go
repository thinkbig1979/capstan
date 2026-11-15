package handlers

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// secretXmbg is a stack .env value under a sensitive key, so it is in the
// stack's secret list (StackSecretValues).
const secretXmbg = "XMBG-commit-secret-pw-71c"

// TestLockedGitMetadata_MaskedForLockedSession_xmbg: a commit message, an
// author and a changed file's name holding a stack secret reached a locked
// session verbatim through /git/log, /git/diff/:hash and /git, while the
// patch text was masked (agent-os-xmbg). Locked, none of the three replies
// carries the value; unlocked, all three still do.
func TestLockedGitMetadata_MaskedForLockedSession_xmbg(t *testing.T) {
	e := newGitWriteEnv(t, false)
	k81pWrite(t, filepath.Join(e.top, ".env"), "DB_PASSWORD="+secretXmbg+"\n")
	k81pWrite(t, filepath.Join(e.top, "note-"+secretXmbg+".txt"), "hello\n")
	k81pGit(t, e.top, "add", "-A")
	k81pGit(t, e.top, "commit", "-q", "--author", secretXmbg+" <a@b.invalid>", "-m", "rotate to "+secretXmbg)
	head := e.head(t)

	paths := []string{
		"/api/git/log?stackId=s-app",
		"/api/git/diff/" + head + "?stackId=s-app",
		"/api/git?stackId=s-app",
	}
	unlocked := unlockedRouterW6wx(t, e)
	for _, p := range paths {
		code, body := getW6wx(t, e.router, p)
		require.Equal(t, http.StatusOK, code, "%s: %s", p, body)
		assert.NotContains(t, body, secretXmbg, "locked %s carries the secret", p)

		code, body = getW6wx(t, unlocked, p)
		require.Equal(t, http.StatusOK, code, "%s: %s", p, body)
		assert.Contains(t, body, secretXmbg, "unlocked %s must show the commit as it is", p)
	}
}

// TestLockedGitDiff_OpenQuoteDoesNotSpillAcrossFiles_xmbg (review F1): the
// file-name pass over a locked patch ran MaskSecrets over the whole diff, and
// an unclosed quote under a sensitive key in one file starred every later
// line, the next files' headers included. The pass now masks header lines
// one at a time, so the later files read as the per-file masking left them.
func TestLockedGitDiff_OpenQuoteDoesNotSpillAcrossFiles_xmbg(t *testing.T) {
	e := newGitWriteEnv(t, false)
	k81pWrite(t, filepath.Join(e.top, "README.md"), "Set API_TOKEN=\"your token here, see docs\n")
	k81pWrite(t, filepath.Join(e.top, "compose.yaml"), "services:\n  web:\n    image: nginx:1.27\n")
	k81pWrite(t, filepath.Join(e.top, "x"), "plain line\n")
	k81pGit(t, e.top, "add", "-A")
	k81pGit(t, e.top, "commit", "-q", "-m", "three files")

	code, body := getW6wx(t, e.router, "/api/git/diff/"+e.head(t)+"?stackId=s-app")
	require.Equal(t, http.StatusOK, code, body)
	var resp struct {
		Diff string `json:"diff"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	assert.Equal(t, 3, strings.Count(resp.Diff, "diff --git "), "every file keeps its header: %s", resp.Diff)
	assert.Contains(t, resp.Diff, "diff --git a/compose.yaml b/compose.yaml")
	assert.Contains(t, resp.Diff, "image: nginx:1.27", "a later file's body must not be starred by the README's open quote")
	assert.Contains(t, resp.Diff, "plain line")
}
