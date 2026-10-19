package services

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// Tests for agent-os-2omy: the history diff never carries an env file's
// contents and runs no program the repository's config names. Each fixture
// is a real repository shaped for one way the exclusion could miss.

type histFix struct {
	top string
	db  *database.DB
	svc *GitService
}

func newHistFix(t *testing.T) *histFix {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	f := &histFix{top: filepath.Join(t.TempDir(), "work"), db: newTestDB(t)}
	runGit(t, filepath.Dir(f.top), "init", "-q", "-b", "main", f.top)
	f.svc = NewGitService(&config.Config{}, f.db)
	return f
}

func (f *histFix) write(t *testing.T, rel, body string) {
	t.Helper()
	writeFile(t, filepath.Join(f.top, rel), body)
}

// commit stages everything and commits, returning the new HEAD.
func (f *histFix) commit(t *testing.T, msg string) string {
	t.Helper()
	runGit(t, f.top, "add", "-A")
	runGit(t, f.top, "commit", "-q", "--allow-empty", "-m", msg)
	return runGit(t, f.top, "rev-parse", "HEAD")
}

func (f *histFix) register(t *testing.T, dirRel, id, envFile string) string {
	t.Helper()
	dir := filepath.Join(f.top, dirRel)
	require.NoError(t, f.db.UpsertDirectory(models.Directory{Path: dir, Name: filepath.Base(dir)}))
	require.NoError(t, f.db.UpsertStack(models.Stack{ID: id, Directory: dir, ComposeFile: "compose.yaml", EnvFile: envFile, ProjectName: id}))
	return dir
}

func (f *histFix) diff(t *testing.T, dirRel, hash string) *models.DiffResult {
	t.Helper()
	res, err := f.svc.GetDiff(filepath.Join(f.top, dirRel), hash)
	require.NoError(t, err)
	return res
}

// requireHidden asserts the secret is nowhere in the patch, the visible
// marker IS (so a result that hides everything can't pass), and the hidden
// list is exactly hidden.
func requireHidden(t *testing.T, res *models.DiffResult, secret, visible string, hidden ...string) {
	t.Helper()
	assert.NotContains(t, res.Diff, secret, "an env file's contents reached the history diff")
	if visible != "" {
		assert.Contains(t, res.Diff, visible, "a file that is not an env file must still show")
	}
	assert.Equal(t, hidden, res.HiddenFiles)
	for _, h := range hidden {
		assert.Contains(t, res.Files, h, "a hidden file stays in Files")
	}
}

// B1: a root commit. diff-tree without --root lists nothing for it.
func TestHistoryDiff_RootCommit(t *testing.T) {
	f := newHistFix(t)
	f.write(t, ".env", "SECRET_ROOT=1\n")
	f.write(t, "x.txt", "PUBLIC_ROOT\n")
	h := f.commit(t, "root")

	res := f.diff(t, "", h)
	requireHidden(t, res, "SECRET_ROOT", "PUBLIC_ROOT", ".env")
	assert.Equal(t, []string{".env", "x.txt"}, res.Files)
}

// B2: a merge whose resolution edits .env. Without -m the name pass lists
// nothing and show prints a combined diff of .env. Asserted on the patch
// text: the UI's parser can't see `diff --cc`.
func TestHistoryDiff_MergeCommit(t *testing.T) {
	f := newHistFix(t)
	f.write(t, ".env", "A=0\n")
	f.write(t, "other.txt", "o\n")
	f.commit(t, "base")
	runGit(t, f.top, "checkout", "-q", "-b", "side")
	f.write(t, ".env", "A=side\n")
	f.commit(t, "side")
	runGit(t, f.top, "checkout", "-q", "main")
	f.write(t, ".env", "A=main\n")
	f.write(t, "other.txt", "MERGE_PUBLIC\n")
	f.commit(t, "main")
	// The merge conflicts on .env by design; the resolution below commits it.
	out, err := gitTry(t, f.top, "merge", "-q", "--no-commit", "side")
	require.Error(t, err, "fixture: the merge must conflict on .env: %s", out)
	f.write(t, ".env", "EVIL_MERGE_SECRET=1\n")
	f.write(t, "other.txt", "MERGE_PUBLIC_RESOLVED\n")
	h := f.commit(t, "merge")
	require.Len(t, strings.Fields(runGit(t, f.top, "rev-parse", h+"^@")), 2, "fixture: HEAD must be a merge")

	res := f.diff(t, "", h)
	requireHidden(t, res, "EVIL_MERGE_SECRET", "MERGE_PUBLIC_RESOLVED", ".env")
}

// B3: a commit that touches only .env. Nothing is left to show, so the show
// child doesn't run at all: faulting it changes nothing.
func TestHistoryDiff_OnlyEnvRunsNoShow(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	f.write(t, ".env", "ONLY_SECRET=1\n")
	h := f.commit(t, "env only")
	faultingGitOnPath(t, "*\" show --format=\"*")

	res := f.diff(t, "", h)
	assert.Equal(t, "", res.Diff)
	assert.Equal(t, []string{".env"}, res.HiddenFiles)
	assert.Equal(t, []string{".env"}, res.Files)
}

// B4: Capstan forwards its environment to git, and GIT_LITERAL_PATHSPECS=1
// would turn the exclude magic into a literal path that matches nothing.
func TestHistoryDiff_InheritedLiteralPathspecs(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "x.txt", "x\n")
	f.write(t, ".env", "A=0\n")
	f.commit(t, "seed")
	f.write(t, "x.txt", "LITERAL_PUBLIC\n")
	f.write(t, ".env", "LITERAL_SECRET=1\n")
	h := f.commit(t, "both")
	t.Setenv("GIT_LITERAL_PATHSPECS", "1")

	requireHidden(t, f.diff(t, "", h), "LITERAL_SECRET", "LITERAL_PUBLIC", ".env")
}

// B5 and B8: one repository, two stack folders. The request names stackA;
// the secrets live in stackB, one of them named only by stackB's compose
// env_file:. Pathspecs are relative to the working directory unless they
// carry top magic, and the env rule must come from every stack in the
// repository, not from the folder the request named.
func TestHistoryDiff_SharedRepo(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "stackA/compose.yaml", "services: {}\n")
	f.write(t, "stackA/a.txt", "a\n")
	f.write(t, "stackB/compose.yaml", "services:\n  app:\n    image: x\n    env_file: secrets.conf\n")
	f.write(t, "stackB/.env", "B=0\n")
	f.write(t, "stackB/secrets.conf", "DB=0\n")
	f.commit(t, "seed")
	f.register(t, "stackA", "s-a", ".env")
	f.register(t, "stackB", "s-b", ".env")
	f.write(t, "stackA/a.txt", "SHARED_PUBLIC\n")
	f.write(t, "stackB/.env", "B_SECRET=1\n")
	f.write(t, "stackB/secrets.conf", "DB_PASS=hunter2\n")
	h := f.commit(t, "edit")

	res := f.diff(t, "stackA", h)
	requireHidden(t, res, "B_SECRET", "SHARED_PUBLIC", "stackB/.env", "stackB/secrets.conf")
	assert.NotContains(t, res.Diff, "hunter2")
}

// B6: names git would C-quote without -z, which then never look env-named.
func TestHistoryDiff_QuotedNames(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	f.write(t, "é.env", "ACCENT_SECRET=1\n")
	f.write(t, `a b".env`, "QUOTE_SECRET=1\n")
	f.write(t, "x.txt", "QUOTED_PUBLIC\n")
	h := f.commit(t, "odd names")

	res := f.diff(t, "", h)
	requireHidden(t, res, "ACCENT_SECRET", "QUOTED_PUBLIC", `a b".env`, "é.env")
	assert.NotContains(t, res.Diff, "QUOTE_SECRET")
}

// B7: with diff.submodule=diff, show prints a submodule's files under paths
// the name pass never saw (it lists only the gitlink).
func TestHistoryDiff_SubmoduleShort(t *testing.T) {
	f := newHistFix(t)
	sub := filepath.Join(filepath.Dir(f.top), "subsrc")
	runGit(t, filepath.Dir(f.top), "init", "-q", "-b", "main", sub)
	writeFile(t, filepath.Join(sub, ".env"), "SUBSECRET1=1\n")
	runGit(t, sub, "add", "-A")
	runGit(t, sub, "commit", "-q", "-m", "sub seed")
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	runGit(t, f.top, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub, "mod")
	f.commit(t, "add mod")
	mod := filepath.Join(f.top, "mod")
	writeFile(t, filepath.Join(mod, ".env"), "SUBSECRET2=2\n")
	runGit(t, mod, "commit", "-q", "-am", "sub bump")
	h := f.commit(t, "bump mod")
	runGit(t, f.top, "config", "diff.submodule", "diff")

	res := f.diff(t, "", h)
	assert.NotContains(t, res.Diff, "SUBSECRET")
	assert.Contains(t, res.Diff, "Subproject commit", "the gitlink's own line still shows")
	assert.Equal(t, []string{"mod"}, res.Files)
}

// S6: a symlink as it was. The commit edits secrets.txt while .env pointed
// at it; the link is gone today.
func TestHistoryDiff_HistoricSymlinkTarget(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "secrets.txt", "S=0\n")
	f.write(t, "x.txt", "x\n")
	require.NoError(t, os.Symlink("secrets.txt", filepath.Join(f.top, ".env")))
	f.commit(t, "seed")
	f.write(t, "secrets.txt", "LINKED_SECRET=1\n")
	f.write(t, "x.txt", "LINK_PUBLIC\n")
	h := f.commit(t, "edit")
	require.NoError(t, os.Remove(filepath.Join(f.top, ".env")))
	f.commit(t, "drop link")

	requireHidden(t, f.diff(t, "", h), "LINKED_SECRET", "LINK_PUBLIC", "secrets.txt")
}

// NEW-4: today's link counts too. secrets.txt was edited before .env -> it
// existed in any tree.
func TestHistoryDiff_TodaysSymlinkTarget(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "secrets.txt", "S=0\n")
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	f.write(t, "secrets.txt", "TODAY_LINKED_SECRET=1\n")
	f.write(t, "x.txt", "TODAY_PUBLIC\n")
	h := f.commit(t, "edit")
	require.NoError(t, os.Symlink("secrets.txt", filepath.Join(f.top, ".env")))
	f.commit(t, "add link")

	requireHidden(t, f.diff(t, "", h), "TODAY_LINKED_SECRET", "TODAY_PUBLIC", "secrets.txt")
}

// S6 and NEW-1: a compose env_file: entry as it was, at the commit and at
// its parent. Today's compose names nothing.
func TestHistoryDiff_HistoricComposeEnvFile(t *testing.T) {
	f := newHistFix(t)
	named := "services:\n  app:\n    image: x\n    env_file: secrets.conf\n"
	f.write(t, "compose.yaml", named)
	f.write(t, "secrets.conf", "DB=0\n")
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	f.register(t, "", "s", "")

	f.write(t, "secrets.conf", "AT_COMMIT_SECRET=1\n")
	f.write(t, "x.txt", "AT_COMMIT_PUBLIC\n")
	atCommit := f.commit(t, "edit while named")

	// The commit that stops naming the file and deletes it: only the parent's
	// compose names it.
	f.write(t, "compose.yaml", "services:\n  app:\n    image: x\n")
	require.NoError(t, os.Remove(filepath.Join(f.top, "secrets.conf")))
	f.write(t, "x.txt", "AT_PARENT_PUBLIC\n")
	atParent := f.commit(t, "drop env_file")

	requireHidden(t, f.diff(t, "", atCommit), "AT_COMMIT_SECRET", "AT_COMMIT_PUBLIC", "secrets.conf")
	requireHidden(t, f.diff(t, "", atParent), "AT_COMMIT_SECRET", "AT_PARENT_PUBLIC", "secrets.conf")
}

// NEW-6: a past compose whose env_file can't be resolved hides everything on
// that commit but the stacks' compose files.
func TestHistoryDiff_UnreadableComposeHidesAllButCompose(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "compose.yaml", "services:\n  app:\n    image: x\n    env_file: ${ENV_FILE}\n")
	f.write(t, "conf.ini", "C=0\n")
	f.commit(t, "seed")
	f.register(t, "", "s", "")
	f.write(t, "compose.yaml", "services:\n  app:\n    image: COMPOSE_PUBLIC\n    env_file: ${ENV_FILE}\n")
	f.write(t, "conf.ini", "MAYBE_SECRET=1\n")
	h := f.commit(t, "edit")

	requireHidden(t, f.diff(t, "", h), "MAYBE_SECRET", "COMPOSE_PUBLIC", "conf.ini")
}

// S6: a file inside a folder named env (or .env) is an env file.
func TestHistoryDiff_EnvFolderComponent(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	f.write(t, "env/prod", "DIR_SECRET=1\n")
	f.write(t, "x.txt", "DIR_PUBLIC\n")
	h := f.commit(t, "env dir")

	requireHidden(t, f.diff(t, "", h), "DIR_SECRET", "DIR_PUBLIC", "env/prod")
}

// D143: the commit that renames .env away hides both sides.
func TestHistoryDiff_RenameHidesBothSides(t *testing.T) {
	f := newHistFix(t)
	f.write(t, ".env", "RENAMED_SECRET=1\nMORE=2\nEVEN_MORE=3\n")
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	runGit(t, f.top, "mv", ".env", "notes.txt")
	f.write(t, "notes.txt", "RENAMED_SECRET=1\nMORE=2\nEVEN_MORE=3\nNEW=4\n")
	f.write(t, "x.txt", "RENAME_PUBLIC\n")
	h := f.commit(t, "rename")

	requireHidden(t, f.diff(t, "", h), "RENAMED_SECRET", "RENAME_PUBLIC", ".env", "notes.txt")
}

// writeMarkerScript writes an executable that appends a line to marker.
func writeMarkerScript(t *testing.T, marker, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "prog.sh")
	//nolint:gosec // test-owned script under t.TempDir()
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\necho \"ran $*\" >> '"+marker+"'\n"+body), 0o755))
	return p
}

func markerLines(t *testing.T, marker string) string {
	t.Helper()
	b, err := os.ReadFile(marker) //nolint:gosec // test-owned path
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)
	return string(b)
}

// A textconv driver on a file that is NOT an env file (an env file is
// excluded either way, so it could not tell). Attributes come from the work
// tree, where show reads them.
func TestHistoryDiff_NoTextconv(t *testing.T) {
	f := newHistFix(t)
	marker := filepath.Join(t.TempDir(), "marks")
	prog := writeMarkerScript(t, marker, "cat \"$1\"\n")
	f.write(t, ".gitattributes", "*.txt diff=evil\n")
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	f.write(t, "x.txt", "TEXTCONV_PUBLIC\n")
	h := f.commit(t, "edit")
	runGit(t, f.top, "config", "diff.evil.textconv", prog)
	require.Empty(t, markerLines(t, marker), "fixture: nothing may have run yet")

	res := f.diff(t, "", h)
	assert.Contains(t, res.Diff, "TEXTCONV_PUBLIC")
	assert.Empty(t, markerLines(t, marker), "the repository's textconv program ran")
}

// log.showSignature with a gpg.program: both the log -1 and the show would
// run it on a signed commit.
func TestHistoryDiff_NoSignatureCheck(t *testing.T) {
	f := newHistFix(t)
	marker := filepath.Join(t.TempDir(), "marks")
	gpg := writeMarkerScript(t, marker, "cat >/dev/null\n"+
		"echo \"[GNUPG:] SIG_CREATED D 1 8 00 1 X\" >&2\n"+
		"printf -- '-----BEGIN PGP SIGNATURE-----\\n\\nZmFrZQ==\\n-----END PGP SIGNATURE-----\\n'\n")
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	f.write(t, "x.txt", "SIGNED_PUBLIC\n")
	runGit(t, f.top, "add", "-A")
	runGit(t, f.top, "-c", "gpg.program="+gpg, "commit", "-q", "-S", "-m", "signed")
	h := runGit(t, f.top, "rev-parse", "HEAD")
	runGit(t, f.top, "config", "gpg.program", gpg)
	runGit(t, f.top, "config", "log.showSignature", "true")
	require.NoError(t, os.Remove(marker))

	res := f.diff(t, "", h)
	assert.Contains(t, res.Diff, "SIGNED_PUBLIC")
	assert.Empty(t, markerLines(t, marker), "the repository's gpg.program ran")
}

// m2g8: a bare repository keeps serving its history, and hides env files.
func TestHistoryDiff_BareRepo(t *testing.T) {
	f := newHistFix(t)
	f.write(t, ".env", "BARE_SECRET=1\n")
	f.write(t, "x.txt", "BARE_PUBLIC\n")
	h := f.commit(t, "root")
	bare := filepath.Join(filepath.Dir(f.top), "bare.git")
	runGit(t, filepath.Dir(f.top), "clone", "-q", "--bare", f.top, bare)

	res, err := f.svc.GetDiff(bare, h)
	require.NoError(t, err)
	requireHidden(t, res, "BARE_SECRET", "BARE_PUBLIC", ".env")
}

// N2: an annotated tag's id resolves to its commit, and show prints the
// commit, not the tag.
func TestHistoryDiff_TagIDResolvesToCommit(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "x.txt", "TAGGED_PUBLIC\n")
	h := f.commit(t, "root")
	runGit(t, f.top, "tag", "-a", "v1", "-m", "TAG_MESSAGE")
	tagID := runGit(t, f.top, "rev-parse", "v1")
	require.NotEqual(t, h, tagID)

	res := f.diff(t, "", tagID)
	assert.Equal(t, h, res.Commit.Hash)
	assert.Contains(t, res.Diff, "TAGGED_PUBLIC")
	assert.NotContains(t, res.Diff, "TAG_MESSAGE")
}

// Fail closed: with no way to list the stacks, there is no diff.
func TestHistoryDiff_RuleUnbuildableRefuses(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "x.txt", "x\n")
	h := f.commit(t, "root")

	for name, svc := range map[string]*GitService{
		"nil database":    NewGitService(&config.Config{}, nil),
		"closed database": NewGitService(&config.Config{}, closedTestDB(t)),
	} {
		t.Run(name, func(t *testing.T) {
			res, err := svc.GetDiff(f.top, h)
			require.Error(t, err)
			assert.Nil(t, res)
			_, code := statusFor(err)
			assert.NotEqual(t, models.ErrGitNotRepo, code)
		})
	}
}

// Past the argv budget the show is not run and every file reads hidden.
func TestHistoryDiff_ExcludeBudget(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	long := strings.Repeat("n", 200)
	for i := 0; i < 1300; i++ {
		f.write(t, filepath.Join("env", long+"-"+strconv.Itoa(i)), "S\n")
	}
	f.write(t, "x.txt", "BUDGET_PUBLIC\n")
	h := f.commit(t, "many env files")

	res := f.diff(t, "", h)
	assert.Equal(t, "", res.Diff)
	assert.Equal(t, res.Files, res.HiddenFiles)
	assert.Contains(t, res.HiddenFiles, "x.txt")
}

func TestParseNameStatusZ(t *testing.T) {
	got, err := parseNameStatusZ("M\x00a\x00R100\x00.env\x00notes.txt\x00A\x00b c\x00")
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"a"}, {".env", "notes.txt"}, {"b c"}}, got)
	_, err = parseNameStatusZ("R100\x00only\x00")
	assert.Error(t, err)
}
