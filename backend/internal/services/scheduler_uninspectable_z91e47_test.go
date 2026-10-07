package services

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestScheduledApply_UninspectableContainerLeavesSkippedRow is
// agent-os-z91e.47 site 1. pruneVanishedTargets drops a cached update whose
// container could not be inspected (daemon down, timeout) before the pass, and
// used to leave no update_history row. It runs before the policy filter, so
// only an item with a policy, one that would otherwise have run, gets a
// 'skipped' row: c1 has a policy, c3 has none and must get no row. c2 inspects
// cleanly and is applied, the control that the pass ran.
func TestScheduledApply_UninspectableContainerLeavesSkippedRow(t *testing.T) {
	inspectErr := errors.New("Cannot connect to the Docker daemon")
	checker := &fakeUpdateChecker{
		updateFn: succeedingUpdate,
		inspectFn: func(containerID string) error {
			if containerID == "c2" {
				return nil
			}
			return inspectErr
		},
	}
	svc := newApplyFixture(t, checker)
	seedContainerPolicy(t, svc, "c1")
	seedContainerPolicy(t, svc, "c2")
	seedCachedUpdate(t, svc, "c1", "web")
	seedCachedUpdate(t, svc, "c2", "api")
	seedCachedUpdate(t, svc, "c3", "nopolicy")
	enableScheduledApply(t, svc, "03:00")

	require.True(t, svc.applyNow(context.Background()))

	assert.Equal(t, []string{"c2"}, checker.updatedIDs(), "only the inspectable, policied container is applied")

	rows, _, err := svc.db.GetUpdateHistory(models.UpdateHistoryFilters{ContainerID: "c1"})
	require.NoError(t, err)
	assertSkippedRow(t, rows, "c1", "web", "nginx:latest",
		"skipped: the container could not be inspected ("+inspectErr.Error()+"); retried next pass")

	_, n, err := svc.db.GetUpdateHistory(models.UpdateHistoryFilters{ContainerID: "c3"})
	require.NoError(t, err)
	assert.Equal(t, 0, n, "a container with no auto-update policy would not have run, so it gets no row")

	policy, err := svc.db.GetAutoUpdatePolicy("container", "c1")
	require.NoError(t, err)
	assert.Equal(t, 0, policy.ConsecutiveFailures, "an inspect failure is not an update failure")
	cached, err := svc.db.GetCachedUpdates()
	require.NoError(t, err)
	assert.Len(t, cached, 2, "c1 and c3 stay cached for the next pass; c2 was applied and evicted")
}
