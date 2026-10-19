package services

import (
	_ "embed"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// TestL4h6_UnresolvableStackFolderIsNamed: one registered stack whose folder
// cannot be resolved (a file where a parent folder should be, ENOTDIR) refuses
// every pull, commit and push, which stays right. The refusal now names the
// stack and its folder, in the shape the delete refusal uses
// (blockingStackId, blockingDirectory), because the user cannot fix a folder
// they are not told about.
func TestL4h6_UnresolvableStackFolderIsNamed(t *testing.T) {
	ops := map[string]func(f *commitFix) truth.ActionResult{
		"pull": func(f *commitFix) truth.ActionResult {
			ar, _ := f.svc.PullVerified(f.dir, false, nil)
			return ar
		},
		"commit": func(f *commitFix) truth.ActionResult {
			ar, _ := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"x"}})
			return ar
		},
		"push": func(f *commitFix) truth.ActionResult {
			ar, _ := f.svc.PushVerified(f.dir)
			return ar
		},
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			f := newCommitFix(t, false)
			// "x" is a file in the repository, so nothing can sit below it.
			badDir := filepath.Join(f.top, "x", "stack")
			f.registerStack(t, badDir, "s-bad", "bad")

			ar := op(f)

			require.Equal(t, truth.OutcomeFailed, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
			var pathErr *RepoStackPathError
			require.True(t, errors.As(ar.Err, &pathErr), "err %v is not a RepoStackPathError (reason %q)", ar.Err, ar.Reason)
			assert.Equal(t, "s-bad", pathErr.Stack.ID)
			assert.Contains(t, ar.Reason, "stack bad")
			assert.Contains(t, ar.Reason, badDir)
			assert.Equal(t, "s-bad", ar.Details["blockingStackId"])
			assert.Equal(t, badDir, ar.Details["blockingDirectory"])

			// Control: without that stack the same operation is not refused
			// by the lock, so the naming above is about the bad folder.
			require.NoError(t, f.db.DeleteStack("s-bad"))
			ar = op(f)
			var again *RepoStackPathError
			assert.False(t, errors.As(ar.Err, &again), "still refused for a stack path after the stack was removed: %q %v", ar.Reason, ar.Err)
			assert.NotContains(t, ar.Reason, "lock the repository's stacks")
		})
	}
}

// TestL4h6_OtherLockFaultsKeepTheirReason: a stack list that cannot be read is
// not a stack path error and keeps the generic reason.
func TestL4h6_OtherLockFaultsKeepTheirReason(t *testing.T) {
	ar := repoLockFailure("could not lock the repository's stacks", "commit", errors.New("could not list the stacks to lock: boom"))
	assert.Equal(t, "could not lock the repository's stacks", ar.Reason)
	assert.Nil(t, ar.Details)
	assert.EqualError(t, ar.Err, "could not list the stacks to lock: boom")
}

// The sources are embedded, not read from disk, so a go test -overlay mutant
// of either file is what this test sees.
//
//go:embed git.go
var l4h6GitSrc string

//go:embed git_commit.go
var l4h6GitCommitSrc string

// lockCallsWithoutHelper returns the call sites of lockRepo and lockRepoStacks
// in src whose error is not answered by repoLockFailure within the next eight
// lines. lockRepo's own call of lockRepoStacks hands the error up and is exempt.
func lockCallsWithoutHelper(src string) []int {
	lines := strings.Split(src, "\n")
	var bad []int
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "func ") ||
			(!strings.Contains(l, ".lockRepo(") && !strings.Contains(l, ".lockRepoStacks(")) {
			continue
		}
		if strings.HasPrefix(trimmed, "_, release, err := s.lockRepoStacks(top, kind)") {
			continue
		}
		// The window stops at the next lock call, so a site cannot borrow the
		// next site's helper.
		end := min(i+9, len(lines))
		for j := i + 1; j < end; j++ {
			if strings.Contains(lines[j], ".lockRepo(") || strings.Contains(lines[j], ".lockRepoStacks(") {
				end = j
				break
			}
		}
		if !strings.Contains(strings.Join(lines[i:end], "\n"), "repoLockFailure(") {
			bad = append(bad, i+1)
		}
	}
	return bad
}

// TestL4h6_EveryLockCallAnswersThroughTheHelper keeps the three call sites on
// repoLockFailure, so a new git write cannot go back to an unnamed refusal.
func TestL4h6_EveryLockCallAnswersThroughTheHelper(t *testing.T) {
	// Control first: a call that answers with a plain Failed is reported.
	planted := "\trelease, err := s.lockRepo(top, OpKindGitPush)\n\tif err != nil {\n\t\treturn truth.Failed(\"x\", err), nil\n\t}\n"
	require.Equal(t, []int{1}, lockCallsWithoutHelper(planted), "the guard cannot see a bypassing call")
	// And a bypass followed by a compliant call must not borrow its helper.
	borrowed := planted + "\trelease, err = s.lockRepo(top, OpKindGitCommit)\n\tif err != nil {\n\t\treturn repoLockFailure(\"x\", \"commit\", err), nil\n\t}\n"
	require.Equal(t, []int{1}, lockCallsWithoutHelper(borrowed), "a bypassing call borrowed the next site's helper")

	calls := 0
	for name, src := range map[string]string{"git.go": l4h6GitSrc, "git_commit.go": l4h6GitCommitSrc} {
		calls += strings.Count(src, ".lockRepo(") + strings.Count(src, ".lockRepoStacks(")
		assert.Empty(t, lockCallsWithoutHelper(src), "%s: a lock call answers without repoLockFailure", name)
	}
	assert.GreaterOrEqual(t, calls, 4, "the scan found too few call sites to be reading the right files")
}
