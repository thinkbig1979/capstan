package handlers

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// TestUpdateStack_LockedStackAnswers409 is agent-os-a1ye.4's case (c): a
// manual stack update used to be enqueued while a backup or lifecycle op held
// the stack, so the two ran compose against the same stack at once.
func TestUpdateStack_LockedStackAnswers409(t *testing.T) {
	h := newTestResourcesHandlerWithJobManager(t)
	lock := services.NewOperationLock()
	h.SetOperationLock(lock)
	seedStack(t, h.db, "s1", "web")
	require.NoError(t, h.db.SetCachedUpdates([]models.CachedUpdate{cachedUpdate("s1", "web")}))

	backupToken, err := lock.Acquire("s1", services.OpKindBackup)
	require.NoError(t, err)

	w := postUpdateStack(t, h, "s1")

	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	body := decodeBody(t, w)
	assert.Equal(t, models.ErrOperationInProgress, body["code"])
	assert.Contains(t, body["message"], "backup in progress since")
	assert.Empty(t, h.jobManager.List(), "a refused update must not be queued")

	// The refusal must not have disturbed the backup's hold.
	_, err = lock.Acquire("s1", services.OpKindStart)
	require.Error(t, err, "the backup's lock was lost")
	lock.Release("s1", backupToken)
}

// TestUpdateStack_JobHoldsLockUntilItFinishes is the other side of the
// instrument: with the same lock wired and the stack free, the update is
// accepted, and the lock it took is released when the job ends (here the job
// fails fast on the nil Docker service, which exercises the same defer).
func TestUpdateStack_JobHoldsLockUntilItFinishes(t *testing.T) {
	h := newTestResourcesHandlerWithJobManager(t)
	lock := services.NewOperationLock()
	h.SetOperationLock(lock)
	seedStack(t, h.db, "s1", "web")
	require.NoError(t, h.db.SetCachedUpdates([]models.CachedUpdate{cachedUpdate("s1", "web")}))

	w := postUpdateStack(t, h, "s1")
	require.Equal(t, http.StatusAccepted, w.Code, "body: %s", w.Body.String())
	jobID, _ := decodeBody(t, w)["jobId"].(string)
	require.NotEmpty(t, jobID)

	guard := hangGuardDeadline(t)
	for {
		if j := h.jobManager.Get(jobID); j != nil && !j.FinishedAt.IsZero() {
			break
		}
		if time.Now().After(guard) {
			t.Fatal("the update job never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}

	token, err := lock.Acquire("s1", services.OpKindStart)
	require.NoError(t, err, "the update job finished but left the stack locked")
	lock.Release("s1", token)
}
