package services

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// seed71spPolicy stores one enabled container policy with a fixed, old
// updated_at, so a test can tell whether the pass wrote the row.
func seed71spPolicy(t *testing.T, svc *SchedulerService, containerID string, failures int) {
	t.Helper()
	const long = "2020-01-01T00:00:00Z"
	require.NoError(t, svc.db.UpsertAutoUpdatePolicy(&models.AutoUpdatePolicy{
		ID:                  "policy-" + containerID,
		TargetType:          "container",
		TargetID:            containerID,
		Enabled:             true,
		ConsecutiveFailures: failures,
		CreatedAt:           long,
		UpdatedAt:           long,
	}))
}

// disable71sp writes what handlers/updates.go upsertAutoUpdatePolicy writes for
// a PUT with enabled=false on an existing row: same id, counters cleared.
func disable71sp(t *testing.T, svc *SchedulerService, containerID string) {
	t.Helper()
	require.NoError(t, svc.db.UpsertAutoUpdatePolicy(&models.AutoUpdatePolicy{
		ID:         "policy-" + containerID,
		TargetType: "container",
		TargetID:   containerID,
		Enabled:    false,
		CreatedAt:  "2020-01-01T00:00:00Z",
		UpdatedAt:  "2021-01-01T00:00:00Z",
	}))
}

func pausedHistoryCount71sp(t *testing.T, svc *SchedulerService) int {
	t.Helper()
	entries, _, err := svc.db.GetUpdateHistory(models.UpdateHistoryFilters{Page: 1, Limit: 100, Status: "paused"})
	require.NoError(t, err)
	return len(entries)
}

// TestAutoUpdatePassKeepsOperatorChange71sp is agent-os-71sp's acceptance. The
// pass reads its policies once; an operator who disables or deletes a policy
// while an update runs must find it still disabled (still deleted) after the
// pass, whatever that update's outcome.
func TestAutoUpdatePassKeepsOperatorChange71sp(t *testing.T) {
	update := []models.CachedUpdate{{ContainerID: "c1", ContainerName: "web", ImageRef: "nginx:latest"}}

	cases := []struct {
		name     string
		failures int // stored before the pass
		outcome  func(string) (models.UpdateResult, truth.ActionResult)
		change   func(*testing.T, *SchedulerService)
		deleted  bool
	}{
		{"disabled during a failing update stays disabled", 0, failingUpdate,
			func(t *testing.T, s *SchedulerService) { disable71sp(t, s, "web") }, false},
		{"disabled during a third failing update stays disabled and is not paused", 2, failingUpdate,
			func(t *testing.T, s *SchedulerService) { disable71sp(t, s, "web") }, false},
		{"disabled during a succeeding update stays disabled", 1, succeedingUpdate,
			func(t *testing.T, s *SchedulerService) { disable71sp(t, s, "web") }, false},
		{"deleted during a failing update stays deleted", 0, failingUpdate,
			func(t *testing.T, s *SchedulerService) {
				require.NoError(t, s.db.DeleteAutoUpdatePolicy("container", "web"))
			}, true},
		{"deleted during a succeeding update stays deleted", 1, succeedingUpdate,
			func(t *testing.T, s *SchedulerService) {
				require.NoError(t, s.db.DeleteAutoUpdatePolicy("container", "web"))
			}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var svc *SchedulerService
			svc = newApplyFixture(t, &fakeUpdateChecker{updateFn: func(id string) (models.UpdateResult, truth.ActionResult) {
				tc.change(t, svc) // between the pass's read and its write
				return tc.outcome(id)
			}})
			seed71spPolicy(t, svc, "web", tc.failures)

			svc.RunAutoUpdates(context.Background(), update)

			policy, err := svc.db.GetAutoUpdatePolicy("container", "web")
			if tc.deleted {
				require.True(t, errors.Is(err, errdefs.ErrNotFound), "the pass re-created a deleted policy: %+v, err %v", policy, err)
				return
			}
			require.NoError(t, err)
			require.False(t, policy.Enabled, "the pass re-enabled a policy disabled during it")
			require.False(t, policy.Paused)
			require.Equal(t, 0, policy.ConsecutiveFailures)
			require.Equal(t, "2021-01-01T00:00:00Z", policy.UpdatedAt, "the pass wrote a row it no longer owns")
			require.Equal(t, 0, pausedHistoryCount71sp(t, svc), "no 'paused' row for a policy the pass did not pause")
		})
	}
}

// TestAutoUpdatePassStillCountsUntouchedPolicy71sp is the other side: a policy
// nobody touched during the pass still gets its counter and timestamp.
func TestAutoUpdatePassStillCountsUntouchedPolicy71sp(t *testing.T) {
	update := []models.CachedUpdate{{ContainerID: "c1", ContainerName: "web", ImageRef: "nginx:latest"}}

	cases := []struct {
		name         string
		failures     int
		outcome      func(string) (models.UpdateResult, truth.ActionResult)
		wantFailures int
		wantPaused   bool
	}{
		{"a failure counts one", 0, failingUpdate, 1, false},
		{"a third failure pauses", 2, failingUpdate, 3, true},
		{"a success resets", 2, succeedingUpdate, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newApplyFixture(t, &fakeUpdateChecker{updateFn: tc.outcome})
			seed71spPolicy(t, svc, "web", tc.failures)

			svc.RunAutoUpdates(context.Background(), update)

			policy, err := svc.db.GetAutoUpdatePolicy("container", "web")
			require.NoError(t, err)
			require.True(t, policy.Enabled)
			require.Equal(t, tc.wantFailures, policy.ConsecutiveFailures)
			require.Equal(t, tc.wantPaused, policy.Paused)
			require.NotEqual(t, "2020-01-01T00:00:00Z", policy.UpdatedAt, "updated_at was not written")
			wantPausedRows := 0
			if tc.wantPaused {
				wantPausedRows = 1
			}
			require.Equal(t, wantPausedRows, pausedHistoryCount71sp(t, svc))
		})
	}
}
