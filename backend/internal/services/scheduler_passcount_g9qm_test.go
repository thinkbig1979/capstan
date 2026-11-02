package services

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// seedStackPolicyWithFailures enables auto-update for a whole stack, starting
// from the given failure count.
func seedStackPolicyWithFailures(t *testing.T, svc *SchedulerService, stackID string, failures int) {
	t.Helper()
	now := time.Now().Format(time.RFC3339)
	require.NoError(t, svc.db.UpsertAutoUpdatePolicy(&models.AutoUpdatePolicy{
		ID:                  "policy-" + stackID,
		TargetType:          "stack",
		TargetID:            stackID,
		Enabled:             true,
		ConsecutiveFailures: failures,
		CreatedAt:           now,
		UpdatedAt:           now,
	}))
}

// TestAutoUpdateConsecutiveFailuresPerPass is agent-os-g9qm's acceptance. A
// stack policy covers every service of the stack, so a pass counts at most one
// failure against it, and a success on one service does not wipe a failure on
// another in the same pass.
func TestAutoUpdateConsecutiveFailuresPerPass(t *testing.T) {
	stackUpdates := []models.CachedUpdate{
		{ContainerID: "c1", ContainerName: "web", ImageRef: "nginx:latest", StackID: "s1"},
		{ContainerID: "c2", ContainerName: "api", ImageRef: "nginx:latest", StackID: "s1"},
		{ContainerID: "c3", ContainerName: "db", ImageRef: "nginx:latest", StackID: "s1"},
	}
	cases := []struct {
		name         string
		updates      []models.CachedUpdate
		startFrom    int
		updateFn     func(string) (models.UpdateResult, truth.ActionResult)
		wantFailures int
	}{
		{"three failing services count once", stackUpdates, 0, failingUpdate, 1},
		{"a success does not reset a failure in the same pass", stackUpdates[:2], 0,
			func(id string) (models.UpdateResult, truth.ActionResult) {
				if id == "c1" {
					return failingUpdate(id)
				}
				return succeedingUpdate(id)
			}, 1},
		{"all succeeding resets the count", stackUpdates, 2, succeedingUpdate, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newApplyFixture(t, &fakeUpdateChecker{updateFn: tc.updateFn})
			seedStackPolicyWithFailures(t, svc, "s1", tc.startFrom)

			svc.RunAutoUpdates(context.Background(), tc.updates)

			policy, err := svc.db.GetAutoUpdatePolicy("stack", "s1")
			require.NoError(t, err)
			require.Equal(t, tc.wantFailures, policy.ConsecutiveFailures)
			require.False(t, policy.Paused, "no case here reaches three failures")
		})
	}
}
