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
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-r5b4: linkDeps quoted a path with boundedPath before anything
// masked the token in it. boundedPath escapes (%q) and cuts at
// maxErrPathBytes, so the finished message no longer held the token whole and
// the pull's display boundary (redactPullResult) could not find it. That
// boundary also left the redeploy and sibling failure reasons unmasked.

const tokR5b4 = "ghp_R5B4tokenSECRETvalue0123"

// tokR5b4Quoted is a token %q escapes: its quote and backslash.
const tokR5b4Quoted = `pa"ss\wordR5b4secret`

// assertNoTokenR5b4 checks that msg holds no part of tok: whole, escaped, or
// its first 8 bytes (what a cut leaves).
func assertNoTokenR5b4(t *testing.T, msg, tok string) {
	t.Helper()
	q := strconv.Quote(tok)
	assert.NotContains(t, msg, tok)
	assert.NotContains(t, msg, q[1:len(q)-1], "the token is shown escaped")
	assert.NotContains(t, msg, tok[:8], "the start of the token is shown")
}

// notDirR5b4 is a regular file under top: a path through it fails Lstat
// with ENOTDIR, which linkDeps reports with the path.
func notDirR5b4(t *testing.T) (top, file string) {
	t.Helper()
	top = t.TempDir()
	file = filepath.Join(top, "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	return top, file
}

func TestLinkDepsR5b4_TokenAcrossCutNotShown(t *testing.T) {
	top, file := notDirR5b4(t)
	// The token starts 10 bytes before boundedPath's cut.
	pad := strings.Repeat("a", maxErrPathBytes-10-len(file)-1)
	_, err := linkDeps(top, filepath.Join(file, pad+tokR5b4), tokR5b4)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not read")
	assertNoTokenR5b4(t, err.Error(), tokR5b4)
}

func TestLinkDepsR5b4_EscapedTokenNotShown(t *testing.T) {
	top, file := notDirR5b4(t)
	_, err := linkDeps(top, filepath.Join(file, "x"+tokR5b4Quoted), tokR5b4Quoted)
	require.Error(t, err)
	assertNoTokenR5b4(t, err.Error(), tokR5b4Quoted)
}

func TestLinkDepsR5b4_LinkLoopTokenNotShown(t *testing.T) {
	top := t.TempDir()
	dir := filepath.Join(top, tokR5b4Quoted)
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.NoError(t, os.Symlink("loop", filepath.Join(dir, "loop")))
	_, err := linkDeps(top, filepath.Join(dir, "loop"), tokR5b4Quoted)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too many levels")
	assertNoTokenR5b4(t, err.Error(), tokR5b4Quoted)
}

// The stack-level walk carries the token to linkDeps.
func TestStackLinkDepsR5b4_TokenNotShown(t *testing.T) {
	top, file := notDirR5b4(t)
	_, err := stackLinkDeps(top, models.Stack{Directory: filepath.Join(file, "x"+tokR5b4Quoted), ComposeFile: "compose.yaml"}, tokR5b4Quoted)
	require.Error(t, err)
	assertNoTokenR5b4(t, err.Error(), tokR5b4Quoted)
}

// The display boundary masks the token in every failure reason, without
// writing into the result it was given.
func TestRedactPullResultR5b4_MasksFailureReasons(t *testing.T) {
	failed := []RedeployFailure{{StackID: "a", Reason: "could not read " + tokR5b4}}
	sibling := []RedeployFailure{{StackID: "b", Reason: "after the pull: " + tokR5b4}}
	ar := truth.Partial("pulled", truth.KV("failedRedeploys", failed))
	ar.Details["siblingCheckFailures"] = sibling

	got, _ := redactPullResult(ar, &models.PullResult{}, tokR5b4)

	for _, key := range []string{"failedRedeploys", "siblingCheckFailures"} {
		fs, ok := got.Details[key].([]RedeployFailure)
		require.True(t, ok, key)
		require.Len(t, fs, 1)
		assert.NotContains(t, fs[0].Reason, tokR5b4, key)
		assert.Contains(t, fs[0].Reason, "***", key)
	}
	assert.Equal(t, "could not read "+tokR5b4, failed[0].Reason, "the caller's slice was written")
	assert.Equal(t, "after the pull: "+tokR5b4, sibling[0].Reason, "the caller's slice was written")
	assert.Equal(t, "after the pull: "+tokR5b4, ar.Details["siblingCheckFailures"].([]RedeployFailure)[0].Reason, "the caller's map was written")
}

// linkedStackR5b4 is a stack in top/s whose compose.yaml is a symlink to a
// path through a regular file, named with the token: every walk of it fails
// with that path.
func linkedStackR5b4(t *testing.T) (top string, st models.Stack) {
	t.Helper()
	top, _ = notDirR5b4(t)
	dir := filepath.Join(top, "s")
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.NoError(t, os.Symlink(filepath.Join("..", "file", tokR5b4Quoted), filepath.Join(dir, "compose.yaml")))
	return top, models.Stack{ID: "S", Directory: dir, ComposeFile: "compose.yaml", ProjectName: "s"}
}

// The walk before the pull (PullVerified's snapshot of a stack it redeploys).
func TestSnapshotLinksR5b4_TokenNotShown(t *testing.T) {
	top, st := linkedStackR5b4(t)
	snap := snapshotLinks(top, st, tokR5b4Quoted)
	require.Error(t, snap.err)
	assertNoTokenR5b4(t, snap.err.Error(), tokR5b4Quoted)
}

// The redeploy walk: the failure reason PullVerified records for the stack.
func TestStackFilesChangedSinceR5b4_TokenNotShown(t *testing.T) {
	top, st := linkedStackR5b4(t)
	_, err := stackFilesChangedSince(top, st, nil, linkSnapshot{}, tokR5b4Quoted)
	require.Error(t, err)
	assertNoTokenR5b4(t, err.Error(), tokR5b4Quoted)
}

// The sibling walk: placed before the pull, walked again after it.
func TestSiblingPlacementR5b4_TokenNotShown(t *testing.T) {
	top, st := linkedStackR5b4(t)
	p := placeSiblings(top, filepath.Join(top, "other"), []models.Stack{st}, tokR5b4Quoted)
	require.Len(t, p.stacks, 1)
	_, failures := p.changed(top, nil, tokR5b4Quoted)
	require.Len(t, failures, 1)
	assertNoTokenR5b4(t, failures[0].Reason, tokR5b4Quoted)
}

// Review S2: a stack FOLDER whose path holds the token, placed through a
// regular file so repoRel fails on it, in each pull walk that places it. The
// reasons are checked as the pull shows them: through redactPullResult.
func TestRepoRelWalksR5b4_TokenInStackFolderNotShown(t *testing.T) {
	top, file := notDirR5b4(t)
	st := models.Stack{ID: "F", Directory: filepath.Join(file, tokR5b4Quoted), ComposeFile: "compose.yaml", ProjectName: "f"}

	p := placeSiblings(top, filepath.Join(top, "other"), []models.Stack{st}, tokR5b4Quoted)
	_, siblingFailures := p.changed(top, nil, tokR5b4Quoted)
	require.Len(t, siblingFailures, 1)
	_, err := stackFilesChangedSince(top, st, nil, linkSnapshot{}, tokR5b4Quoted)
	require.Error(t, err, "stackFilesChangedSince")

	ar := truth.Partial("pulled", truth.KV("failedRedeploys", []RedeployFailure{{StackID: st.ID, Reason: err.Error()}}))
	ar.Details["siblingCheckFailures"] = siblingFailures
	shown, _ := redactPullResult(ar, &models.PullResult{}, tokR5b4Quoted)
	for _, key := range []string{"failedRedeploys", "siblingCheckFailures"} {
		fs := shown.Details[key].([]RedeployFailure)
		require.Len(t, fs, 1)
		assert.Contains(t, fs[0].Reason, "could not tell whether", key)
		assertNoTokenR5b4(t, fs[0].Reason, tokR5b4Quoted)
	}
}
