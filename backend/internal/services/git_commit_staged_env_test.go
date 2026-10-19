package services

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-7l76: the commit dialog's env rule also reads the index, which is
// what a commit takes, and today's disk copy of every compose-named file the
// index tracks, whether or not a stack is registered for it.

// stagedEnvStackFix stages a new stack folder app/extra, registered for no
// stack, whose compose file names key.conf (not env-named), with key.conf
// and a plain readme.txt staged beside it. Neither is in HEAD.
func stagedEnvStackFix(t *testing.T) *commitFix {
	t.Helper()
	f := newCommitFix(t, true)
	writeFile(t, filepath.Join(f.dir, "extra/compose.yaml"), "services:\n  x:\n    image: x\n    env_file: key.conf\n")
	writeFile(t, filepath.Join(f.dir, "extra/key.conf"), odjsSecret+"=1\n")
	writeFile(t, filepath.Join(f.dir, "extra/readme.txt"), "plain readme\n")
	runGit(t, f.top, "add", "-A")
	return f
}

// stagedEnvHashObject writes body as a blob and returns its object name.
func stagedEnvHashObject(t *testing.T, dir, body string) string {
	t.Helper()
	cmd := exec.Command("git", "hash-object", "-w", "--stdin")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(body)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.TrimSpace(string(out))
}

// Shape (a): the compose file names the secret only in the index (the change
// is staged, the work tree's compose file reverted), and the secret is
// modified.
func TestStagedEnvComposeNamesSecretOnlyInIndex(t *testing.T) {
	f := newCommitFix(t, true)
	writeFile(t, filepath.Join(f.dir, "sub/secrets.conf"), odjsSecret+"=1\n")
	runGit(t, f.top, "add", "-A")
	runGit(t, f.top, "commit", "-q", "-m", "secret, compose names nothing")
	writeFile(t, filepath.Join(f.dir, "compose.yaml"), "services:\n  app:\n    image: x\n    env_file: sub/secrets.conf\n")
	runGit(t, f.top, "add", "app/compose.yaml")
	writeFile(t, filepath.Join(f.dir, "compose.yaml"), "services: {}\n")
	writeFile(t, filepath.Join(f.dir, "sub/secrets.conf"), odjsSecret+"=2\n")

	requireOdjsHidden(t, f, f.dir, "app/sub/secrets.conf")
}

// Shape (b): a new stack folder no stack is registered for, its compose file
// and secret both staged, neither in HEAD.
func TestStagedEnvUnregisteredNewStack(t *testing.T) {
	t.Run("staged_and_on_disk", func(t *testing.T) {
		f := stagedEnvStackFix(t)
		requireOdjsHidden(t, f, f.dir, "app/extra/key.conf")
	})
	// Only the index names it: the disk copy no longer does.
	t.Run("staged_only", func(t *testing.T) {
		f := stagedEnvStackFix(t)
		writeFile(t, filepath.Join(f.dir, "extra/compose.yaml"), "services: {}\n")
		requireOdjsHidden(t, f, f.dir, "app/extra/key.conf")
	})
}

// A tracked compose file of an unregistered folder, edited on disk only (not
// staged), names a tracked file: the next commit's compose file may name it.
func TestStagedEnvUnregisteredComposeEditedOnDiskOnly(t *testing.T) {
	f := newCommitFix(t, true)
	writeFile(t, filepath.Join(f.dir, "extra/compose.yaml"), "services: {}\n")
	writeFile(t, filepath.Join(f.dir, "extra/key.conf"), odjsSecret+"=1\n")
	runGit(t, f.top, "add", "-A")
	runGit(t, f.top, "commit", "-q", "-m", "unregistered stack, names nothing")
	writeFile(t, filepath.Join(f.dir, "extra/compose.yaml"), "services:\n  x:\n    image: x\n    env_file: key.conf\n")
	writeFile(t, filepath.Join(f.dir, "extra/key.conf"), odjsSecret+"=2\n")

	requireOdjsHidden(t, f, f.dir, "app/extra/key.conf")
}

// A conflicted compose file has no stage 0; each side it is held in names its
// files. Planted with update-index, so no merge is in progress to refuse the
// commit first; the conflict sits in a sibling folder, so app/'s change list
// does not refuse it either.
func TestStagedEnvConflictStageNamesSecret(t *testing.T) {
	f := newCommitFix(t, true)
	writeFile(t, filepath.Join(f.dir, "sub/secrets.conf"), odjsSecret+"=1\n")
	runGit(t, f.top, "add", "-A")
	runGit(t, f.top, "commit", "-q", "-m", "secret, compose names nothing")
	base := stagedEnvHashObject(t, f.top, "services: {}\n")
	ours := stagedEnvHashObject(t, f.top, "services:\n  o:\n    image: x\n")
	theirs := stagedEnvHashObject(t, f.top, "services:\n  o:\n    image: x\n    env_file: ../app/sub/secrets.conf\n")
	cmd := exec.Command("git", "update-index", "--index-info")
	cmd.Dir = f.top
	cmd.Stdin = strings.NewReader("0 0000000000000000000000000000000000000000\tother/compose.yaml\n" +
		"100644 " + base + " 1\tother/compose.yaml\n" +
		"100644 " + ours + " 2\tother/compose.yaml\n" +
		"100644 " + theirs + " 3\tother/compose.yaml\n")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	require.Contains(t, runGit(t, f.top, "ls-files", "-u"), " 3\tother/compose.yaml", "the conflict is planted")
	writeFile(t, filepath.Join(f.dir, "sub/secrets.conf"), odjsSecret+"=2\n")

	requireOdjsHidden(t, f, f.dir, "app/sub/secrets.conf")
}

// An unborn branch has no HEAD, but its index names files: here only the
// index, as the disk copy no longer does. GetChanges refuses an unborn
// repository before the rule (GetStatus); CommitVerified does not.
func TestStagedEnvUnbornBranchReadsIndex(t *testing.T) {
	f := newCommitFix(t, true)
	runGit(t, f.top, "checkout", "-q", "--orphan", "fresh")
	writeFile(t, filepath.Join(f.dir, "extra/compose.yaml"), "services:\n  x:\n    image: x\n    env_file: key.conf\n")
	writeFile(t, filepath.Join(f.dir, "extra/key.conf"), odjsSecret+"=1\n")
	runGit(t, f.top, "add", "-A")
	writeFile(t, filepath.Join(f.dir, "extra/compose.yaml"), "services: {}\n")

	ar, _ := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"app/extra/key.conf"}})
	requireRefusal(t, ar, http.StatusBadRequest, models.ErrGitEnvConfirmRequired)
}

// D162 on the index side: a compose file staged whose blob can't be read
// hides every change but the compose files, so a plain change needs
// confirmEnv.
func TestStagedEnvIndexReadFaultHides(t *testing.T) {
	f := newCommitFix(t, true)
	oid := stagedEnvHashObject(t, f.top, "services:\n  x:\n    image: x\n    env_file: key.conf\n")
	runGit(t, f.top, "update-index", "--add", "--cacheinfo", "100644,"+oid+",app/extra/compose.yaml")
	require.NoError(t, os.Remove(filepath.Join(f.top, ".git", "objects", oid[:2], oid[2:])))
	writeFile(t, filepath.Join(f.dir, "a.txt"), "changed plain\n")

	requireOdjsHidden(t, f, f.dir, "app/a.txt")
}

// D162 on the disk side: an unregistered compose-named file that no longer
// parses on disk (HEAD and the index hold a good one) makes every commit ask
// confirmEnv, as a registered stack's unreadable compose file does.
func TestStagedEnvBrokenUnregisteredComposeOnDiskHides(t *testing.T) {
	f := newCommitFix(t, true)
	writeFile(t, filepath.Join(f.dir, "extra/compose.yaml"), "services: {}\n")
	runGit(t, f.top, "add", "-A")
	runGit(t, f.top, "commit", "-q", "-m", "unregistered stack")
	writeFile(t, filepath.Join(f.dir, "extra/compose.yaml"), "services: [\n")
	writeFile(t, filepath.Join(f.dir, "a.txt"), "changed plain\n")

	requireOdjsHidden(t, f, f.dir, "app/a.txt")
}

// The control: a plain file staged beside the new stack stays a plain file
// while the index rule hides its secret.
func TestStagedEnvPlainStagedFileShown(t *testing.T) {
	f := stagedEnvStackFix(t)
	p := "app/extra/readme.txt"

	got, err := f.svc.GetChanges(f.dir)
	require.NoError(t, err)
	var change *models.GitChange
	for i := range got.Changes {
		if got.Changes[i].Path == p {
			change = &got.Changes[i]
		}
	}
	require.NotNil(t, change, "%s is not listed: %+v", p, got.Changes)
	assert.False(t, change.IsEnv, "GetChanges: %s IsEnv", p)

	d, err := f.svc.GetChangeDiff(f.dir, p)
	require.NoError(t, err)
	assert.False(t, d.Hidden, "GetChangeDiff: %s Hidden", p)
	assert.Contains(t, d.Diff, "+plain readme")

	ar, _ := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{p}})
	require.Equal(t, "success", string(ar.Outcome), "reason %q err %v", ar.Reason, ar.Err)
}

// ls-files -s lists no sizes, so a staged compose file's size is read before
// its blob: one past maxComposeBytes is unknown, not read whole, and hides
// every change but the compose files. Only the index holds it.
func TestStagedEnvOversizedIndexComposeHides(t *testing.T) {
	f := newCommitFix(t, true)
	big := "services: {}\n" + strings.Repeat("# padding\n", maxComposeBytes/10+1)
	writeFile(t, filepath.Join(f.dir, "extra/compose.yaml"), big)
	runGit(t, f.top, "add", "app/extra/compose.yaml")
	require.NoError(t, os.Remove(filepath.Join(f.dir, "extra/compose.yaml")))
	writeFile(t, filepath.Join(f.dir, "a.txt"), "changed plain\n")

	requireOdjsHidden(t, f, f.dir, "app/a.txt")
}
