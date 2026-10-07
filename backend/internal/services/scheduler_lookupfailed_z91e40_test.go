package services

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestRunAutoUpdates_LookupFailedRowIsSkippedNotAppliedUnlocked is
// agent-os-z91e.40's acceptance. A scan whose stack lookup failed leaves the
// row with StackID "" and StackLookupFailed set. The apply loop takes the
// stack lock only for a non-empty StackID, so a container-scoped policy used
// to apply that row with no lock at all; UpdateContainer resolves the stack
// again, and a fault that cleared in between ran compose for it unlocked.
// c1 is that row and must be skipped (no update, a 'skipped' row, policy
// untouched, cached row kept, a note in update_apply_last_error); c2, same
// policy shape without the flag, is the control that proves the pass applied.
func TestRunAutoUpdates_LookupFailedRowIsSkippedNotAppliedUnlocked(t *testing.T) {
	checker := &fakeUpdateChecker{updateFn: succeedingUpdate}
	svc := newApplyFixture(t, checker)
	svc.SetOperationLock(NewOperationLock())
	seedContainerPolicy(t, svc, "c1")
	seedContainerPolicy(t, svc, "c2")

	svc.RunAutoUpdates(context.Background(), []models.CachedUpdate{
		{ContainerID: "c1", ContainerName: "web", ImageRef: "nginx:latest", StackLookupFailed: true},
		{ContainerID: "c2", ContainerName: "api", ImageRef: "nginx:latest"},
	})

	assert.Equal(t, []string{"c2"}, checker.updatedIDs(),
		"a row whose stack lookup failed must not be applied; the control must be")

	rows, _, err := svc.db.GetUpdateHistory(models.UpdateHistoryFilters{ContainerID: "c1"})
	require.NoError(t, err)
	assertSkippedRow(t, rows, "c1", "web", "nginx:latest",
		"skipped: the stack for this container could not be looked up during the scan; retried next pass")

	policy, err := svc.db.GetAutoUpdatePolicy("container", "c1")
	require.NoError(t, err)
	assert.Equal(t, 0, policy.ConsecutiveFailures, "a lookup skip is not an update failure")

	msg, err := svc.db.GetSetting(applyLastErrorKey)
	require.NoError(t, err)
	assert.Equal(t, "1 auto-update(s) skipped: their stack could not be looked up during the scan; retried next pass", msg)
}
