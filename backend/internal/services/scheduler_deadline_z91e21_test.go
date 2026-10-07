package services

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// firstHangsChecker is a pass of two items where the first one hangs until its
// context ends. Every later UpdateContainer call fails on an ended context, the
// way a real pull or compose run does, and is recorded so a test can tell an
// item that started from one that never did (agent-os-z91e.21).
type firstHangsChecker struct {
	hungID  string
	entered chan struct{}

	mu    sync.Mutex
	calls []string
}

func (c *firstHangsChecker) CheckForUpdates(context.Context, DashboardDB) ([]models.ContainerUpdateInfo, error) {
	return nil, nil
}

func (c *firstHangsChecker) InspectContainer(context.Context, string) (container.InspectResponse, error) {
	return container.InspectResponse{}, nil
}

func (c *firstHangsChecker) UpdateContainer(ctx context.Context, id string, _ DashboardDB) (models.UpdateResult, truth.ActionResult) {
	c.mu.Lock()
	c.calls = append(c.calls, id)
	c.mu.Unlock()
	if id == c.hungID {
		select {
		case c.entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return models.UpdateResult{}, truth.Failed("docker pull did not finish", ctx.Err())
	}
	if ctx.Err() != nil {
		return models.UpdateResult{}, truth.Failed("could not start update", ctx.Err())
	}
	return models.UpdateResult{}, truth.Success("image advanced")
}

func (c *firstHangsChecker) called() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

func z91e21Updates() []models.CachedUpdate {
	return []models.CachedUpdate{
		{ContainerID: "c1", ContainerName: "web", ImageRef: "nginx:latest", StackID: "s1"},
		{ContainerID: "c2", ContainerName: "api", ImageRef: "api:latest", StackID: "s2"},
	}
}

// z91e21Fixture seeds two container policies. The second one already has two
// failures, so a third counted against it pauses it: that is the harm.
func z91e21Fixture(t *testing.T) (*SchedulerService, *firstHangsChecker) {
	t.Helper()
	checker := &firstHangsChecker{hungID: "c1", entered: make(chan struct{}, 1)}
	svc := newApplyFixture(t, checker)
	svc.SetOperationLock(NewOperationLock())
	seedContainerPolicy(t, svc, "c1")
	now := time.Now().Format(time.RFC3339)
	require.NoError(t, svc.db.UpsertAutoUpdatePolicy(&models.AutoUpdatePolicy{
		ID: "policy-c2", TargetType: "container", TargetID: "c2", Enabled: true,
		ConsecutiveFailures: 2, CreatedAt: now, UpdatedAt: now,
	}))
	return svc, checker
}

func z91e21PolicyFor(t *testing.T, svc *SchedulerService, id string) models.AutoUpdatePolicy {
	t.Helper()
	policies, err := svc.db.GetEnabledAutoUpdatePolicies()
	require.NoError(t, err)
	for _, p := range policies {
		if p.TargetID == id {
			return p
		}
	}
	t.Fatalf("policy for %s is gone or was paused (GetEnabledAutoUpdatePolicies no longer returns it)", id)
	return models.AutoUpdatePolicy{}
}

func z91e21Rows(t *testing.T, svc *SchedulerService, stackID string) []models.UpdateHistoryEntry {
	t.Helper()
	rows, _, err := svc.db.GetUpdateHistory(models.UpdateHistoryFilters{StackID: stackID})
	require.NoError(t, err)
	return rows
}

// TestAutoUpdateDeadline_UnstartedItemIsNotCountedAsFailed is agent-os-z91e.21's
// acceptance. Once a pass's deadline fires, every later item used to run on the
// ended context, record a failed run and add to its policy's
// ConsecutiveFailures, so one hung container paused unrelated policies after
// three passes. An item the pass never started must leave its policy and the
// history alone, and be reported as not started; the item that hung still
// records a failed run naming the timeout.
func TestAutoUpdateDeadline_UnstartedItemIsNotCountedAsFailed(t *testing.T) {
	svc, checker := z91e21Fixture(t)
	svc.applyTimeout = 200 * time.Millisecond

	runUntilGuard(t, "RunAutoUpdates", func() {
		svc.RunAutoUpdates(context.Background(), z91e21Updates())
	})

	assert.Equal(t, []string{"c1"}, checker.called(), "the second item must never be handed to UpdateContainer")

	p2 := z91e21PolicyFor(t, svc, "c2")
	assert.Equal(t, 2, p2.ConsecutiveFailures, "an item the pass never started must not count as a failure")
	assert.False(t, p2.Paused)
	assert.Empty(t, z91e21Rows(t, svc, "s2"), "an unstarted item leaves no history row (update_history has no 'skipped' status)")

	hung := z91e21Rows(t, svc, "s1")
	require.Len(t, hung, 1)
	assert.Equal(t, "failed", hung[0].Status)
	require.NotNil(t, hung[0].ErrorMessage)
	assert.Equal(t, "auto-update timed out after 200ms: context deadline exceeded", *hung[0].ErrorMessage)
	assert.Equal(t, 1, z91e21PolicyFor(t, svc, "c1").ConsecutiveFailures, "the item that hung still counts")

	msg, err := svc.db.GetSetting(applyLastErrorKey)
	require.NoError(t, err)
	assert.Equal(t, "1 auto-update(s) not started: pass deadline reached; retried next pass", msg)
}

// TestAutoUpdateDeadline_ShutdownMidPassIsNotCountedAsFailed is the Stop() side:
// the parent context ending is a shutdown, not a deadline, and the unstarted
// item is reported as such.
func TestAutoUpdateDeadline_ShutdownMidPassIsNotCountedAsFailed(t *testing.T) {
	svc, checker := z91e21Fixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-checker.entered
		cancel()
	}()

	runUntilGuard(t, "RunAutoUpdates", func() { svc.RunAutoUpdates(ctx, z91e21Updates()) })

	assert.Equal(t, []string{"c1"}, checker.called())
	p2 := z91e21PolicyFor(t, svc, "c2")
	assert.Equal(t, 2, p2.ConsecutiveFailures)
	assert.False(t, p2.Paused)
	assert.Empty(t, z91e21Rows(t, svc, "s2"))

	msg, err := svc.db.GetSetting(applyLastErrorKey)
	require.NoError(t, err)
	assert.Equal(t, "1 auto-update(s) not started: shutdown; retried next pass", msg)
}

// TestAutoUpdateDeadline_ItemsRunBeforeTheDeadlineAreUnaffected is the pass
// side of the same instrument: with a live context the second item starts, is
// applied, and the apply error stays empty.
func TestAutoUpdateDeadline_ItemsRunBeforeTheDeadlineAreUnaffected(t *testing.T) {
	svc, checker := z91e21Fixture(t)
	checker.hungID = "none"

	svc.RunAutoUpdates(context.Background(), z91e21Updates())

	assert.Equal(t, []string{"c1", "c2"}, checker.called())
	assert.Equal(t, 0, z91e21PolicyFor(t, svc, "c2").ConsecutiveFailures)
	msg, err := svc.db.GetSetting(applyLastErrorKey)
	require.NoError(t, err)
	assert.Empty(t, msg)
}
