package services

import (
	"bytes"
	"fmt"
	"log/slog"
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

// agent-os-odjs: the commit dialog's env rule is built from today's disk AND
// HEAD, so a file a compose file named at HEAD stays an env file when the
// compose side changes, and an absolute env_file: entry through a symlinked
// stacks root still maps once the file it names is gone.

const odjsSecret = "TOPSECRET"

// odjsFix is a shared fixture (stack app/) whose compose file names
// sub/secrets.conf (not env-named) through entry, all committed.
func odjsFix(t *testing.T, entry func(f *commitFix) string) *commitFix {
	t.Helper()
	f := newCommitFix(t, true)
	writeFile(t, filepath.Join(f.dir, "compose.yaml"), "services:\n  app:\n    image: x\n    env_file: "+entry(f)+"\n")
	writeFile(t, filepath.Join(f.dir, "sub/secrets.conf"), odjsSecret+"=1\n")
	writeFile(t, filepath.Join(f.dir, "sub/plain.txt"), "p\n")
	runGit(t, f.top, "add", "-A")
	runGit(t, f.top, "commit", "-q", "-m", "env files")
	return f
}

func odjsRelEntry(*commitFix) string { return "sub/secrets.conf" }

// odjsAbsFix registers app/ through a symlinked stacks root and names the
// secret by its absolute path through that link. It returns the link path of
// the stack folder.
func odjsAbsFix(t *testing.T) (*commitFix, string) {
	t.Helper()
	link := filepath.Join(t.TempDir(), "stacks")
	var linkApp string
	f := odjsFix(t, func(f *commitFix) string {
		require.NoError(t, os.Symlink(filepath.Dir(f.top), link))
		linkApp = filepath.Join(link, filepath.Base(f.top), "app")
		return filepath.Join(linkApp, "sub/secrets.conf")
	})
	f.registerStack(t, linkApp, "s-app", "app")
	f.registerStack(t, filepath.Join(filepath.Dir(linkApp), "other"), "s-other", "other")
	return f, linkApp
}

// requireOdjsHidden asserts p is an env file on every consumer: listed with
// IsEnv, its diff hidden with no content, and a commit refused without
// confirmEnv.
func requireOdjsHidden(t *testing.T, f *commitFix, dir, p string) {
	t.Helper()
	got, err := f.svc.GetChanges(dir)
	require.NoError(t, err)
	var change *models.GitChange
	for i := range got.Changes {
		if got.Changes[i].Path == p {
			change = &got.Changes[i]
		}
	}
	require.NotNil(t, change, "%s is not listed: %+v", p, got.Changes)
	assert.True(t, change.IsEnv, "GetChanges: %s IsEnv", p)

	d, err := f.svc.GetChangeDiff(dir, p)
	require.NoError(t, err)
	assert.True(t, d.Hidden, "GetChangeDiff: %s Hidden", p)
	assert.NotContains(t, d.Diff, odjsSecret, "GetChangeDiff served the secret")

	ar, _ := f.svc.CommitVerified(dir, CommitRequest{Message: "m", Files: []string{p}})
	requireRefusal(t, ar, http.StatusBadRequest, models.ErrGitEnvConfirmRequired)
}

func TestOdjsCommitDialogHidesSecretWhenComposeSideChanged(t *testing.T) {
	const secret = "app/sub/secrets.conf"
	rm := func(t *testing.T, f *commitFix, rel string) {
		t.Helper()
		require.NoError(t, os.Remove(filepath.Join(f.dir, rel)))
	}
	dropEnvFile := func(t *testing.T, f *commitFix) {
		t.Helper()
		writeFile(t, filepath.Join(f.dir, "compose.yaml"), "services:\n  app:\n    image: x\n")
	}
	modify := func(t *testing.T, f *commitFix) {
		t.Helper()
		writeFile(t, filepath.Join(f.dir, "sub/secrets.conf"), odjsSecret+"=2\n")
	}

	cases := []struct {
		name   string
		change func(t *testing.T, f *commitFix)
		path   string
	}{
		{"control_only_secret_deleted", func(t *testing.T, f *commitFix) { rm(t, f, "sub/secrets.conf") }, secret},
		{"control_only_secret_modified", modify, secret},
		{"both_deleted_unstaged", func(t *testing.T, f *commitFix) {
			rm(t, f, "sub/secrets.conf")
			rm(t, f, "compose.yaml")
		}, secret},
		{"both_deleted_staged", func(t *testing.T, f *commitFix) {
			runGit(t, f.top, "rm", "-q", "app/sub/secrets.conf", "app/compose.yaml")
		}, secret},
		{"compose_deleted_secret_modified", func(t *testing.T, f *commitFix) {
			modify(t, f)
			rm(t, f, "compose.yaml")
		}, secret},
		{"compose_drops_env_file_secret_deleted", func(t *testing.T, f *commitFix) {
			dropEnvFile(t, f)
			rm(t, f, "sub/secrets.conf")
		}, secret},
		{"compose_drops_env_file_secret_modified", func(t *testing.T, f *commitFix) {
			dropEnvFile(t, f)
			modify(t, f)
		}, secret},
		{"compose_renamed_staged_secret_deleted", func(t *testing.T, f *commitFix) {
			runGit(t, f.top, "mv", "app/compose.yaml", "app/docker-compose.yml")
			rm(t, f, "sub/secrets.conf")
		}, secret},
		// The rename's original path is the named one.
		{"secret_renamed_staged_compose_deleted", func(t *testing.T, f *commitFix) {
			runGit(t, f.top, "mv", "app/sub/secrets.conf", "app/sub/moved.conf")
			rm(t, f, "compose.yaml")
		}, "app/sub/moved.conf"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := odjsFix(t, odjsRelEntry)
			tc.change(t, f)
			requireOdjsHidden(t, f, f.dir, tc.path)
		})
	}

	// Still committable once confirmed: the fix asks for more, it blocks nothing.
	t.Run("both_deleted_commits_with_confirm", func(t *testing.T) {
		f := odjsFix(t, odjsRelEntry)
		rm(t, f, "sub/secrets.conf")
		rm(t, f, "compose.yaml")
		ar, _ := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{secret}, ConfirmEnv: true})
		require.Equal(t, "success", string(ar.Outcome), "reason %q err %v", ar.Reason, ar.Err)
		assert.Equal(t, []string{secret}, f.committedFiles(t))
	})
}

func TestOdjsCommitDialogAbsoluteEnvFileThroughSymlinkedRoot(t *testing.T) {
	const secret = "app/sub/secrets.conf"
	t.Run("control_secret_modified", func(t *testing.T) {
		f, linkApp := odjsAbsFix(t)
		writeFile(t, filepath.Join(f.dir, "sub/secrets.conf"), odjsSecret+"=2\n")
		requireOdjsHidden(t, f, linkApp, secret)
	})
	t.Run("secret_deleted_registered_by_link", func(t *testing.T) {
		f, linkApp := odjsAbsFix(t)
		require.NoError(t, os.Remove(filepath.Join(f.dir, "sub/secrets.conf")))
		requireOdjsHidden(t, f, linkApp, secret)
	})
	t.Run("secret_deleted_registered_by_real_path", func(t *testing.T) {
		f, _ := odjsAbsFix(t)
		f.registerStack(t, f.dir, "s-app", "app")
		require.NoError(t, os.Remove(filepath.Join(f.dir, "sub/secrets.conf")))
		requireOdjsHidden(t, f, f.dir, secret)
	})
	// Only today's compose file names it, so HEAD can't: the disk rule's own
	// resolution has to map the missing file.
	// No HEAD rule on an unborn branch: only the disk rule's own resolution
	// maps the secret, staged and then deleted from disk.
	t.Run("unborn_secret_staged_then_deleted", func(t *testing.T) {
		f, linkApp := odjsAbsFix(t)
		runGit(t, f.top, "checkout", "-q", "--orphan", "fresh")
		require.NoError(t, os.Remove(filepath.Join(f.dir, "sub/secrets.conf")))
		ar, _ := f.svc.CommitVerified(linkApp, CommitRequest{Message: "m", Files: []string{secret}})
		requireRefusal(t, ar, http.StatusBadRequest, models.ErrGitEnvConfirmRequired)
	})
	t.Run("disk_only_entry_secret_deleted", func(t *testing.T) {
		f, linkApp := odjsAbsFix(t)
		writeFile(t, filepath.Join(f.dir, "compose.yaml"), "services:\n  app:\n    image: x\n")
		runGit(t, f.top, "commit", "-q", "-am", "drop env_file")
		writeFile(t, filepath.Join(f.dir, "compose.yaml"),
			"services:\n  app:\n    image: x\n    env_file: "+filepath.Join(linkApp, "sub/secrets.conf")+"\n")
		require.NoError(t, os.Remove(filepath.Join(f.dir, "sub/secrets.conf")))
		requireOdjsHidden(t, f, linkApp, secret)
	})
}

// The HEAD rule is the history view's rule, so the dialog also hides what that
// rule hides beyond the disk rule (D144): a file inside an env-named folder,
// and a file named by any compose-named file in HEAD, registered or not.
func TestOdjsCommitDialogInheritsHistoryRuleHiding(t *testing.T) {
	t.Run("file_in_env_named_folder", func(t *testing.T) {
		f := newCommitFix(t, true)
		writeFile(t, filepath.Join(f.dir, "env/config.txt"), odjsSecret+"=1\n")
		runGit(t, f.top, "add", "-A")
		runGit(t, f.top, "commit", "-q", "-m", "env folder")
		writeFile(t, filepath.Join(f.dir, "env/config.txt"), odjsSecret+"=2\n")
		requireOdjsHidden(t, f, f.dir, "app/env/config.txt")
	})
	t.Run("named_by_unregistered_compose_in_head", func(t *testing.T) {
		f := newCommitFix(t, true)
		writeFile(t, filepath.Join(f.dir, "extra/compose.yaml"), "services:\n  x:\n    image: x\n    env_file: key.conf\n")
		writeFile(t, filepath.Join(f.dir, "extra/key.conf"), odjsSecret+"=1\n")
		runGit(t, f.top, "add", "-A")
		runGit(t, f.top, "commit", "-q", "-m", "unregistered compose")
		writeFile(t, filepath.Join(f.dir, "extra/key.conf"), odjsSecret+"=2\n")
		requireOdjsHidden(t, f, f.dir, "app/extra/key.conf")
	})
}

// A changed path that can't be resolved is an env file (fail closed). Here a
// folder became a file, so resolving a deleted file under it is ENOTDIR,
// which is not "does not exist".
func TestOdjsCommitDialogUnresolvablePathIsEnv(t *testing.T) {
	f := newCommitFix(t, true)
	writeFile(t, filepath.Join(f.dir, "dir/plain.txt"), "p\n")
	runGit(t, f.top, "add", "-A")
	runGit(t, f.top, "commit", "-q", "-m", "dir")
	require.NoError(t, os.RemoveAll(filepath.Join(f.dir, "dir")))
	writeFile(t, filepath.Join(f.dir, "dir"), "now a file\n")

	got, err := f.svc.GetChanges(f.dir)
	require.NoError(t, err)
	for _, c := range got.Changes {
		if c.Path == "app/dir/plain.txt" {
			assert.True(t, c.IsEnv, "an unresolvable changed path is flagged")
			return
		}
	}
	t.Fatalf("app/dir/plain.txt is not listed: %+v", got.Changes)
}

// A HEAD object the rule needs is missing: every change but the compose files
// is hidden, and the dialog still opens.
func TestOdjsCommitDialogHeadReadFaultHides(t *testing.T) {
	f := odjsFix(t, odjsRelEntry)
	oid := runGit(t, f.top, "rev-parse", "HEAD:app/compose.yaml")
	require.NoError(t, os.Remove(filepath.Join(f.top, ".git", "objects", oid[:2], oid[2:])))
	writeFile(t, filepath.Join(f.dir, "sub/plain.txt"), "changed\n")
	writeFile(t, filepath.Join(f.dir, "compose.yaml"), "services:\n  app:\n    image: y\n    env_file: sub/secrets.conf\n")

	requireOdjsHidden(t, f, f.dir, "app/sub/plain.txt")

	got, err := f.svc.GetChanges(f.dir)
	require.NoError(t, err)
	for _, c := range got.Changes {
		if c.Path == "app/compose.yaml" {
			assert.False(t, c.IsEnv, "the stack's own compose file stays visible")
			return
		}
	}
	t.Fatalf("app/compose.yaml is not listed: %+v", got.Changes)
}

// An unborn branch has no HEAD to read; that is not a fault, so a plain file
// commits without confirmEnv. GetChanges refuses an unborn repository before
// the rule (GetStatus), CommitVerified does not.
func TestOdjsCommitDialogUnbornHeadHidesNothingExtra(t *testing.T) {
	f := newCommitFix(t, true)
	runGit(t, f.top, "checkout", "-q", "--orphan", "fresh")
	writeFile(t, filepath.Join(f.dir, "new.txt"), "n\n")
	runGit(t, f.top, "add", "app/new.txt")

	ar, _ := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"app/new.txt"}})
	require.Equal(t, "success", string(ar.Outcome), "reason %q err %v", ar.Reason, ar.Err)
}

// A plain change stays a plain change while the HEAD rule is in force: the
// union must not hide everything.
func TestOdjsCommitDialogPlainFileStaysVisibleWithHeadRule(t *testing.T) {
	f := odjsFix(t, odjsRelEntry)
	writeFile(t, filepath.Join(f.dir, "sub/plain.txt"), "plain change\n")

	rule, err := f.svc.envRule(f.dir, f.top, "", "")
	require.NoError(t, err)
	require.NotNil(t, rule.head, "the HEAD rule is built")
	assert.False(t, rule.composeUnreadable, "the HEAD rule hides nothing extra here")

	got, err := f.svc.GetChanges(f.dir)
	require.NoError(t, err)
	var change *models.GitChange
	for i := range got.Changes {
		if got.Changes[i].Path == "app/sub/plain.txt" {
			change = &got.Changes[i]
		}
	}
	require.NotNil(t, change, "app/sub/plain.txt is not listed: %+v", got.Changes)
	assert.False(t, change.IsEnv)
	d, err := f.svc.GetChangeDiff(f.dir, "app/sub/plain.txt")
	require.NoError(t, err)
	assert.False(t, d.Hidden)
	assert.Contains(t, d.Diff, "+plain change")
	ar, _ := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"app/sub/plain.txt"}})
	require.Equal(t, "success", string(ar.Outcome), "reason %q err %v", ar.Reason, ar.Err)
}

// D162: a compose-named file anywhere in HEAD whose env_file: is unknown (an
// unregistered template with ${ENV_FILE}) hides every change but the compose
// files, and a plain change needs confirmEnv, as D147 does for the history
// view. The log line names the commit dialog.
func TestOdjsCommitDialogUnknownEntryInHeadTemplateHidesAll(t *testing.T) {
	f := newCommitFix(t, true)
	buf := odjsCaptureSlog(t)
	writeFile(t, filepath.Join(f.top, "templates/compose.yaml"), "services:\n  x:\n    image: x\n    env_file: ${ENV_FILE}\n")
	runGit(t, f.top, "add", "-A")
	runGit(t, f.top, "commit", "-q", "-m", "template")
	writeFile(t, filepath.Join(f.dir, "a.txt"), "changed plain\n")

	requireOdjsHidden(t, f, f.dir, "app/a.txt")
	assert.Contains(t, buf.String(), "Hiding every file but the compose files in the commit dialog")
	assert.NotContains(t, buf.String(), "history diff")
}

// A committed link's target is the repository author's text; an error that
// quotes it is cut, so it can't fill a log line on every request.
func TestOdjsLongLinkTargetIsBoundedInLogsAndErrors(t *testing.T) {
	f := newCommitFix(t, true)
	buf := odjsCaptureSlog(t)
	target := "/" + strings.Repeat("TOPSECRETVALUE", 400)
	cmd := exec.Command("git", "hash-object", "-w", "--stdin")
	cmd.Dir = f.top
	cmd.Stdin = strings.NewReader(target)
	out, err := cmd.Output()
	require.NoError(t, err)
	runGit(t, f.top, "update-index", "--add", "--cacheinfo", "120000,"+strings.TrimSpace(string(out))+",app/.env.link")
	runGit(t, f.top, "commit", "-q", "-m", "link")
	writeFile(t, filepath.Join(f.dir, "a.txt"), "changed plain\n")

	_, err = f.svc.GetChanges(f.dir)
	require.NoError(t, err)
	// Never print the log or the error whole: on failure it holds the target.
	logged := buf.String()
	require.True(t, strings.Contains(logged, "the env files named at HEAD are unknown"), "the long target faults the HEAD rule")
	assert.False(t, strings.Contains(logged, target), "the log holds the whole target (%d bytes logged)", len(logged))
	assert.Less(t, strings.Count(logged, "TOPSECRETVALUE"), 2*200/len("TOPSECRETVALUE")+2, "at most two bounded copies")
	assert.True(t, strings.Contains(logged, fmt.Sprintf("(%d bytes)", len(target))), "the cut says how long the target was")

	_, err = f.svc.GetDiff(f.dir, f.head(t))
	if err != nil {
		assert.False(t, strings.Contains(err.Error(), target), "the history error holds the whole target (%d bytes)", len(err.Error()))
	}
}

func odjsCaptureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}
