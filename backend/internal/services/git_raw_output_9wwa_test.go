package services

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
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

// Review round 1 of agent-os-9wwa. A raw value is redacted BEFORE it is
// formatted: %q escapes a token holding `"` or `\`, so masking the finished
// string no longer finds it. quoteEsc9wwa is the token as %q prints it.
const quotedTok9wwa = `pw"zq7`

func quoteEsc9wwa(tok string) string {
	q := strconv.Quote(tok)
	return q[1 : len(q)-1]
}

func requireNoToken9wwa(t *testing.T, s, tok string, what string) {
	t.Helper()
	assert.NotContains(t, s, tok, "%s carries the token", what)
	assert.NotContains(t, s, quoteEsc9wwa(tok), "%s carries the token as %%q escaped it", what)
}

func TestQuotedOperandRedacted9wwa(t *testing.T) {
	for _, tok := range []string{quotedTok9wwa, "zq7plain"} {
		t.Run("HEAD names a non-branch "+tok, func(t *testing.T) {
			f := newCommitFix(t, false)
			runGit(t, f.top, "tag", "x"+tok)
			runGit(t, f.top, "symbolic-ref", "HEAD", "refs/tags/x"+tok)
			setToken9wwa(t, f.svc, f.db, f.dir, tok)
			_, err := f.svc.GetChanges(f.dir)
			require.Error(t, err)
			require.Contains(t, err.Error(), "which is not a branch", "control: the headBranch error")
			requireNoToken9wwa(t, err.Error(), tok, "the error")
		})
		t.Run("pull refused for submodule drift "+tok, func(t *testing.T) {
			t.Setenv("GIT_CONFIG_PARAMETERS", "'protocol.file.allow=always'")
			f := newCommitFix(t, false)
			sub := filepath.Join(t.TempDir(), "sub")
			runGit(t, filepath.Dir(sub), "init", "-q", "-b", "main", sub)
			runGit(t, sub, "commit", "-q", "--allow-empty", "-m", "s1")
			runGit(t, f.top, "submodule", "add", "-q", sub, "s-"+tok)
			runGit(t, f.top, "commit", "-q", "-m", "add sub")
			runGit(t, f.top, "push", "-q", "origin", "main")
			runGit(t, filepath.Join(f.top, "s-"+tok), "commit", "-q", "--allow-empty", "-m", "s2")
			setToken9wwa(t, f.svc, f.db, f.dir, tok)
			_, err := f.svc.pullCLI(f.dir)
			var ae *models.AppError
			require.True(t, errors.As(err, &ae), "want an AppError, got %v", err)
			require.Equal(t, models.ErrGitDirty, ae.Code)
			require.Contains(t, ae.Message, "Submodule ", "control: the drift message")
			requireNoToken9wwa(t, ae.Message, tok, "the GIT_DIRTY message")
		})
	}
}

// #1: envRule logs historyEnvRule's error, which names raw index paths.
func TestEnvRuleLogRedacted9wwa(t *testing.T) {
	// Subtests are not named after the token: t.TempDir puts the name in
	// the path, which the log line carries.
	for name, tok := range map[string]string{"plain": "zq7tokLOG", "quoted": quotedTok9wwa} {
		t.Run(name, func(t *testing.T) {
			f := newCommitFix(t, false)
			p := filepath.Join(f.top, "d-"+tok, "compose.yaml")
			writeFile(t, p, "services: {}\n# unique 9wwa "+tok+"\n")
			runGit(t, f.top, "add", "d-"+tok+"/compose.yaml")
			oid := runGit(t, f.top, "rev-parse", ":d-"+tok+"/compose.yaml")
			require.NoError(t, os.Remove(filepath.Join(f.top, ".git", "objects", oid[:2], oid[2:])))
			writeFile(t, filepath.Join(f.top, "x"), "changed\n")
			setToken9wwa(t, f.svc, f.db, f.dir, tok)
			var buf bytes.Buffer
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
			t.Cleanup(func() { slog.SetDefault(old) })

			_, err := f.svc.GetChanges(f.dir)
			require.NoError(t, err)
			require.Contains(t, buf.String(), "env files named at HEAD or in the index are unknown", "control: the log line")
			requireNoToken9wwa(t, buf.String(), tok, "the log")
		})
	}
}

// #3: the token and the secrets are masked together. Either one masked
// first used to split the other: a secret holding the token, or a token
// holding a secret, or the two overlapping.
func TestJointMaskTokenAndSecrets9wwa(t *testing.T) {
	for name, tc := range map[string]struct{ tok, secret, line string }{
		"secret contains token": {"tok9wwa", "QQtok9wwaZZ", "v=QQtok9wwaZZ"},
		"token contains secret": {"AAAAzq7BBBBCCCC", "zq7B", "note AAAAzq7BBBBCCCC"},
		"overlapping":           {"AAAzq7", "zq7BBB", "n AAAzq7BBB"},
	} {
		check := func(t *testing.T, diff string) {
			t.Helper()
			assert.Contains(t, diff, "RAW_PUBLIC")
			assert.NotContains(t, diff, tc.tok)
			for _, part := range []string{tc.secret, "QQ", "ZZ", "AAA", "BBB"} {
				if strings.Contains(tc.line, part) {
					assert.NotContains(t, diff, part)
				}
			}
		}
		t.Run(name+"/change diff", func(t *testing.T) {
			f := newCommitFix(t, false)
			setToken9wwa(t, f.svc, f.db, f.dir, tc.tok)
			writeFile(t, filepath.Join(f.top, "x"), tc.line+"\nRAW_PUBLIC\n")
			res, err := f.svc.GetChangeDiffMasked(f.dir, "x", []string{tc.secret})
			require.NoError(t, err)
			check(t, res.Diff)
		})
		t.Run(name+"/history diff", func(t *testing.T) {
			f := newHistFix9wwa(t)
			f.write(t, "x.txt", "x\n")
			f.commit(t, "seed")
			f.register(t, "", "s", "")
			f.write(t, "x.txt", tc.line+"\nRAW_PUBLIC\n")
			h := f.commit(t, "edit")
			setToken9wwa(t, f.svc, f.db, f.top, tc.tok)
			res, err := f.svc.GetDiffMasked(f.top, h, []string{tc.secret})
			require.NoError(t, err)
			check(t, res.Diff)
		})
	}
}

// #5 keep-green arms, each pinned by a boundary mutant.
func TestBoundaryArms9wwa(t *testing.T) {
	const tok = "tok9wwa"
	t.Run("unmasked change diff masks the token", func(t *testing.T) {
		f := newCommitFix(t, false)
		setToken9wwa(t, f.svc, f.db, f.dir, tok)
		writeFile(t, filepath.Join(f.top, "x"), "has "+tok+"\n")
		res, err := f.svc.GetChangeDiff(f.dir, "x")
		require.NoError(t, err)
		assert.Contains(t, res.Diff, "has ***")
		assert.NotContains(t, res.Diff, tok)
	})
	t.Run("ahead/behind failure names the tracking branch masked", func(t *testing.T) {
		f := newCommitFix(t, false)
		runGit(t, f.top, "checkout", "-q", "-b", "br-"+tok)
		blob := runGit(t, f.top, "hash-object", "-w", "x")
		runGit(t, f.top, "update-ref", "refs/remotes/origin/br-"+tok, blob)
		setToken9wwa(t, f.svc, f.db, f.dir, tok)
		_, err := f.svc.GetStatus(f.dir)
		require.Error(t, err)
		require.Contains(t, err.Error(), "ahead/behind", "control: the rev-list failure")
		assert.NotContains(t, err.Error(), tok)
	})
	t.Run("commit kept after a failed push names the remote masked", func(t *testing.T) {
		// The remote name reaches truth.KV("remote", ...) unmasked inside
		// CommitVerified; only its display boundary masks it.
		f := newCommitFix(t, false)
		remote := "o-" + tok
		runGit(t, f.top, "remote", "add", remote, filepath.Join(t.TempDir(), "missing.git"))
		runGit(t, f.top, "update-ref", "refs/remotes/"+remote+"/main", "HEAD")
		runGit(t, f.top, "config", "branch.main.remote", remote)
		runGit(t, f.top, "config", "branch.main.merge", "refs/heads/main")
		writeFile(t, filepath.Join(f.top, "x"), "changed\n")
		setToken9wwa(t, f.svc, f.db, f.dir, tok)
		ar, w := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"x"}, Push: true})
		require.NotNil(t, w, "control: the commit was kept (%s %v)", ar.Reason, ar.Err)
		require.Equal(t, truth.OutcomePartial, ar.Outcome, "control: the push failed after the commit")
		assert.Equal(t, "o-***", ar.Details["remote"])
		assert.NotContains(t, fmt.Sprint(ar.Details)+ar.Reason, tok)
	})
}

// sinkArgs9wwa lists every argument of a log, error or message sink in the
// three files that read git raw, as "file:function:expression", except
// literals, values passed through a redaction helper, and errors (an error
// built in these files masks its operands where it is built, which this same
// census checks; one built elsewhere carries redacted git diagnostics).
func sinkArgs9wwa(t *testing.T) map[string]bool {
	t.Helper()
	sinks := map[string]bool{
		"slog.Debug": true, "slog.Info": true, "slog.Warn": true, "slog.Error": true,
		"fmt.Errorf": true, "fmt.Sprintf": true,
		"models.NewAppError": true, "models.NewAppErrorWithDetails": true,
	}
	safeCalls := map[string]bool{
		"redactToken": true, "redactErr": true, "redactAll": true, "boundedPathErrShown": true,
		"len": true,
	}
	fset := token.NewFileSet()
	got := map[string]bool{}
	for _, name := range []string{"git.go", "git_commit.go", "git_history_diff.go"} {
		src, err := packageSources.ReadFile(name)
		require.NoError(t, err)
		file, err := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, err)
		for _, d := range file.Decls {
			ast.Inspect(d, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || !sinks[pkg.Name+"."+sel.Sel.Name] {
					return true
				}
				for _, a := range call.Args {
					if safeArg9wwa(a, safeCalls) {
						continue
					}
					var b strings.Builder
					require.NoError(t, printer.Fprint(&b, fset, a))
					got[name+":"+declName(d)+":"+b.String()] = true
				}
				return true
			})
		}
	}
	return got
}

func safeArg9wwa(a ast.Expr, safeCalls map[string]bool) bool {
	switch a := a.(type) {
	case *ast.BasicLit:
		return true
	case *ast.Ident:
		return a.Name == "err" || strings.HasSuffix(a.Name, "Err") || a.Name == "nil"
	case *ast.SelectorExpr:
		// A package constant (models.ErrX, http.StatusX).
		x, ok := a.X.(*ast.Ident)
		return ok && (x.Name == "models" || x.Name == "http")
	case *ast.BinaryExpr:
		return safeArg9wwa(a.X, safeCalls) && safeArg9wwa(a.Y, safeCalls)
	case *ast.CallExpr:
		name := ""
		switch f := a.Fun.(type) {
		case *ast.Ident:
			name = f.Name
		case *ast.SelectorExpr:
			if x, ok := f.X.(*ast.Ident); ok {
				name = x.Name + "." + f.Sel.Name
			}
			if f.Sel.Name == "Error" && len(a.Args) == 0 {
				return safeArg9wwa(f.X, safeCalls)
			}
		}
		if safeCalls[name] {
			return true
		}
		if name == "fmt.Sprintf" || name == "fmt.Errorf" {
			return true // a sink itself, checked where it is called
		}
		if name == "boundedPath" && len(a.Args) == 1 {
			return safeArg9wwa(a.Args[0], safeCalls)
		}
	}
	return false
}

// TestSinkArgsCensus9wwa is the consumer side of TestRawGitCallers9wwa:
// every argument of a slog call, fmt.Errorf/Sprintf or AppError constructor
// in git.go, git_commit.go and git_history_diff.go is a literal, a constant,
// an error, a value passed through redactToken/redactErr/redactAll, or one of
// the listed values below, each with why it cannot hold repository text. A
// new argument fails here until it is masked or listed.
func TestSinkArgsCensus9wwa(t *testing.T) {
	const (
		num      = "a number or a format Capstan wrote"
		path     = "a server path: the configured stack folder or its repository root, not repository data"
		dbValue  = "a value from Capstan's database or its own constants"
		errValue = "an error: built in these files with its operands masked, or carrying redacted git diagnostics"
		label    = "an argv word, a ref or code Capstan chose, or an object id"
		hash     = "a hash: git's own output, served raw (agent-os-9wwa D2)"
		request  = "the client's own request value, which it read masked"
		masked   = "masked before it reaches this call"
	)
	want := map[string]string{
		"git.go:GetLogForFile:limit":                                   num,
		"git.go:GetLogForFile:logFormat":                               num,
		"git.go:getLogCLI:fetchCount":                                  num,
		"git.go:getLogCLI:logFormat":                                   num,
		"git.go:getLogCLI:skip":                                        num,
		"git.go:submoduleDriftMessage:len(paths) - maxNamedSubmodules": num,
		"git_commit.go:parsePorcelainV2:want":                          num,
		"git_history_diff.go:lsIndex:i":                                num,
		"git_history_diff.go:treeSource:maxComposeBytes":               num,
		"git.go:gitCommandUnredacted:dirPath":                          path,
		"git.go:gitFailure:dirPath":                                    path,
		"git.go:logPullRefusal:dirPath":                                path,
		"git.go:pullCLI:dirPath":                                       path,
		"git.go:placeSiblings:st.Directory":                            path,
		"git.go:placeSiblings:top":                                     path,
		"git.go:relFilesChanged:stack.Directory":                       path,
		"git.go:stackFilesChangedSince:stack.Directory":                path,
		"git.go:stackFilesChangedSince:top":                            path,
		"git_commit.go:CommitVerified:dirPath":                         path,
		"git_commit.go:Error:e.Stack.Directory":                        path,
		"git_commit.go:commitPaths:top":                                path,
		"git_commit.go:envRule:composePath":                            path,
		"git_commit.go:envRule:top":                                    path,
		"git_commit.go:push:top":                                       path,
		"git_commit.go:repoLockFailure:pathErr.Stack.Directory":        path,
		"git_history_diff.go:historyDiff:dirPath":                      path,
		"git.go:PullVerified:stack.ID":                                 dbValue,
		"git.go:lockStacks:st.ProjectName":                             dbValue,
		"git.go:relFilesChanged:name":                                  dbValue,
		"git_commit.go:Error:e.Stack.ProjectName":                      dbValue,
		"git_commit.go:envRule:u.stack.ProjectName":                    dbValue,
		"git_commit.go:repoLockFailure:pathErr.Stack.ProjectName":      dbValue,
		"git_commit.go:settingOrUnset:key":                             dbValue,
		"git.go:changed:st.err":                                        errValue,
		"git.go:getDiffCLI:verr":                                       errValue,
		"git_commit.go:Error:e.Err":                                    errValue,
		"git_commit.go:envRule:u.err":                                  errValue,
		"git_history_diff.go:boundedPathErrShown:pe.Err":               errValue,
		"git.go:changed:reason":                                        errValue,
		"git.go:gitCommandUnredacted:args[0]":                          label,
		"git.go:logPullRefusal:code":                                   label,
		"git_commit.go:gitRunUnredacted:gitSubcommand(args)":           label,
		"git_commit.go:refuseOperationInProgress:ref":                  label,
		"git_commit.go:repoLockFailure:action":                         label,
		"git_history_diff.go:boundedPathErrShown:pe.Op":                label,
		"git_history_diff.go:lsTree:t":                                 label,
		"git_history_diff.go:resolve:t.name":                           label,
		"git_history_diff.go:treeSource:t.name":                        label,
		"git.go:pullCLI:currentCommit":                                 hash,
		"git.go:pullCLI:previousCommit":                                hash,
		"git_commit.go:CommitVerified:hash":                            hash,
		"git_history_diff.go:historyDiff:oid":                          hash,
		"git_commit.go:getChangeDiff:path":                             request,
		"git_commit.go:selectCommitPaths:f":                            request,
		"git.go:pullCLI:s.dirtyPullMessage(dirPath, cred)":             masked + ": dirtyPullMessage masks each path before quoting it",
		"git.go:submoduleDriftMessage:p":                               masked + ": its one caller passes redactAll(paths)",
		"git_history_diff.go:boundedPath:p[:maxErrPathBytes]":          masked + ": every caller in these three files passes a masked or non-repository path (git_pull_linkdeps.go does not; a follow-up)",
		"git_history_diff.go:historyEnvRule:args":                      masked + ": unknown() masks each string and error in args first",
		`git_history_diff.go:historyEnvRule:"Hiding every file but the compose files in the " + view + ": " + msg`: masked + ": view and msg are literals at every call",
		"git_commit.go:gitRunUnredacted:gitDiagnostic(redactToken(stdout, cred.token), stderr)":                    masked + ": stderr is redacted on read",
	}
	got := sinkArgs9wwa(t)
	var gotNames, wantNames []string
	for k := range got {
		gotNames = append(gotNames, k)
	}
	for k := range want {
		wantNames = append(wantNames, k)
	}
	sort.Strings(gotNames)
	sort.Strings(wantNames)
	assert.Equal(t, wantNames, gotNames,
		"a sink in the raw-reading files takes a value that is neither masked nor listed: mask it with redactToken/redactErr BEFORE formatting, or list it with why it cannot hold repository text")
}
