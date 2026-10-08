package services

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// TestRunAutoUpdates_StandaloneUpdateTakesATurnPrunesWaitFor is agent-os-qags.30
// arm (d) for the auto-update path. A container no managed stack owns has an
// empty StackID, so the pass took no lock; its update removes the old container
// and then creates and starts the new one, and a container prune between the
// create and the start removes the `created` one, leaving no container. While
// UpdateContainer runs, an exclusive operation (the prune) must be refused,
// naming the update; afterwards the turn is gone.
func TestRunAutoUpdates_StandaloneUpdateTakesATurnPrunesWaitFor(t *testing.T) {
	var midUpdateErr error
	var lock *OperationLock
	checker := &fakeUpdateChecker{}
	checker.updateFn = func(string) (models.UpdateResult, truth.ActionResult) {
		_, midUpdateErr = lock.AcquireExclusive(OpKindContainerPrune)
		return models.UpdateResult{}, truth.Success("image advanced")
	}
	svc := newApplyFixture(t, checker)
	lock = NewOperationLock()
	svc.SetOperationLock(lock)
	seedContainerPolicy(t, svc, "c1")

	svc.RunAutoUpdates(context.Background(), []models.CachedUpdate{
		{ContainerID: "c1", ContainerName: "web", ImageRef: "nginx:latest"}, // no StackID
	})

	assert.Equal(t, []string{"c1"}, checker.updatedIDs(), "the standalone container is updated")
	require.Error(t, midUpdateErr, "an exclusive operation ran in the middle of a standalone update")
	assert.Contains(t, midUpdateErr.Error(), "update in progress since")

	token, err := lock.AcquireExclusive(OpKindContainerPrune)
	require.NoError(t, err, "the pass left the standalone container's turn held")
	lock.ReleaseExclusive(token)
}

// TestRunAutoUpdates_BusyStandaloneContainerIsSkipped: when something already
// holds the container's turn (a manual update of it), the pass skips it with a
// 'skipped' row and a note naming the container, touches nothing, and the free
// one on the same pass is still applied (the control).
func TestRunAutoUpdates_BusyStandaloneContainerIsSkipped(t *testing.T) {
	checker := &fakeUpdateChecker{updateFn: succeedingUpdate}
	svc := newApplyFixture(t, checker)
	lock := NewOperationLock()
	svc.SetOperationLock(lock)
	seedContainerPolicy(t, svc, "c1")
	seedContainerPolicy(t, svc, "c2")

	manual, err := lock.Acquire("c1", OpKindUpdate)
	require.NoError(t, err)

	svc.RunAutoUpdates(context.Background(), []models.CachedUpdate{
		{ContainerID: "c1", ContainerName: "web", ImageRef: "nginx:latest"},
		{ContainerID: "c2", ContainerName: "api", ImageRef: "nginx:latest"},
	})

	assert.Equal(t, []string{"c2"}, checker.updatedIDs(), "the busy container must not be touched; the free one must be")
	rows, _, err := svc.db.GetUpdateHistory(models.UpdateHistoryFilters{ContainerID: "c1"})
	require.NoError(t, err)
	assertSkippedRow(t, rows, "c1", "web", "nginx:latest",
		"skipped: container web is busy ("+busyHolder(t, lock, "c1")+"); retried next pass")
	msg, err := svc.db.GetSetting(applyLastErrorKey)
	require.NoError(t, err)
	assert.Equal(t, "1 auto-update(s) skipped: another operation in progress on container web; retried next pass", msg)
	lock.Release("c1", manual)
}
