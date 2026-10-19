package services

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// Review round 2 of agent-os-2omy: the reviewer's r3b probes, made
// assertions, for both views where the shared parser is involved.

// changeIsEnv is the dialog's verdict for one changed path.
func changeIsEnv(t *testing.T, f *commitFix, path string) bool {
	t.Helper()
	got, err := f.svc.GetChanges(f.dir)
	require.NoError(t, err)
	for _, c := range got.Changes {
		if c.Path == path {
			return c.IsEnv
		}
	}
	t.Fatalf("%s is not in the change list %v", path, changePaths(got))
	return false
}

// Compose merges every YAML document of a file, so an env_file in the second
// one names a file too.
func TestHistoryDiff_MultiDocumentCompose(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "compose.yaml", "services:\n  app:\n    image: x\n---\nservices:\n  app:\n    env_file: secrets.conf\n")
	f.write(t, "secrets.conf", "S=0\n")
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	f.register(t, "", "s", "")
	f.write(t, "secrets.conf", "MULTIDOC_SECRET=1\n")
	f.write(t, "x.txt", "MULTIDOC_PUBLIC\n")
	h := f.commit(t, "edit")

	requireHidden(t, f.diff(t, "", h), "MULTIDOC_SECRET", "MULTIDOC_PUBLIC", "secrets.conf")
}

func TestGetChanges_MultiDocumentCompose(t *testing.T) {
	f := newCommitFix(t, false)
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services:\n  app:\n    image: x\n---\nservices:\n  app:\n    env_file: secrets.conf\n")
	writeFile(t, filepath.Join(f.top, "secrets.conf"), "0\n")
	writeFile(t, filepath.Join(f.top, "plain.txt"), "0\n")
	runGit(t, f.top, "add", "-A")
	runGit(t, f.top, "commit", "-q", "-m", "compose")
	writeFile(t, filepath.Join(f.top, "secrets.conf"), "1\n")
	writeFile(t, filepath.Join(f.top, "plain.txt"), "1\n")

	assert.True(t, changeIsEnv(t, f, "secrets.conf"), "named by the second document")
	assert.False(t, changeIsEnv(t, f, "plain.txt"), "control")
}

// A YAML error on a file reached through include: (here an env file) must not
// carry the file's values into the log, in either view.
func TestComposeErrorsKeepValuesOutOfTheLog(t *testing.T) {
	t.Run("history", func(t *testing.T) {
		f := newHistFix(t)
		f.write(t, "compose.yaml", "include:\n  - .env\nservices:\n  app:\n    image: x\n")
		f.write(t, ".env", "hunter2\n")
		f.write(t, "x.txt", "x\n")
		f.commit(t, "seed")
		f.register(t, "", "s", "")
		f.write(t, ".env", "pw4LOGX\n")
		f.write(t, "x.txt", "LOG_PUBLIC\n")
		h := f.commit(t, "edit")
		logs := debugSlog(t)

		res := f.diff(t, "", h)
		assert.Contains(t, res.HiddenFiles, "x.txt", "the unreadable include hides all but compose files")
		require.Contains(t, logs.String(), "Hiding every file but the compose files", "control: the failure is logged")
		assert.NotContains(t, logs.String(), "pw4LOGX")
		assert.NotContains(t, logs.String(), "hunter2")
	})
	t.Run("dialog", func(t *testing.T) {
		f := newCommitFix(t, false)
		writeFile(t, filepath.Join(f.top, "compose.yaml"), "include:\n  - inc.conf\nservices:\n  app:\n    image: x\n")
		writeFile(t, filepath.Join(f.top, "inc.conf"), "hunter2\n")
		runGit(t, f.top, "add", "-A")
		runGit(t, f.top, "commit", "-q", "-m", "compose")
		writeFile(t, filepath.Join(f.top, "inc.conf"), "pw4LOGX\n")
		logs := debugSlog(t)

		assert.True(t, changeIsEnv(t, f, "inc.conf"))
		require.Contains(t, logs.String(), "Treating every changed file of the folder as an env file", "control: the failure is logged")
		assert.NotContains(t, logs.String(), "pw4LOGX")
	})
}

// getWithin runs fn with a bound, so a hang fails the test instead of the run.
func getWithin(t *testing.T, d time.Duration, unblock func(), fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Errorf("did not return within %s", d)
		unblock()
		<-done
	}
}

// openFIFOWriter unblocks a reader stuck opening the FIFO at p.
func openFIFOWriter(p string) func() {
	return func() {
		//nolint:gosec // test-owned FIFO under t.TempDir()
		if w, err := os.OpenFile(p, os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
	}
}

// A FIFO reached through include: is unreadable, never opened: opening it for
// reading would block the request until something writes to it.
func TestComposeIncludeFIFODoesNotHang(t *testing.T) {
	t.Run("history", func(t *testing.T) {
		f := newHistFix(t)
		f.write(t, "compose.yaml", "include:\n  - pipe.yaml\nservices:\n  app:\n    image: x\n")
		f.write(t, "x.txt", "x\n")
		f.commit(t, "seed")
		f.register(t, "", "s", "")
		pipe := filepath.Join(f.top, "pipe.yaml")
		require.NoError(t, syscall.Mkfifo(pipe, 0o600))
		f.write(t, "x.txt", "FIFO_PUBLIC\n")
		runGit(t, f.top, "add", "x.txt")
		runGit(t, f.top, "commit", "-q", "-m", "edit")
		h := runGit(t, f.top, "rev-parse", "HEAD")

		var res *models.DiffResult
		var err error
		getWithin(t, 10*time.Second, openFIFOWriter(pipe), func() { res, err = f.svc.GetDiff(f.top, h) })
		require.NoError(t, err)
		assert.Contains(t, res.HiddenFiles, "x.txt", "an unreadable include hides all but compose files")
	})
	t.Run("dialog", func(t *testing.T) {
		f := newCommitFix(t, false)
		writeFile(t, filepath.Join(f.top, "compose.yaml"), "include:\n  - pipe.yaml\nservices:\n  app:\n    image: x\n")
		writeFile(t, filepath.Join(f.top, "plain.txt"), "0\n")
		runGit(t, f.top, "add", "-A")
		runGit(t, f.top, "commit", "-q", "-m", "compose")
		pipe := filepath.Join(f.top, "pipe.yaml")
		require.NoError(t, syscall.Mkfifo(pipe, 0o600))
		writeFile(t, filepath.Join(f.top, "plain.txt"), "1\n")

		var isEnv bool
		getWithin(t, 10*time.Second, openFIFOWriter(pipe), func() { isEnv = changeIsEnv(t, f, "plain.txt") })
		assert.True(t, isEnv, "an unreadable include hides all but compose files")
	})
}

// A ".." after a link climbs from where the link led: .env -> lnk/../secret
// with lnk -> deep/dir is deep/secret, as the kernel reads it. Links gone
// today.
func TestHistoryDiff_DotDotAfterFolderLink(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "deep/dir/keep", "k\n")
	f.write(t, "deep/secret", "S=0\n")
	f.write(t, "secret", "decoy\n")
	f.write(t, "x.txt", "x\n")
	require.NoError(t, os.Symlink("deep/dir", filepath.Join(f.top, "lnk")))
	require.NoError(t, os.Symlink("lnk/../secret", filepath.Join(f.top, ".env")))
	b, err := os.ReadFile(filepath.Join(f.top, ".env"))
	require.NoError(t, err)
	require.Equal(t, "S=0\n", string(b), "fixture: the kernel resolves .env to deep/secret")
	f.commit(t, "seed")
	f.write(t, "deep/secret", "DOTDOT_SECRET=1\n")
	f.write(t, "x.txt", "DOTDOT_PUBLIC\n")
	h := f.commit(t, "edit")
	require.NoError(t, os.Remove(filepath.Join(f.top, ".env")))
	require.NoError(t, os.Remove(filepath.Join(f.top, "lnk")))
	f.commit(t, "drop links")

	requireHidden(t, f.diff(t, "", h), "DOTDOT_SECRET", "DOTDOT_PUBLIC", "deep/secret")
}

// A compose file that was a link leaving the repository named something no
// tree here shows: unknown, so all but compose files hide, not "missing".
func TestHistoryDiff_ComposeLinkLeavingRepoIsUnreadable(t *testing.T) {
	f := newHistFix(t)
	outside := filepath.Join(t.TempDir(), "compose.yaml")
	writeFile(t, outside, "services:\n  app:\n    image: x\n    env_file: ../secrets.conf\n")
	require.NoError(t, os.Symlink(outside, filepath.Join(f.top, "compose.yaml")))
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	f.write(t, "x.txt", "MAYBE_LINKED_COMPOSE\n")
	h := f.commit(t, "edit")
	require.NoError(t, os.Remove(filepath.Join(f.top, "compose.yaml")))
	f.commit(t, "drop link")

	res := f.diff(t, "", h)
	assert.NotContains(t, res.Diff, "MAYBE_LINKED_COMPOSE")
	assert.Equal(t, []string{"x.txt"}, res.HiddenFiles)
}

// The dialog only hides more than before the parser followed include:: when
// another compose file is unreadable, a file reached through include: is
// hidden like any other non-compose file; only the stacks' own compose files
// show.
func TestGetChanges_UnreadableComposeKeepsIncludedFilesHidden(t *testing.T) {
	f := newCommitFix(t, false)
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "include:\n  - inc.yaml\nservices:\n  app:\n    image: x\n")
	writeFile(t, filepath.Join(f.top, "inc.yaml"), "services:\n  db:\n    image: y\n")
	writeFile(t, filepath.Join(f.top, "bad.yaml"), "services:\n  b:\n    image: y\n    env_file: ${F}\n")
	runGit(t, f.top, "add", "-A")
	runGit(t, f.top, "commit", "-q", "-m", "compose")
	require.NoError(t, f.db.UpsertStack(models.Stack{ID: "s-bad", Directory: f.dir, ComposeFile: "bad.yaml", ProjectName: "bad"}))
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "include:\n  - inc.yaml\nservices:\n  app:\n    image: x2\n")
	writeFile(t, filepath.Join(f.top, "inc.yaml"), "services:\n  db:\n    image: y2\n")

	assert.True(t, changeIsEnv(t, f, "inc.yaml"), "an included file is not a stack's compose file")
	assert.False(t, changeIsEnv(t, f, "compose.yaml"), "control: the stack's own compose file shows")
}

// Only the compose read (cat-file) touches the missing object here: the
// compose file is unchanged, so show and diff-tree never read it. So this
// fails when catBlob alone loses GIT_NO_LAZY_FETCH.
func TestHistoryDiff_NoLazyFetchInCatBlob(t *testing.T) {
	if ok, v := gitAtLeast(t, 2, 45); !ok {
		t.Skipf("git %s ignores GIT_NO_LAZY_FETCH (needs 2.45 or later)", v)
	}
	f := newHistFix(t)
	marker := filepath.Join(t.TempDir(), "marks")
	ssh := writeMarkerScript(t, marker, "exit 1\n")
	f.write(t, "compose.yaml", "services:\n  app:\n    image: x\n")
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	f.write(t, "x.txt", "CAT_LAZY_PUBLIC\n")
	h := f.commit(t, "edit")
	blob := runGit(t, f.top, "rev-parse", h+":compose.yaml")
	require.NoError(t, os.Remove(filepath.Join(f.top, ".git", "objects", blob[:2], blob[2:])))
	for k, v := range map[string]string{
		"core.repositoryformatversion": "1", "extensions.partialClone": "origin",
		"remote.origin.url": "ssh://example.invalid/x", "remote.origin.promisor": "true",
		"core.sshCommand": ssh,
	} {
		runGit(t, f.top, "config", k, v)
	}

	// The compose blob is missing, so the rule can't be built: fail closed.
	_, err := f.svc.GetDiff(f.top, h)
	assert.Error(t, err)
	assert.Empty(t, markerLines(t, marker), "a lazy fetch in cat-file ran the repository's core.sshCommand")

	out, err := gitTry(t, f.top, "cat-file", "blob", blob)
	t.Logf("control cat-file: %v %s", err, out)
	assert.NotEmpty(t, markerLines(t, marker), "control: a plain cat-file must try the lazy fetch")
}
