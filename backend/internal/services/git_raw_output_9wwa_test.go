package services

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-9wwa: git's DATA output is read raw for every decision and
// redacted only where it is shown. redactToken is a substring rewrite, so a
// token that also occurs in a hash, in "true", in a file name or in a
// committed file used to change what Capstan decided, not only what it showed.

// setToken9wwa stores token as dir's https credential and checks it reached
// the service, so a test cannot pass on a credential that never applied.
func setToken9wwa(t *testing.T, svc *GitService, db interface {
	UpsertDirectory(models.Directory) error
	UpdateDirectoryCredentials(path, authType, sshKeyPath, httpsUser, httpsToken string) error
}, dir, token string) {
	t.Helper()
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: dir, Name: filepath.Base(dir)}))
	require.NoError(t, db.UpdateDirectoryCredentials(dir, "https", "", "u-9wwa", token))
	require.Equal(t, token, svc.gitCredentials(dir).token, "the fixture's token did not reach the credentials")
}

// (a) A token that is a substring of HEAD's hash.
func TestHashTokenSubstring9wwa(t *testing.T) {
	t.Run("status serves the hash git printed", func(t *testing.T) {
		f := newCommitFix(t, false)
		head := f.head(t)
		setToken9wwa(t, f.svc, f.db, f.dir, head[4:8])

		st, err := f.svc.GetStatus(f.dir)
		require.NoError(t, err)
		assert.Equal(t, head, st.Commit.Hash)
		assert.Equal(t, head[:7], st.Commit.Short)
	})

	t.Run("pull reports the commits git moved between", func(t *testing.T) {
		f := newCommitFix(t, false)
		before := f.head(t)
		setToken9wwa(t, f.svc, f.db, f.dir, before[4:8])

		// No new commits: no change.
		res, err := f.svc.pullCLI(f.dir)
		require.NoError(t, err)
		assert.Equal(t, before, res.PreviousCommit)
		assert.Equal(t, res.PreviousCommit, res.CurrentCommit)
		assert.Empty(t, res.ChangedFiles)

		// One new commit upstream: both hashes as git printed them, and the
		// diff between them ran (it fails on a masked hash).
		other := filepath.Join(t.TempDir(), "other")
		runGit(t, filepath.Dir(other), "clone", "-q", f.bare, other)
		writeFile(t, filepath.Join(other, "x"), "changed\n")
		runGit(t, other, "commit", "-q", "-am", "upstream")
		runGit(t, other, "push", "-q", "origin", "main")
		after := runGit(t, other, "rev-parse", "HEAD")

		res, err = f.svc.pullCLI(f.dir)
		require.NoError(t, err)
		assert.Equal(t, before, res.PreviousCommit)
		assert.Equal(t, after, res.CurrentCommit)
		assert.Empty(t, res.DiffError)
		assert.Equal(t, []string{"x"}, res.ChangedFiles)
	})
}

// (b) Token "true": every --is-bare-repository site.
func TestIsBareTokenTrue9wwa(t *testing.T) {
	t.Run("status of a bare repository is bare", func(t *testing.T) {
		f := newCommitFix(t, false)
		setToken9wwa(t, f.svc, f.db, f.bare, "true")
		st, err := f.svc.GetStatus(f.bare)
		require.NoError(t, err)
		assert.True(t, st.IsBare)
	})
	t.Run("status of a work tree is not bare", func(t *testing.T) {
		f := newCommitFix(t, false)
		setToken9wwa(t, f.svc, f.db, f.dir, "true")
		st, err := f.svc.GetStatus(f.dir)
		require.NoError(t, err)
		assert.False(t, st.IsBare)
	})
	t.Run("pull into a bare repository is refused as bare", func(t *testing.T) {
		f := newCommitFix(t, false)
		setToken9wwa(t, f.svc, f.db, f.bare, "true")
		_, err := f.svc.pullCLI(f.bare)
		requireAppCode9wwa(t, err, models.ErrGitBareRepo)
	})
	t.Run("changes of a bare repository are refused as bare", func(t *testing.T) {
		f := newCommitFix(t, false)
		setToken9wwa(t, f.svc, f.db, f.bare, "true")
		_, err := f.svc.repoTopLevel(f.bare, f.svc.gitCredentials(f.bare))
		requireAppCode9wwa(t, err, models.ErrGitBareRepo)
	})
	t.Run("history of a bare repository has the folder as its top", func(t *testing.T) {
		f := newCommitFix(t, false)
		setToken9wwa(t, f.svc, f.db, f.bare, "true")
		top, bare, err := f.svc.historyTop(f.bare, f.svc.gitCredentials(f.bare))
		require.NoError(t, err)
		assert.True(t, bare)
		want, err := filepath.EvalSymlinks(f.bare)
		require.NoError(t, err)
		assert.Equal(t, want, top)
	})
}

func requireAppCode9wwa(t *testing.T, err error, code string) {
	t.Helper()
	var appErr *models.AppError
	require.True(t, errors.As(err, &appErr), "want AppError %s, got %v", code, err)
	assert.Equal(t, code, appErr.Code, "message: %s", appErr.Message)
}

// (c) The token in a committed file: the history env rule reads the compose
// file raw, while the patch the API returns still masks the token.
func TestHistoryRuleReadsRawBlob9wwa(t *testing.T) {
	t.Run("token in the compose content", func(t *testing.T) {
		f := newHistFix9wwa(t)
		f.write(t, "compose.yaml", "services:\n  app:\n    image: x\n    env_file: secrets.conf\n")
		f.write(t, "secrets.conf", "DB=0\n")
		f.write(t, "x.txt", "x\n")
		f.commit(t, "seed")
		f.register(t, "", "s", "")
		f.write(t, "secrets.conf", "RAW_SECRET=1\n")
		f.write(t, "x.txt", "RAW_PUBLIC env_file\n")
		h := f.commit(t, "edit")
		setToken9wwa(t, f.svc, f.db, f.top, "env_file")

		res := f.diff(t, "", h)
		requireHidden(t, res, "RAW_SECRET", "RAW_PUBLIC", "secrets.conf")
		assert.NotContains(t, res.Diff, "env_file", "the shown patch carries the token")
		assert.Contains(t, res.Diff, "RAW_PUBLIC ***")
	})
	t.Run("token in a removed stack's quoted env_file", func(t *testing.T) {
		// The compose file is only in history, so the rule reads it with
		// cat-file blob. Masked but still YAML, it named a file that does not
		// exist, and the real env file's contents were shown.
		f := newHistFix9wwa(t)
		f.write(t, "old/compose.yaml", "services:\n  app:\n    image: x\n    env_file: \"./secrets.conf\"\n")
		f.write(t, "old/secrets.conf", "DB=0\n")
		f.write(t, "x.txt", "x\n")
		f.commit(t, "seed")
		f.write(t, "old/secrets.conf", "GONE_SECRET=1\n")
		f.write(t, "x.txt", "GONE_PUBLIC\n")
		h := f.commit(t, "edit")
		require.NoError(t, os.RemoveAll(filepath.Join(f.top, "old")))
		f.commit(t, "remove old stack")
		setToken9wwa(t, f.svc, f.db, f.top, "./")

		requireHidden(t, f.diff(t, "", h), "GONE_SECRET", "GONE_PUBLIC", "old/secrets.conf")
	})
	t.Run("token in the env file's name", func(t *testing.T) {
		f := newHistFix9wwa(t)
		f.write(t, "compose.yaml", "services:\n  app:\n    image: x\n    env_file: secrets.conf\n")
		f.write(t, "secrets.conf", "DB=0\n")
		f.write(t, "x.txt", "x\n")
		f.commit(t, "seed")
		f.register(t, "", "s", "")
		f.write(t, "secrets.conf", "RAW_SECRET=1\n")
		f.write(t, "x.txt", "RAW_PUBLIC\n")
		h := f.commit(t, "edit")
		setToken9wwa(t, f.svc, f.db, f.top, "secrets")

		res := f.diff(t, "", h)
		assert.NotContains(t, res.Diff, "RAW_SECRET", "an env file's contents reached the history diff")
		assert.Contains(t, res.Diff, "RAW_PUBLIC")
		assert.Equal(t, []string{"***.conf"}, res.HiddenFiles, "the shown file list carries the token")
		assert.NotContains(t, strings.Join(res.Files, "\n"), "secrets")
	})
}

// newHistFix9wwa is newHistFix with a database that can store a token.
func newHistFix9wwa(t *testing.T) *histFix {
	t.Helper()
	f := newHistFix(t)
	db, err := database.NewWithMigrationsAndEncryptor(t.TempDir(), NewTokenEncryptorOrDefault("", "test-secret-key-for-unit-tests-32c"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	f.db = db
	f.svc = NewGitService(&config.Config{}, db)
	return f
}

// (d) KEEP-GREEN, green on base by design: every display boundary a raw read
// now feeds still masks the token.
func TestDisplayStaysRedacted9wwa(t *testing.T) {
	const token = "tok9wwa"
	f := newCommitFix(t, false)
	runGit(t, f.top, "checkout", "-q", "-b", "br-"+token)
	writeFile(t, filepath.Join(f.top, "f-"+token+".txt"), "body "+token+"\n")
	runGit(t, f.top, "add", "-A")
	runGit(t, f.top, "commit", "-q", "-m", "subject "+token,
		"--author=au-"+token+" <em-"+token+"@x.invalid>")
	runGit(t, f.top, "push", "-q", "-u", "origin", "br-"+token)
	runGit(t, f.top, "remote", "set-url", "origin", f.bare+"?"+token)
	// A local edit keeps the work tree dirty, so the status reads every field.
	writeFile(t, filepath.Join(f.top, "x"), "dirty "+token+"\n")
	setToken9wwa(t, f.svc, f.db, f.dir, token)
	head := f.head(t)

	st, err := f.svc.GetStatus(f.dir)
	require.NoError(t, err)
	for name, v := range map[string]string{
		"branch": st.Branch, "tracking": st.TrackingBranch, "remoteURL": st.RemoteURL,
		"author": st.Commit.Author, "email": st.Commit.Email, "subject": st.Commit.Message,
	} {
		assert.NotContains(t, v, token, "status %s", name)
		assert.Contains(t, v, "***", "status %s", name)
	}

	d, err := f.svc.GetDiff(f.dir, head)
	require.NoError(t, err)
	assert.NotContains(t, d.Diff, token)
	assert.NotContains(t, strings.Join(d.Files, "\n"), token)
	assert.NotContains(t, d.Commit.Message+d.Commit.Author+d.Commit.Email, token)

	lg, err := f.svc.GetLog(f.dir, 5, 0)
	require.NoError(t, err)
	require.NotEmpty(t, lg.Commits)
	assert.Equal(t, head, lg.Commits[0].Hash, "a hash is served as git printed it")
	assert.NotContains(t, lg.Commits[0].Message+lg.Commits[0].Author+lg.Commits[0].Email, token)
	assert.Contains(t, lg.Commits[0].Message, "***")

	writeFile(t, filepath.Join(f.top, "f-"+token+".txt"), "edited\n")
	ch, err := f.svc.GetChanges(f.dir)
	require.NoError(t, err)
	assert.NotContains(t, ch.Branch, token)
	var paths []string
	for _, c := range ch.Changes {
		paths = append(paths, c.Path)
	}
	assert.Contains(t, paths, "f-***.txt")
	assert.NotContains(t, strings.Join(paths, "\n"), token)

	// A refusal built from raw values: the push names the branch and the
	// remote it tracks, which is not a configured remote.
	runGit(t, f.top, "config", "branch.br-"+token+".remote", "nope-"+token)
	ar, w := f.svc.PushVerified(f.dir)
	require.Nil(t, w)
	var appErr *models.AppError
	require.True(t, errors.As(ar.Err, &appErr), "want an AppError, got %v", ar.Err)
	assert.Equal(t, models.ErrGitNoUpstream, appErr.Code)
	assert.NotContains(t, appErr.Message+fmt.Sprint(appErr.Details)+ar.Reason, token)
	assert.Contains(t, appErr.Message, "***")
}

// KEEP-GREEN: the pull's changed file names reach the response masked, while
// the redeploy decided on them as git printed them.
func TestPullShowsRedactedNames9wwa(t *testing.T) {
	const token = "tok9wwa"
	f := newCommitFix(t, false)
	setToken9wwa(t, f.svc, f.db, f.dir, token)
	other := filepath.Join(t.TempDir(), "other")
	runGit(t, filepath.Dir(other), "clone", "-q", f.bare, other)
	writeFile(t, filepath.Join(other, "n-"+token+".txt"), "n\n")
	runGit(t, other, "add", "-A")
	runGit(t, other, "commit", "-q", "-m", "upstream")
	runGit(t, other, "push", "-q", "origin", "main")

	ar, res := f.svc.PullVerified(f.dir, false, nil)
	require.NotNil(t, res, "pull failed: %s %v", ar.Reason, ar.Err)
	assert.Equal(t, []string{"n-***.txt"}, res.ChangedFiles)
	assert.NotContains(t, fmt.Sprint(ar.Details), token)
}

// TestRawGitCallers9wwa is the census of every function that reads git's
// stdout unredacted, through gitCommandUnredacted or gitRunUnredacted. That
// output can hold the token, so each caller is a place a credential could
// reach a message, a log or a response; a new caller fails here and has to
// be added deliberately, naming the boundary where its value is redacted.
// It replaces TestGitCommandUnredactedCallers0j5h. Read through go:embed
// (packageSources), so a `go test -overlay` mutant reaches it. A method
// value counts as a caller.
func TestRawGitCallers9wwa(t *testing.T) {
	// caller -> what it decides on, and the display boundary it feeds.
	want := map[string]string{
		"gitCommandWithCreds": "the redacting wrapper itself",
		"gitRun":              "the redacting wrapper itself",
		"sshPullPrecheck":     "URL shape only; never shown (agent-os-0j5h)",
		"pushPreconditions":   "push URL shape and branch config; refusals masked by redactWriteResult",
		"gitConfigValues":     "branch.<b>.remote/merge; shown only through pushPreconditions' refusals",
		"getStatusCLI":        "branch, bare, HEAD, porcelain, upstream, counts; Branch/TrackingBranch and count errors masked at the result",
		"dirtyPullMessage":    "submodule drift paths; the message is masked before it is returned",
		"pullCLI":             "bare, porcelain, HEADs, changed names; ChangedFiles/DiffError masked by redactPullResult",
		"getLogCLI":           "commit count, log records; logCommit masks author/email/subject",
		"GetLogForFile":       "log records; logCommit masks author/email/subject",
		"getDiffCLI":          "oid, parents, log record; logCommit and redactErr on historyDiff's error",
		"historyTop":          "bare and top level; never shown",
		"historyDiff":         "changed names for the env rule and pathspecs, the patch for the secret masker; files/hidden masked by shown(), the patch after the masker",
		"catBlob":             "compose content and link targets for the env rule; never shown",
		"blobSize":            "a blob's size; never shown",
		"lsIndex":             "index listing for the env rule; errors masked by the public callers",
		"lsTree":              "tree listing for the env rule; errors masked by the public callers",
		"repoTopLevel":        "top level and bare; never shown",
		"headBranch":          "current branch; masked in GetChanges and by redactWriteResult",
		"listChanges":         "porcelain records; Path/OrigPath masked in GetChanges",
		"getChangeDiff":       "the working-tree patch, for the secret masker; token masked after it",
		"envLinkTargets":      "env file names; never shown",
		"commitPaths":         "the new commit's hash, served raw (D2)",
		"push":                "push porcelain flag and summary; masked by redactWriteResult",
	}
	entries, err := packageSources.ReadDir(".")
	require.NoError(t, err)
	fset := token.NewFileSet()
	got := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := packageSources.ReadFile(name)
		require.NoError(t, err)
		file, err := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, err)
		for _, d := range file.Decls {
			ast.Inspect(d, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok &&
					(sel.Sel.Name == "gitCommandUnredacted" || sel.Sel.Name == "gitRunUnredacted") {
					got[declName(d)] = true
				}
				return true
			})
		}
	}
	var gotNames, wantNames []string
	for n := range got {
		gotNames = append(gotNames, n)
	}
	for n := range want {
		wantNames = append(wantNames, n)
	}
	sort.Strings(gotNames)
	sort.Strings(wantNames)
	assert.Equal(t, wantNames, gotNames,
		"gitCommandUnredacted/gitRunUnredacted return the token unmasked: add the caller here with the boundary that masks what it shows, or use the redacting wrapper")
}

// The secret masker reads the patch raw and the token is masked after it: a
// secret value holding the token, masked first, became "QQ***ZZ", no longer
// matched the secret, and its other characters were shown.
func TestSecretMaskerReadsRawPatch9wwa(t *testing.T) {
	const token, secret = "tok9wwa", "QQtok9wwaZZ"
	t.Run("history diff", func(t *testing.T) {
		f := newHistFix9wwa(t)
		f.write(t, "x.txt", "x\n")
		f.commit(t, "seed")
		f.register(t, "", "s", "")
		f.write(t, "x.txt", "v="+secret+"\nRAW_PUBLIC\n")
		h := f.commit(t, "edit")
		setToken9wwa(t, f.svc, f.db, f.top, token)

		res, err := f.svc.GetDiffMasked(f.top, h, []string{secret})
		require.NoError(t, err)
		assert.Contains(t, res.Diff, "RAW_PUBLIC")
		assert.NotContains(t, res.Diff, "QQ")
		assert.NotContains(t, res.Diff, "ZZ")
		assert.NotContains(t, res.Diff, token)
	})
	t.Run("change diff", func(t *testing.T) {
		f := newCommitFix(t, false)
		setToken9wwa(t, f.svc, f.db, f.dir, token)
		writeFile(t, filepath.Join(f.top, "x"), "v="+secret+"\nRAW_PUBLIC\n")

		res, err := f.svc.GetChangeDiffMasked(f.dir, "x", []string{secret})
		require.NoError(t, err)
		assert.Contains(t, res.Diff, "RAW_PUBLIC")
		assert.NotContains(t, res.Diff, "QQ")
		assert.NotContains(t, res.Diff, "ZZ")
		assert.NotContains(t, res.Diff, token)
	})
}
