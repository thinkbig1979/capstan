package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/pathutil"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-ncdg: the pull's failure reasons that name a stack folder or file
// are shown through redactPullResult. A path operand must be masked before it
// is quoted: %q escapes a token holding a quote or backslash, and the
// boundary can no longer find it.

// shownReasonNcdg is reason as the pull shows it, through redactPullResult.
func shownReasonNcdg(t *testing.T, reason, token string) string {
	t.Helper()
	ar := truth.Partial("pulled", truth.KV("failedRedeploys", []RedeployFailure{{StackID: "S", Reason: reason}}))
	shown, _ := redactPullResult(ar, &models.PullResult{}, token)
	fs := shown.Details["failedRedeploys"].([]RedeployFailure)
	require.Len(t, fs, 1)
	return fs[0].Reason
}

// linkedTopNcdg is a repository top passed through a symlink: a folder under
// it is contained, yet its path relative to the unresolved top leaves it, so
// repoRel answers !ok.
func linkedTopNcdg(t *testing.T) (linkTop, real string) {
	t.Helper()
	base := t.TempDir()
	real = filepath.Join(base, "real")
	require.NoError(t, os.Mkdir(real, 0o700))
	linkTop = filepath.Join(base, "link")
	require.NoError(t, os.Symlink(real, linkTop))
	return linkTop, real
}

// relFilesChanged: a stack file name that cannot be placed.
func TestRelFilesChangedNcdg_TokenInFileNameNotShown(t *testing.T) {
	for _, tok := range []string{tokR5b4, tokR5b4Quoted} {
		st := models.Stack{ID: "S", Directory: "/stacks/s", ComposeFile: "../../" + tok}
		_, err := relFilesChanged("s", st, nil, tok)
		require.Error(t, err)
		reason := shownReasonNcdg(t, err.Error(), tok)
		require.Contains(t, reason, "cannot be placed")
		assertNoTokenR5b4(t, reason, tok)
	}
}

// relFilesChanged: the token in the stack FOLDER, the message's other path
// operand (agent-os-u27g). Only the census guarded it: m2, the folder quoted
// without redactToken, failed no behaviour test.
func TestRelFilesChangedU27g_TokenInDirectoryNotShown(t *testing.T) {
	for _, tok := range []string{tokR5b4, tokR5b4Quoted} {
		st := models.Stack{ID: "S", Directory: "/stacks/" + tok, ComposeFile: "../../x.yaml"}
		_, err := relFilesChanged("s", st, nil, tok)
		require.Error(t, err)
		reason := shownReasonNcdg(t, err.Error(), tok)
		require.Contains(t, reason, "cannot be placed")
		assertNoTokenR5b4(t, reason, tok)
	}
}

// stackFilesChangedSince: the stack folder is outside the repository.
func TestStackFilesChangedSinceNcdg_TokenInFolderNotShown(t *testing.T) {
	linkTop, real := linkedTopNcdg(t)
	for _, tok := range []string{tokR5b4, tokR5b4Quoted} {
		st := models.Stack{ID: "S", Directory: filepath.Join(real, tok), ComposeFile: "compose.yaml"}
		_, err := stackFilesChangedSince(linkTop, st, nil, linkSnapshot{}, tok)
		require.Error(t, err)
		reason := shownReasonNcdg(t, err.Error(), tok)
		require.Contains(t, reason, "is outside the repository")
		assertNoTokenR5b4(t, reason, tok)
	}
}

// placeSiblings: the stack folder cannot be placed.
func TestPlaceSiblingsNcdg_TokenInFolderNotShown(t *testing.T) {
	linkTop, real := linkedTopNcdg(t)
	for _, tok := range []string{tokR5b4, tokR5b4Quoted} {
		st := models.Stack{ID: "S", Directory: filepath.Join(real, tok), ComposeFile: "compose.yaml"}
		p := placeSiblings(linkTop, filepath.Join(real, "other"), []models.Stack{st}, tok)
		require.Len(t, p.stacks, 1)
		require.Error(t, p.stacks[0].err)
		reason := shownReasonNcdg(t, p.stacks[0].err.Error(), tok)
		require.Contains(t, reason, "cannot be placed")
		assertNoTokenR5b4(t, reason, tok)
	}
}

// placeSiblings: whether the folder is inside the repository cannot be told.
func TestPlaceSiblingsNcdg_ContainmentErrorTokenNotShown(t *testing.T) {
	top, file := notDirR5b4(t)
	for _, tok := range []string{tokR5b4, tokR5b4Quoted} {
		st := models.Stack{ID: "S", Directory: filepath.Join(file, tok), ComposeFile: "compose.yaml"}
		p := placeSiblings(top, filepath.Join(top, "other"), []models.Stack{st}, tok)
		require.Len(t, p.stacks, 1)
		require.Error(t, p.stacks[0].err)
		reason := shownReasonNcdg(t, p.stacks[0].err.Error(), tok)
		require.Contains(t, reason, "could not tell whether")
		assertNoTokenR5b4(t, reason, tok)
	}
}

// The commit, push and pull lock refusal names the blocking stack's folder
// raw (%s keeps a token whole), and every caller shows it through
// redactWriteResult or redactPullResult. A pin: this passes on the base.
func TestRepoLockFailureNcdg_TokenInFolderMaskedAtBoundary(t *testing.T) {
	top, file := notDirR5b4(t)
	for _, tok := range []string{tokR5b4, tokR5b4Quoted} {
		st := models.Stack{ID: "S", Directory: filepath.Join(file, tok), ProjectName: "s"}
		_, err := pathutil.IsContained(top, st.Directory)
		require.Error(t, err)
		ar := repoLockFailure("could not lock", "commit", &RepoStackPathError{Stack: st, Err: err})
		shown, _ := redactWriteResult(ar, nil, tok)
		require.Contains(t, shown.Reason, "could not resolve the folder of stack")
		assertNoTokenR5b4(t, shown.Reason, tok)
		assertNoTokenR5b4(t, shown.Err.Error(), tok)
		assertNoTokenR5b4(t, shown.Details["blockingDirectory"].(string), tok)
	}
}
