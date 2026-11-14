package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-19kr: a save whose OWN compose or env file is a link to itself
// answered 500 "Failed to check which stacks share this stack's files", since
// lockStacksSharingFiles found the sharers by statting that file. The save now
// answers the write helper's refusal, which names the link.

const (
	composeBody19kr    = `{"content":"services:\n  web:\n    image: nginx\n"}`
	composeEnvBody19kr = `{"composeContent":"services:\n  web:\n    image: nginx\n","envRaw":"NEW=1\n"}`
)

type save19kr struct {
	method, path, body string
	okCode             int
}

// savesWriting19kr lists the saves that write file, the stack's own
// compose.yaml or .env.
func savesWriting19kr(file string) []save19kr {
	if file == "compose.yaml" {
		return []save19kr{
			{http.MethodPut, "/a/compose", composeBody19kr, http.StatusOK},
			{http.MethodPut, "/a/compose-env", composeEnvBody19kr, http.StatusOK},
		}
	}
	return []save19kr{
		{http.MethodPut, "/a/env", `{"raw":"NEW=1\n"}`, http.StatusOK},
		{http.MethodPost, "/a/env", `{"content":"NEW=1\n"}`, http.StatusCreated},
		{http.MethodPut, "/a/compose-env", composeEnvBody19kr, http.StatusOK},
	}
}

func TestSharedFileSave_LoopingOwnFile_19kr(t *testing.T) {
	for _, own := range []string{"compose.yaml", ".env"} {
		for _, tc := range savesWriting19kr(own) {
			for _, looping := range []bool{true, false} {
				name := own + " " + tc.method + " " + tc.path + " healthy"
				if looping {
					name = own + " " + tc.method + " " + tc.path + " looping"
				}
				t.Run(name, func(t *testing.T) {
					f := newLockOrderFixture(t)
					a := filepath.Join(f.root, "a")
					writeFile0j8a(t, filepath.Join(a, "compose.yaml"), "services: {}\n")
					writeFile0j8a(t, filepath.Join(a, ".env"), "OLD=1\n")
					p := filepath.Join(a, own)
					switch {
					case looping:
						require.NoError(t, os.Remove(p))
						require.NoError(t, os.Symlink(p, p))
					case tc.method == http.MethodPost:
						// Create refuses an env file that exists.
						require.NoError(t, os.Remove(p))
					}
					f.addStack(t, "a", a, "compose.yaml", ".env")

					rec := f.do(tc.method, tc.path, tc.body)

					if !looping {
						assert.Equal(t, tc.okCode, rec.Code, rec.Body.String())
						return
					}
					assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
					assert.Contains(t, rec.Body.String(), own+" is a symlink that cannot be followed")
					assert.Contains(t, rec.Body.String(), "fix or remove the link, then save again")
				})
			}
		}
	}
}

// TestSharedFileSave_LoopingStackFolder_19kr: a loop in the folder ABOVE the
// file is not the file's own link, and the write helper has no refusal for it
// (its Lstat fails, a plain error). The save keeps answering 500 for it.
func TestSharedFileSave_LoopingStackFolder_19kr(t *testing.T) {
	f := newLockOrderFixture(t)
	a := filepath.Join(f.root, "a")
	require.NoError(t, os.Symlink(a, a))
	f.addStack(t, "a", a, "compose.yaml", ".env")

	rec := f.do(http.MethodPut, "/a/compose", composeBody19kr)

	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "Failed to check which stacks share this stack's files")
}

// TestSharedFileSave_LongLinkChain_19kr: a chain of 50 links ending at a file
// inside the stack is ELOOP to the kernel (40 links) but not to Go's
// EvalSymlinks (255), so the write helper's link branch does not refuse it.
// The save answers 500, never a containment refusal claiming the file is
// outside.
func TestSharedFileSave_LongLinkChain_19kr(t *testing.T) {
	f := newLockOrderFixture(t)
	a := filepath.Join(f.root, "a")
	writeFile0j8a(t, filepath.Join(a, "real.yaml"), "services: {}\n")
	next := filepath.Join(a, "real.yaml")
	for i := range 50 {
		link := filepath.Join(a, "link"+strconv.Itoa(i))
		require.NoError(t, os.Symlink(next, link))
		next = link
	}
	require.NoError(t, os.Symlink(next, filepath.Join(a, "compose.yaml")))
	_, statErr := os.Stat(filepath.Join(a, "compose.yaml"))
	require.ErrorIs(t, statErr, syscall.ELOOP, "the fixture must be a chain the kernel refuses")
	f.addStack(t, "a", a, "compose.yaml", ".env")

	rec := f.do(http.MethodPut, "/a/compose", composeBody19kr)

	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "outside")
	assert.Contains(t, rec.Body.String(), "Failed to check which stacks share this stack's files")
}
