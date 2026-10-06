package services

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// pullLockFixture is a clone one commit behind its origin, registered as stack
// "s1" in a migrated database. The new commit only touches README, so a pull
// with redeploy finds no stack whose files changed and runs no docker compose.
func pullLockFixture(t *testing.T) (svc *GitService, lock *OperationLock, work string) {
	t.Helper()
	origin := t.TempDir()
	mustGit(t, origin, "init", "-q", "--bare", "-b", "main")
	seed := t.TempDir()
	mustGit(t, seed, "init", "-q", "-b", "main")
	writeStackFile(t, seed, "compose.yml", "services: {}\n")
	mustGit(t, seed, "add", "-A")
	mustGit(t, seed, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "seed")
	mustGit(t, seed, "remote", "add", "origin", origin)
	mustGit(t, seed, "push", "-q", "origin", "main")
	work = t.TempDir()
	mustGit(t, work, "clone", "-q", origin, ".")
	writeStackFile(t, seed, "README", "second\n")
	mustGit(t, seed, "add", "-A")
	mustGit(t, seed, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "second")
	mustGit(t, seed, "push", "-q", "origin", "main")

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: work, Name: "w", RootDir: work, ScannedAt: time.Now()}))
	require.NoError(t, db.UpsertStack(models.Stack{ID: "s1", ProjectName: "web", Directory: work, ComposeFile: "compose.yml", Status: "running"}))

	svc = NewGitService(&config.Config{}, db)
	lock = NewOperationLock()
	svc.SetOperationLock(lock)
	return svc, lock, work
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(mustGitOut(t, dir, "rev-parse", "HEAD"))
}

// TestPullVerified_RedeployOnLockedStackIs409AndPullsNothing is agent-os-a1ye.4:
// a git pull with redeploy used to rewrite a stack's files and restart it while
// a backup or lifecycle op held that stack. The pull without redeploy had the
// same gap; see git_pull_lock_ai1z_test.go.
func TestPullVerified_RedeployOnLockedStackIs409AndPullsNothing(t *testing.T) {
	svc, lock, work := pullLockFixture(t)
	before := headOf(t, work)
	backupToken, err := lock.Acquire("s1", OpKindBackup)
	require.NoError(t, err)

	ar, pullResult := svc.PullVerified(work, true, &DockerService{})

	require.Equal(t, truth.OutcomeFailed, ar.Outcome, "reason %q", ar.Reason)
	var appErr *models.AppError
	require.True(t, errors.As(ar.Err, &appErr), "err %v is not an AppError, so the handler would answer 500", ar.Err)
	assert.Equal(t, http.StatusConflict, appErr.Status)
	assert.Equal(t, models.ErrOperationInProgress, appErr.Code)
	assert.Contains(t, appErr.Message, "stack web: backup in progress since")
	assert.Nil(t, pullResult)
	assert.Equal(t, before, headOf(t, work), "the pull ran even though the stack was locked")

	_, err = lock.Acquire("s1", OpKindStart)
	require.Error(t, err, "the backup's lock was lost")
	lock.Release("s1", backupToken)
}

// TestPullVerified_RedeployOnFreeStackPullsAndReleases is the other side: same
// fixture, stack free, so the pull lands and the lock is free afterwards.
func TestPullVerified_RedeployOnFreeStackPullsAndReleases(t *testing.T) {
	svc, lock, work := pullLockFixture(t)
	before := headOf(t, work)

	ar, _ := svc.PullVerified(work, true, &DockerService{})

	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	assert.NotEqual(t, before, headOf(t, work), "the pull did not advance HEAD")
	token, err := lock.Acquire("s1", OpKindStart)
	require.NoError(t, err, "PullVerified left the stack locked")
	lock.Release("s1", token)
}
