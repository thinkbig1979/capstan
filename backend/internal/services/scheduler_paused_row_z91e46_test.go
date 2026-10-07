package services

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestRunAutoUpdates_PausedRowIsClearable is agent-os-z91e.46's acceptance.
// The 'paused' row written when a policy hits three failures had no
// completed_at, and retention and the manual clear both delete by
// completed_at, so that row could never be deleted. It must be complete when
// written (completed_at = started_at, as recordSkippedUpdate does), and the
// manual clear must then remove it. The three 'failed' rows on the same pass
// are the control: the clear reaches them either way, so a clear that ran at
// all is not what this test measures.
func TestRunAutoUpdates_PausedRowIsClearable(t *testing.T) {
	checker := &fakeUpdateChecker{updateFn: failingUpdate}
	svc := newApplyFixture(t, checker)
	seedContainerPolicy(t, svc, "c1")

	update := models.CachedUpdate{ContainerID: "c1", ContainerName: "web", ImageRef: "nginx:latest"}
	for i := 0; i < 3; i++ {
		svc.RunAutoUpdates(context.Background(), []models.CachedUpdate{update})
	}

	paused, n, err := svc.db.GetUpdateHistory(models.UpdateHistoryFilters{Status: "paused"})
	require.NoError(t, err)
	require.Equal(t, 1, n, "three failures write one paused row")
	if assert.NotNil(t, paused[0].CompletedAt, "a paused row is complete, so retention can age it out") {
		assert.Equal(t, paused[0].StartedAt, *paused[0].CompletedAt)
	}

	deleted, err := svc.db.DeleteUpdateHistoryOlderThan(time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 4, deleted, "the manual clear removes the three failed rows and the paused row")
	_, n, err = svc.db.GetUpdateHistory(models.UpdateHistoryFilters{Status: "paused"})
	require.NoError(t, err)
	assert.Equal(t, 0, n, "the paused row must not survive the manual clear")
}
