package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// rde7Ssh puts a fake `ssh` first on PATH and sets the repository's
// core.sshCommand to a marker program. Either one running is recorded in the
// returned file, so "no ssh child ran" is a fact read from it.
func rde7Ssh(t *testing.T, f *commitFix) (record string) {
	t.Helper()
	bin := t.TempDir()
	record = filepath.Join(t.TempDir(), "ssh.record")
	script := func(label string) string {
		return "#!/bin/sh\necho \"" + label + " $*\" >> " + record + "\nexit 255\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(bin, "ssh"), []byte(script("default-ssh")), 0o755)) //nolint:gosec // test-owned fake under t.TempDir()
	marker := filepath.Join(bin, "core-ssh-command")
	require.NoError(t, os.WriteFile(marker, []byte(script("core.sshCommand")), 0o755)) //nolint:gosec // test-owned fake under t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	runGit(t, f.top, "config", "core.sshCommand", marker)
	return record
}

func rde7Ran(t *testing.T, record string) string {
	t.Helper()
	b, err := os.ReadFile(record) //nolint:gosec // test-owned record file under t.TempDir()
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)
	return string(b)
}

// TestRde7_HttpsAuthSshRemote_PullIsRefused is agent-os-rde7: a folder set to
// https auth whose remote is ssh pulled with the container's default ssh
// identities, and ran the repository's core.sshCommand, while push refused it
// with GIT_SSH_UNSUPPORTED. The pull now gives push's answer before any ssh
// child starts.
func TestRde7_HttpsAuthSshRemote_PullIsRefused(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T, f *commitFix){
		"scp-like url": func(t *testing.T, f *commitFix) {
			runGit(t, f.top, "remote", "set-url", "origin", "git@example.invalid:o/r.git")
		},
		"ssh:// url": func(t *testing.T, f *commitFix) {
			runGit(t, f.top, "remote", "set-url", "origin", "ssh://git@example.invalid/o/r.git")
		},
		"insteadOf turning https into ssh": func(t *testing.T, f *commitFix) {
			runGit(t, f.top, "remote", "set-url", "origin", "https://example.invalid/o/r.git")
			runGit(t, f.top, "config", "url.ssh://git@example.invalid/.insteadOf", "https://example.invalid/")
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := pullFix(t)
			require.NoError(t, f.db.UpdateDirectoryCredentials(f.dir, "https", "", "rde7-user", "rde7-token-5c1f"))
			setup(t, f)
			record := rde7Ssh(t, f)
			before := f.head(t)

			_, err := f.svc.pullCLI(f.dir)

			msg := requireSSHCode(t, err, models.ErrGitSSHUnsupported)
			assert.Contains(t, msg, "pulls over SSH")
			assert.Empty(t, rde7Ran(t, record), "an ssh child ran: the refusal came too late")
			assert.Equal(t, before, f.head(t))
		})
	}
}

// TestRde7_HttpsAuthSshRemote_OthersUnchanged is the other side: what the new
// refusal must not touch.
func TestRde7_HttpsAuthSshRemote_OthersUnchanged(t *testing.T) {
	t.Run("https auth with a local remote still pulls", func(t *testing.T) {
		f := pullFix(t)
		require.NoError(t, f.db.UpdateDirectoryCredentials(f.dir, "https", "", "rde7-user", "rde7-token-5c1f"))
		res, err := f.svc.pullCLI(f.dir)
		require.NoError(t, err)
		assert.NotEqual(t, res.PreviousCommit, res.CurrentCommit)
	})
	t.Run("https auth with an https remote reaches the network", func(t *testing.T) {
		f := pullFix(t)
		require.NoError(t, f.db.UpdateDirectoryCredentials(f.dir, "https", "", "rde7-user", "rde7-token-5c1f"))
		runGit(t, f.top, "remote", "set-url", "origin", "http://127.0.0.1:1/o/r.git")
		_, err := f.svc.pullCLI(f.dir)
		var ae *models.AppError
		require.ErrorAs(t, err, &ae)
		assert.Equal(t, models.ErrGitRemoteUnreachable, ae.Code)
	})
	t.Run("inherit auth with no key keeps pulling over ssh as before", func(t *testing.T) {
		f := pullFix(t)
		require.NoError(t, f.db.UpdateDirectoryCredentials(f.dir, "inherit", "", "", ""))
		runGit(t, f.top, "remote", "set-url", "origin", "git@example.invalid:o/r.git")
		record := rde7Ssh(t, f)
		_, err := f.svc.pullCLI(f.dir)
		var ae *models.AppError
		require.ErrorAs(t, err, &ae)
		assert.NotEqual(t, models.ErrGitSSHUnsupported, ae.Code, "inherit auth was refused")
		assert.NotEmpty(t, rde7Ran(t, record), "the pull never reached ssh, so this arm measured nothing")
	})
	t.Run("ssh auth with a missing key keeps its own refusal", func(t *testing.T) {
		f := pullFix(t)
		require.NoError(t, f.db.UpdateDirectoryCredentials(f.dir, "ssh", filepath.Join(t.TempDir(), "missing"), "", ""))
		runGit(t, f.top, "remote", "set-url", "origin", "git@example.invalid:o/r.git")
		_, err := f.svc.pullCLI(f.dir)
		requireSSHCode(t, err, models.ErrGitSSHKeyUnusable)
	})
}
