package services

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// Review round 1 of agent-os-2omy (D144-D146 and the r3 findings): shapes of
// "a file the compose names as secret" the first rule missed. The fixtures
// are the reviewer's probes, made assertions.

// An absolute env_file inside the repository is placed, not dropped.
func TestHistoryDiff_AbsoluteEnvFile(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "compose.yaml", "services:\n  app:\n    image: x\n    env_file: "+filepath.Join(f.top, "secrets.conf")+"\n")
	f.write(t, "secrets.conf", "DB=0\n")
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	f.register(t, "", "s", "")
	f.write(t, "secrets.conf", "ABS_SECRET=1\n")
	f.write(t, "x.txt", "ABS_PUBLIC\n")
	h := f.commit(t, "edit")

	requireHidden(t, f.diff(t, "", h), "ABS_SECRET", "ABS_PUBLIC", "secrets.conf")
}

// D144: a stack folder removed since still named its env file then.
func TestHistoryDiff_UnregisteredHistoricStack(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "old/compose.yaml", "services:\n  app:\n    image: x\n    env_file: secrets.conf\n")
	f.write(t, "old/secrets.conf", "DB=0\n")
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	f.write(t, "old/secrets.conf", "GONE_STACK_SECRET=1\n")
	f.write(t, "x.txt", "GONE_PUBLIC\n")
	h := f.commit(t, "edit")
	require.NoError(t, os.RemoveAll(filepath.Join(f.top, "old")))
	f.commit(t, "remove old stack")

	requireHidden(t, f.diff(t, "", h), "GONE_STACK_SECRET", "GONE_PUBLIC", "old/secrets.conf")
}

// D144: so did a compose file renamed since.
func TestHistoryDiff_RenamedComposeFile(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "docker-compose.yml", "services:\n  app:\n    image: x\n    env_file: secrets.conf\n")
	f.write(t, "secrets.conf", "DB=0\n")
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	f.write(t, "secrets.conf", "RENAMED_COMPOSE_SECRET=1\n")
	f.write(t, "x.txt", "RC_PUBLIC\n")
	h := f.commit(t, "edit")
	runGit(t, f.top, "mv", "docker-compose.yml", "compose.yaml")
	f.write(t, "compose.yaml", "services:\n  app:\n    image: x\n")
	require.NoError(t, os.Remove(filepath.Join(f.top, "secrets.conf")))
	f.commit(t, "rename compose, drop env_file")
	f.register(t, "", "s", "")

	requireHidden(t, f.diff(t, "", h), "RENAMED_COMPOSE_SECRET", "RC_PUBLIC", "secrets.conf")
}

// D145: env files named through include: and extends: file:.
func TestHistoryDiff_ComposeIncludeAndExtends(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "compose.yaml", "include:\n  - db.yaml\n  - path: [cache.yaml]\n    env_file: inc.conf\nservices:\n  app:\n    extends:\n      file: base.yaml\n      service: b\n")
	f.write(t, "db.yaml", "services:\n  db:\n    image: y\n    env_file: db.conf\n")
	f.write(t, "cache.yaml", "services:\n  c:\n    image: z\n")
	f.write(t, "base.yaml", "services:\n  b:\n    image: y\n    env_file: [{path: base.conf}]\n")
	for _, n := range []string{"db.conf", "inc.conf", "base.conf"} {
		f.write(t, n, "S=0\n")
	}
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	f.register(t, "", "s", "")
	f.write(t, "db.conf", "INCLUDE_SECRET=1\n")
	f.write(t, "inc.conf", "INCLUDE_ENV_SECRET=1\n")
	f.write(t, "base.conf", "EXTENDS_SECRET=1\n")
	f.write(t, "x.txt", "INC_PUBLIC\n")
	h := f.commit(t, "edit")

	res := f.diff(t, "", h)
	requireHidden(t, res, "INCLUDE_SECRET", "INC_PUBLIC", "base.conf", "db.conf", "inc.conf")
	assert.NotContains(t, res.Diff, "EXTENDS_SECRET")
	assert.NotContains(t, res.Diff, "INCLUDE_ENV_SECRET")
}

// D146: a top-level secrets: file: is hidden like an env file.
func TestHistoryDiff_ComposeSecretsFile(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "compose.yaml", "services:\n  app:\n    image: x\n    secrets: [db]\nsecrets:\n  db:\n    file: ./db_password.txt\n")
	f.write(t, "db_password.txt", "p\n")
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	f.register(t, "", "s", "")
	f.write(t, "db_password.txt", "SECRETS_FILE_SECRET\n")
	f.write(t, "x.txt", "SF_PUBLIC\n")
	h := f.commit(t, "edit")

	requireHidden(t, f.diff(t, "", h), "SECRETS_FILE_SECRET", "SF_PUBLIC", "db_password.txt")
}

// BL-4: a link to a folder inside the path, as the tree held it: .env ->
// conf/secret with conf -> private, both links gone today.
func TestHistoryDiff_HistoricFolderLinkInPath(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "private/secret", "S=0\n")
	f.write(t, "x.txt", "x\n")
	require.NoError(t, os.Symlink("private", filepath.Join(f.top, "conf")))
	require.NoError(t, os.Symlink("conf/secret", filepath.Join(f.top, ".env")))
	f.commit(t, "seed")
	f.write(t, "private/secret", "DIRLINK_SECRET=1\n")
	f.write(t, "x.txt", "DIRLINK_PUBLIC\n")
	h := f.commit(t, "edit")
	require.NoError(t, os.Remove(filepath.Join(f.top, ".env")))
	require.NoError(t, os.Remove(filepath.Join(f.top, "conf")))
	f.commit(t, "drop links")

	requireHidden(t, f.diff(t, "", h), "DIRLINK_SECRET", "DIRLINK_PUBLIC", "private/secret")
}

// BL-4: an env_file named through a folder link that is gone today.
func TestHistoryDiff_HistoricEnvFileViaFolderLink(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "compose.yaml", "services:\n  app:\n    image: x\n    env_file: conf/secrets\n")
	f.write(t, "private/secrets", "S=0\n")
	f.write(t, "x.txt", "x\n")
	require.NoError(t, os.Symlink("private", filepath.Join(f.top, "conf")))
	f.commit(t, "seed")
	f.register(t, "", "s", "")
	f.write(t, "private/secrets", "ENVFILE_DIRLINK_SECRET=1\n")
	f.write(t, "x.txt", "EFDL_PUBLIC\n")
	h := f.commit(t, "edit")
	require.NoError(t, os.Remove(filepath.Join(f.top, "conf")))
	f.write(t, "compose.yaml", "services:\n  app:\n    image: x\n")
	f.commit(t, "drop")

	requireHidden(t, f.diff(t, "", h), "ENVFILE_DIRLINK_SECRET", "EFDL_PUBLIC", "private/secrets")
}

// D144 in a bare repository: no stack lies inside it, so only compose files
// found in the tree can name its env files.
func TestHistoryDiff_BareRepoComposeEnvFile(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "compose.yaml", "services:\n  app:\n    image: x\n    env_file: secrets.conf\n")
	f.write(t, "secrets.conf", "DB=0\n")
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	f.write(t, "secrets.conf", "BARE_NAMED_SECRET=1\n")
	f.write(t, "x.txt", "BARE_NAMED_PUBLIC\n")
	h := f.commit(t, "edit")
	bare := filepath.Join(filepath.Dir(f.top), "bare.git")
	runGit(t, filepath.Dir(f.top), "clone", "-q", "--bare", f.top, bare)

	res, err := f.svc.GetDiff(bare, h)
	require.NoError(t, err)
	requireHidden(t, res, "BARE_NAMED_SECRET", "BARE_NAMED_PUBLIC", "secrets.conf")
}

// A stack folder that can't be placed (here a link loop) does not fail every
// history diff in every repository: it hides all but compose files in this
// one, which stays closed.
func TestHistoryDiff_UnplaceableStackHidesNotFails(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "compose.yaml", "services:\n  app:\n    image: COMPOSE_STILL_SHOWN\n")
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	f.write(t, "compose.yaml", "services:\n  app:\n    image: COMPOSE_STILL_SHOWN_2\n")
	f.write(t, "x.txt", "MAYBE_SECRET_X\n")
	h := f.commit(t, "edit")
	f.register(t, "", "s", "")
	loop := filepath.Join(t.TempDir(), "loop")
	require.NoError(t, os.Symlink("loop", loop))
	require.NoError(t, f.db.UpsertDirectory(models.Directory{Path: loop, Name: "loop"}))
	require.NoError(t, f.db.UpsertStack(models.Stack{ID: "s-loop", Directory: loop, ComposeFile: "compose.yaml", ProjectName: "loop"}))

	requireHidden(t, f.diff(t, "", h), "MAYBE_SECRET_X", "COMPOSE_STILL_SHOWN_2", "x.txt")
}

// gitAtLeast reports whether the git on PATH is at least major.minor.
func gitAtLeast(t *testing.T, major, minor int) (bool, string) {
	t.Helper()
	v := strings.TrimPrefix(runGit(t, t.TempDir(), "--version"), "git version ")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return false, v
	}
	ma, err1 := strconv.Atoi(parts[0])
	mi, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false, v
	}
	return ma > major || (ma == major && mi >= minor), v
}

// A partial clone missing an object fetches it on demand, which runs the
// repository's core.sshCommand. GIT_NO_LAZY_FETCH=1 stops that; git before
// 2.45 ignores the variable.
func TestHistoryDiff_NoLazyFetch(t *testing.T) {
	if ok, v := gitAtLeast(t, 2, 45); !ok {
		t.Skipf("git %s ignores GIT_NO_LAZY_FETCH (needs 2.45 or later)", v)
	}
	f := newHistFix(t)
	marker := filepath.Join(t.TempDir(), "marks")
	ssh := writeMarkerScript(t, marker, "exit 1\n")
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	f.write(t, "x.txt", "LAZY_PUBLIC\n")
	h := f.commit(t, "edit")
	blob := runGit(t, f.top, "rev-parse", h+":x.txt")
	require.NoError(t, os.Remove(filepath.Join(f.top, ".git", "objects", blob[:2], blob[2:])))
	for k, v := range map[string]string{
		"core.repositoryformatversion": "1", "extensions.partialClone": "origin",
		"remote.origin.url": "ssh://example.invalid/x", "remote.origin.promisor": "true",
		"core.sshCommand": ssh,
	} {
		runGit(t, f.top, "config", k, v)
	}

	// The blob is missing, so this may fail; the assertion is that nothing ran.
	_, err := f.svc.GetDiff(f.top, h)
	t.Logf("GetDiff with the blob missing: %v", err)
	assert.Empty(t, markerLines(t, marker), "a lazy fetch ran the repository's core.sshCommand")

	// The fixture can fire: plain git show fetches.
	out, err := gitTry(t, f.top, "show", h)
	t.Logf("control show: %v %s", err, out)
	assert.NotEmpty(t, markerLines(t, marker), "control: a plain show must try the lazy fetch")
}

// An absolute env_file only a past compose wrote: today's compose names
// nothing, so only placing the tree's entry hides it.
func TestHistoryDiff_AbsoluteEnvFileHistoric(t *testing.T) {
	f := newHistFix(t)
	f.write(t, "compose.yaml", "services:\n  app:\n    image: x\n    env_file: "+filepath.Join(f.top, "secrets.conf")+"\n")
	f.write(t, "secrets.conf", "DB=0\n")
	f.write(t, "x.txt", "x\n")
	f.commit(t, "seed")
	f.register(t, "", "s", "")
	f.write(t, "secrets.conf", "ABS_HIST_SECRET=1\n")
	f.write(t, "x.txt", "ABS_HIST_PUBLIC\n")
	h := f.commit(t, "edit")
	f.write(t, "compose.yaml", "services:\n  app:\n    image: x\n")
	f.commit(t, "drop env_file")

	requireHidden(t, f.diff(t, "", h), "ABS_HIST_SECRET", "ABS_HIST_PUBLIC", "secrets.conf")
}
