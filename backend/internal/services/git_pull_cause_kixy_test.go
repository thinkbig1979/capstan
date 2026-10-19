package services

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// kixyPullLines returns the log lines the pull failure wrote.
func kixyPullLines(logged string) []string {
	var lines []string
	for _, l := range strings.Split(logged, "\n") {
		if strings.Contains(l, "git pull failed") {
			lines = append(lines, l)
		}
	}
	return lines
}

// TestKixy_ClassifiedPullFailureLogsRedactedCause is agent-os-kixy: the 502
// folds a refused credential and an unreachable host into one answer, and its
// Details (git's own text) reach neither the response nor the log, so the log
// was the one place to tell them apart and said nothing.
func TestKixy_ClassifiedPullFailureLogsRedactedCause(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(t *testing.T, local string) (token string)
		wantCause string
	}{
		{"unreachable remote", func(t *testing.T, local string) string {
			runGit(t, local, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
			return testGitToken
		}, "does not appear to be a git repository"},
		// Git echoes a local remote path in its error text, so this is the case
		// where redaction is the only thing keeping the token out of the log.
		{"token in the remote path", func(t *testing.T, local string) string {
			runGit(t, local, "remote", "set-url", "origin", filepath.Join(t.TempDir(), testGitToken, "missing.git"))
			return testGitToken
		}, "does not appear to be a git repository"},
		{"wrong token", func(t *testing.T, local string) string { return testGitToken + "-wrong" }, "Authentication failed"},
		{"token embedded in the remote url", func(t *testing.T, local string) string {
			// The configured token is also the password in the URL, which
			// git can echo back in its error text.
			remote := runGit(t, local, "remote", "get-url", "origin")
			token := testGitToken + "-wrong"
			runGit(t, local, "remote", "set-url", "origin",
				strings.Replace(remote, "http://", "http://"+testGitUser+":"+token+"@", 1))
			return token
		}, "Authentication failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			local, advance := authenticatedHTTPRepo(t)
			advance()
			token := tc.setup(t, local)
			svc := gitServiceWithDirectoryCredential(t, local, "https", testGitUser, token)
			buf := captureSlog(t)

			_, err := svc.pullCLI(local)

			var appErr *models.AppError
			require.True(t, errors.As(err, &appErr), "err %v", err)
			require.Equal(t, http.StatusBadGateway, appErr.Status)
			assert.Equal(t, "Could not read from the git remote", appErr.Message, "the response text must not change")
			lines := kixyPullLines(buf.String())
			require.Len(t, lines, 1, "log:\n%s", buf.String())
			assert.Contains(t, lines[0], tc.wantCause)
			assert.Contains(t, lines[0], models.ErrGitRemoteUnreachable)
			assert.NotContains(t, buf.String(), token, "the token reached the log")
		})
	}
}

// TestKixy_DivergedPullLogsItsCause: the 409 is a 4xx, so no request log
// carries git's text for it either.
func TestKixy_DivergedPullLogsItsCause(t *testing.T) {
	work, seed, _ := pullFixture(t)
	mustGit(t, seed, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "remote side")
	mustGit(t, seed, "push", "-q", runGit(t, work, "remote", "get-url", "origin"), "HEAD:refs/heads/"+fixtureBranch)
	mustGit(t, work, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "local side")
	svc := gitServiceWithDirectoryCredential(t, work, "https", testGitUser, testGitToken)
	buf := captureSlog(t)

	_, err := svc.pullCLI(work)

	var appErr *models.AppError
	require.True(t, errors.As(err, &appErr), "err %v", err)
	require.Equal(t, models.ErrGitConflict, appErr.Code)
	lines := kixyPullLines(buf.String())
	require.Len(t, lines, 1, "log:\n%s", buf.String())
	assert.Contains(t, lines[0], models.ErrGitConflict)
	assert.Contains(t, lines[0], "git pull")
}
