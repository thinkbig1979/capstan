package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// submodulePullFix is a commitFix whose repository has a submodule "sub", and
// whose origin has one commit the work tree lacks that moves sub to a commit
// the work tree's submodule has never fetched: exactly the case where
// `git pull` fetches the submodule on demand. The work tree's submodule then
// points at an ssh URL whose core.sshCommand, and a fake `ssh` on PATH, record
// any run in the returned file.
func submodulePullFix(t *testing.T) (f *commitFix, record string) {
	t.Helper()
	f = newCommitFix(t, false)
	f.svc.config.DataDir = f.dataDir

	sub := filepath.Join(t.TempDir(), "sub")
	runGit(t, filepath.Dir(sub), "init", "-q", "-b", "main", sub)
	runGit(t, sub, "commit", "-q", "--allow-empty", "-m", "s1")
	runGit(t, f.top, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub, "sub")
	runGit(t, f.top, "commit", "-q", "-m", "add sub")
	runGit(t, f.top, "push", "-q", "origin", "main")

	other := filepath.Join(t.TempDir(), "other")
	runGit(t, filepath.Dir(other), "clone", "-q", f.bare, other)
	runGit(t, other, "-c", "protocol.file.allow=always", "submodule", "update", "-q", "--init")
	runGit(t, sub, "commit", "-q", "--allow-empty", "-m", "s2")
	runGit(t, filepath.Join(other, "sub"), "-c", "protocol.file.allow=always", "pull", "-q", "origin", "main")
	runGit(t, other, "commit", "-q", "-am", "move sub")
	runGit(t, other, "push", "-q", "origin", "main")

	bin := t.TempDir()
	record = filepath.Join(t.TempDir(), "ssh.record")
	script := func(label string) string {
		return "#!/bin/sh\necho \"" + label + " $*\" >> " + record + "\nexit 255\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(bin, "ssh"), []byte(script("default-ssh")), 0o755)) //nolint:gosec // test-owned fake under t.TempDir()
	marker := filepath.Join(bin, "sub-ssh-command")
	require.NoError(t, os.WriteFile(marker, []byte(script("sub core.sshCommand")), 0o755)) //nolint:gosec // test-owned fake under t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	runGit(t, filepath.Join(f.top, "sub"), "config", "remote.origin.url", "ssh://git@example.invalid/o/sub.git")
	runGit(t, filepath.Join(f.top, "sub"), "config", "core.sshCommand", marker)
	return f, record
}

// TestPull1vkh_NeverFetchesSubmodules is agent-os-1vkh: git pull fetched a
// changed submodule on demand, over the submodule's own ssh URL and
// core.sshCommand, even for a folder set to https auth that agent-os-rde7
// refuses ssh for at the top level. With submodule.recurse=true pull also
// passes --recurse-submodules itself, which outranks a
// `-c fetch.recurseSubmodules=no` pin (OBSERVED, git 2.39.5 and 2.47.3), so
// that case is tested on its own.
func TestPull1vkh_NeverFetchesSubmodules(t *testing.T) {
	for name, recurse := range map[string]bool{"default config": false, "submodule.recurse=true": true} {
		t.Run(name, func(t *testing.T) {
			f, record := submodulePullFix(t)
			require.NoError(t, f.db.UpdateDirectoryCredentials(f.dir, "https", "", "1vkh-user", "1vkh-token-9d2e"))
			if recurse {
				runGit(t, f.top, "config", "submodule.recurse", "true")
			}
			before := f.head(t)

			res, err := f.svc.pullCLI(f.dir)

			assert.Empty(t, rde7Ran(t, record), "an ssh child ran for the submodule")
			require.NoError(t, err)
			// Control: the superproject itself still fast-forwards.
			assert.Equal(t, before, res.PreviousCommit)
			assert.NotEqual(t, before, f.head(t), "the pull did not move HEAD")
			assert.Equal(t, f.remoteMain(t), f.head(t))
		})
	}
}

// TestPull1vkh_InstrumentSeesSubmoduleSsh is the positive side of the
// instrument above: plain git on the same fixture does run the submodule's
// ssh, so an empty record there is the fix, not a fixture that never fetches.
func TestPull1vkh_InstrumentSeesSubmoduleSsh(t *testing.T) {
	f, record := submodulePullFix(t)
	_, err := gitTry(t, f.top, "pull", "--ff-only")
	require.Error(t, err, "the submodule fetch over the fake ssh should fail the plain pull")
	assert.Contains(t, rde7Ran(t, record), "sub core.sshCommand")
}

// workingSubmodulePullFix is submodulePullFix with a submodule that CAN be
// fetched: a local path, file transport allowed, so a recursing pull would
// update it. Its remote.origin.uploadpack records every fetch from it into
// the returned file and then serves it.
func workingSubmodulePullFix(t *testing.T) (f *commitFix, record string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_PARAMETERS", "'protocol.file.allow=always'")
	f = newCommitFix(t, false)
	f.svc.config.DataDir = f.dataDir

	sub := filepath.Join(t.TempDir(), "sub")
	runGit(t, filepath.Dir(sub), "init", "-q", "-b", "main", sub)
	runGit(t, sub, "commit", "-q", "--allow-empty", "-m", "s1")
	runGit(t, f.top, "submodule", "add", "-q", sub, "sub")
	runGit(t, f.top, "commit", "-q", "-m", "add sub")
	runGit(t, f.top, "push", "-q", "origin", "main")

	other := filepath.Join(t.TempDir(), "other")
	runGit(t, filepath.Dir(other), "clone", "-q", f.bare, other)
	runGit(t, other, "submodule", "update", "-q", "--init")
	runGit(t, sub, "commit", "-q", "--allow-empty", "-m", "s2")
	runGit(t, filepath.Join(other, "sub"), "pull", "-q", "origin", "main")
	runGit(t, other, "commit", "-q", "-am", "move sub")
	runGit(t, other, "push", "-q", "origin", "main")

	record = filepath.Join(t.TempDir(), "uploadpack.record")
	wrapper := filepath.Join(t.TempDir(), "uploadpack")
	require.NoError(t, os.WriteFile(wrapper, []byte("#!/bin/sh\necho \"uploadpack $*\" >> "+record+"\nexec git upload-pack \"$@\"\n"), 0o755)) //nolint:gosec // test-owned fake under t.TempDir()
	runGit(t, filepath.Join(f.top, "sub"), "config", "remote.origin.uploadpack", wrapper)
	runGit(t, f.top, "config", "submodule.recurse", "true")
	return f, record
}

// TestPull1vkh_LeavesSubmoduleWorkTreeBehind pins the trade the flag makes
// (D177): with submodule.recurse=true a plain pull would fetch AND update the
// submodule; Capstan's pull does neither, so the folder then reads as having
// uncommitted changes and the next pull is refused until
// `git submodule update` runs on the host. The positive side is the same
// fixture under plain git, which does fetch and update.
func TestPull1vkh_LeavesSubmoduleWorkTreeBehind(t *testing.T) {
	t.Run("capstan pull", func(t *testing.T) {
		f, record := workingSubmodulePullFix(t)
		subBefore := runGit(t, filepath.Join(f.top, "sub"), "rev-parse", "HEAD")

		_, err := f.svc.pullCLI(f.dir)

		require.NoError(t, err)
		assert.Equal(t, f.remoteMain(t), f.head(t), "the superproject did not fast-forward")
		assert.Empty(t, rde7Ran(t, record), "the submodule was fetched")
		assert.Equal(t, subBefore, runGit(t, filepath.Join(f.top, "sub"), "rev-parse", "HEAD"), "the submodule work tree was updated")
		assert.Equal(t, "M sub", runGit(t, f.top, "status", "--porcelain"))
		_, err = f.svc.pullCLI(f.dir)
		var ae *models.AppError
		require.ErrorAs(t, err, &ae)
		assert.Equal(t, models.ErrGitDirty, ae.Code, "the documented consequence: the next pull is refused")
	})
	t.Run("plain git on the same fixture", func(t *testing.T) {
		f, record := workingSubmodulePullFix(t)
		subBefore := runGit(t, filepath.Join(f.top, "sub"), "rev-parse", "HEAD")
		runGit(t, f.top, "pull", "-q", "--ff-only")
		assert.Contains(t, rde7Ran(t, record), "uploadpack")
		assert.NotEqual(t, subBefore, runGit(t, filepath.Join(f.top, "sub"), "rev-parse", "HEAD"))
	})
}
