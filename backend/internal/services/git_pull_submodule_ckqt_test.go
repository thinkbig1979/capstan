package services

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-ckqt: after a pull moves a submodule's commit, Capstan's pull
// (--no-recurse-submodules, D177) leaves the submodule work tree behind, so the
// folder reads ` M sub` and every later pull is refused. The refusal stays
// (D180 option B); what changes is that it names the submodule and the fix
// instead of calling the folder "uncommitted changes".

func refusal(t *testing.T, err error) *models.AppError {
	t.Helper()
	var ae *models.AppError
	require.ErrorAs(t, err, &ae)
	return ae
}

func TestPullCkqt_SubmoduleDriftRefusalNamesTheSubmoduleAndTheFix(t *testing.T) {
	f, _ := workingSubmodulePullFix(t)
	_, err := f.svc.pullCLI(f.dir)
	require.NoError(t, err, "control: the first pull moves the gitlink and succeeds")
	require.Equal(t, "M sub", runGit(t, f.top, "status", "--porcelain"), "control: the folder now shows submodule drift")

	_, err = f.svc.pullCLI(f.dir)

	ae := refusal(t, err)
	assert.Equal(t, models.ErrGitDirty, ae.Code)
	assert.Equal(t, 400, ae.Status)
	assert.Contains(t, ae.Message, `"sub"`, "the message must name the submodule path")
	assert.Contains(t, ae.Message, "git submodule update", "the message must name the fix")
	assert.NotContains(t, ae.Message, "uncommitted", "submodule drift is not uncommitted work")
}

func TestPullCkqt_RealUncommittedChangeKeepsTheGenericMessage(t *testing.T) {
	f := newCommitFix(t, false)
	writeFile(t, filepath.Join(f.top, "x"), "edited\n")

	_, err := f.svc.pullCLI(f.dir)

	ae := refusal(t, err)
	assert.Equal(t, models.ErrGitDirty, ae.Code)
	assert.Equal(t, "Working directory has uncommitted changes", ae.Message)
}

func TestPullCkqt_DriftPlusRealChangeKeepsTheGenericMessage(t *testing.T) {
	f, _ := workingSubmodulePullFix(t)
	_, err := f.svc.pullCLI(f.dir)
	require.NoError(t, err)
	writeFile(t, filepath.Join(f.top, "x"), "edited\n")

	_, err = f.svc.pullCLI(f.dir)

	ae := refusal(t, err)
	assert.Equal(t, models.ErrGitDirty, ae.Code)
	assert.Equal(t, "Working directory has uncommitted changes", ae.Message)
}

func TestPullCkqt_UncommittedWorkInsideASubmoduleKeepsTheGenericMessage(t *testing.T) {
	f, _ := workingSubmodulePullFix(t)
	writeFile(t, filepath.Join(f.top, "sub", "scratch.txt"), "untracked inside the submodule\n")

	_, err := f.svc.pullCLI(f.dir)

	ae := refusal(t, err)
	assert.Equal(t, models.ErrGitDirty, ae.Code)
	assert.Equal(t, "Working directory has uncommitted changes", ae.Message)
}

// spacedSubmoduleFix is a commitFix with a submodule at "dir with space/sub"
// whose HEAD has moved past the recorded commit: ` M "dir with space/sub"`.
func spacedSubmoduleFix(t *testing.T) *commitFix {
	t.Helper()
	t.Setenv("GIT_CONFIG_PARAMETERS", "'protocol.file.allow=always'")
	f := newCommitFix(t, false)
	f.svc.config.DataDir = f.dataDir
	sub := filepath.Join(t.TempDir(), "sub")
	runGit(t, filepath.Dir(sub), "init", "-q", "-b", "main", sub)
	runGit(t, sub, "commit", "-q", "--allow-empty", "-m", "s1")
	runGit(t, f.top, "submodule", "add", "-q", sub, "dir with space/sub")
	runGit(t, f.top, "commit", "-q", "-m", "add sub")
	runGit(t, f.top, "push", "-q", "origin", "main")
	runGit(t, filepath.Join(f.top, "dir with space", "sub"), "commit", "-q", "--allow-empty", "-m", "s2")
	return f
}

func TestPullCkqt_NamesAPathWithSpacesAsGitPrintsIt(t *testing.T) {
	f := spacedSubmoduleFix(t)
	require.Contains(t, runGit(t, f.top, "status", "--porcelain"), "dir with space/sub", "control: the drift is there")

	_, err := f.svc.pullCLI(f.dir)

	ae := refusal(t, err)
	assert.Equal(t, models.ErrGitDirty, ae.Code)
	assert.Equal(t, `Submodule "dir with space/sub" is not at the committed version: run "git submodule update" in the stack's repository`, ae.Message)
}

// A staged rename is a "2" record with an extra NUL-separated path; it must not
// shift the stream or be read as drift, and the message stays generic.
func TestPullCkqt_StagedRenamePlusDriftKeepsTheGenericMessage(t *testing.T) {
	f := spacedSubmoduleFix(t)
	runGit(t, f.top, "mv", "decoy.txt", "renamed.txt")
	require.Contains(t, runGit(t, f.top, "status", "--porcelain=v2"), "\n2 R", "control: git reports a rename record")

	_, err := f.svc.pullCLI(f.dir)

	ae := refusal(t, err)
	assert.Equal(t, models.ErrGitDirty, ae.Code)
	assert.Equal(t, genericDirtyPullMessage, ae.Message)
}

func TestSubmoduleDriftPathsCkqt(t *testing.T) {
	const h = "1111111111111111111111111111111111111111"
	drift := func(path string) string { return "1 .M SC.. 160000 160000 160000 " + h + " " + h + " " + path + "\x00" }
	cases := []struct {
		name   string
		out    string
		want   []string
		wantOK bool
	}{
		{"one", drift("sub"), []string{"sub"}, true},
		{"two", drift("a") + drift("b c/d"), []string{"a", "b c/d"}, true},
		{"empty", "", nil, false},
		{"file change", "1 .M N... 100644 100644 100644 " + h + " " + h + " x\x00", nil, false},
		{"staged then moved again", "1 MM SC.. 160000 160000 160000 " + h + " " + h + " sub\x00", nil, false},
		{"new submodule then moved", "1 AM SC.. 160000 160000 160000 " + h + " " + h + " sub\x00", nil, false},
		{"unmerged with SC.. field", "u UU SC.. 160000 160000 160000 160000 " + h + " " + h + " " + h + " sub\x00", nil, false},
		{"inner change", "1 .M S.M. 160000 160000 160000 " + h + " " + h + " sub\x00", nil, false},
		{"inner untracked", "1 .M SC.U 160000 160000 160000 " + h + " " + h + " sub\x00", nil, false},
		{"untracked", drift("sub") + "? new.txt\x00", nil, false},
		{"rename first", "2 R. N... 100644 100644 100644 " + h + " " + h + " R100 new\x00old\x00" + drift("sub"), nil, false},
		{"rename last", drift("sub") + "2 R. N... 100644 100644 100644 " + h + " " + h + " R100 new\x00old\x00", nil, false},
		{"unmerged", "u UU N... 100644 100644 100644 100644 " + h + " " + h + " " + h + " f\x00", nil, false},
		{"ignored", "! ignored.log\x00", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := submoduleDriftPaths(c.out)
			assert.Equal(t, c.wantOK, ok)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestSubmoduleDriftMessageCkqt(t *testing.T) {
	const fix = ` not at the committed version: run "git submodule update" in the stack's repository`
	assert.Equal(t, `Submodule "a" is`+fix, submoduleDriftMessage([]string{"a"}))
	assert.Equal(t, `Submodules "a", "b", "c" are`+fix, submoduleDriftMessage([]string{"a", "b", "c"}))
	assert.Equal(t, `Submodules "a", "b", "c", and 2 more are`+fix, submoduleDriftMessage([]string{"a", "b", "c", "d", "e"}))
}

// `1 MM SC..`: the gitlink change is STAGED and the submodule then moved again.
// That is uncommitted work, and `git submodule update` would not clear it.
func TestPullCkqt_StagedGitlinkThenMovedKeepsTheGenericMessage(t *testing.T) {
	f := spacedSubmoduleFix(t)
	runGit(t, f.top, "add", "dir with space/sub")
	runGit(t, filepath.Join(f.top, "dir with space", "sub"), "commit", "-q", "--allow-empty", "-m", "s3")
	require.Contains(t, runGit(t, f.top, "status", "--porcelain=v2"), "1 MM SC..", "control: git reports a staged-then-moved gitlink")

	_, err := f.svc.pullCLI(f.dir)

	ae := refusal(t, err)
	assert.Equal(t, models.ErrGitDirty, ae.Code)
	assert.Equal(t, genericDirtyPullMessage, ae.Message)
}
