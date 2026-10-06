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

// hungUpdateChecker stands in for a docker pull or compose up that never
// returns on its own: UpdateContainer blocks until its context ends, or until
// release is closed. release exists only so a red run (no deadline) does not
// leak the blocked goroutine past the test.
type hungUpdateChecker struct {
	finding models.ContainerUpdateInfo
	entered chan struct{}
	release chan struct{}

	mu          sync.Mutex
	hadDeadline bool
}

func newHungUpdateChecker(t *testing.T, finding models.ContainerUpdateInfo) *hungUpdateChecker {
	t.Helper()
	c := &hungUpdateChecker{
		finding: finding,
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	t.Cleanup(func() { close(c.release) })
	return c
}

func (c *hungUpdateChecker) CheckForUpdates(context.Context, DashboardDB) ([]models.ContainerUpdateInfo, error) {
	return []models.ContainerUpdateInfo{c.finding}, nil
}

func (c *hungUpdateChecker) InspectContainer(context.Context, string) (container.InspectResponse, error) {
	return container.InspectResponse{}, nil
}

func (c *hungUpdateChecker) UpdateContainer(ctx context.Context, _ string, _ DashboardDB) (models.UpdateResult, truth.ActionResult) {
	_, ok := ctx.Deadline()
	c.mu.Lock()
	c.hadDeadline = ok
	c.mu.Unlock()
	select {
	case c.entered <- struct{}{}:
	default:
	}

	select {
	case <-ctx.Done():
		return models.UpdateResult{}, truth.Failed("docker pull did not finish", ctx.Err())
	case <-c.release:
		return models.UpdateResult{}, truth.Failed("released by test cleanup", nil)
	}
}

// o1jgHangGuardCeiling caps how long a test here waits for a pass that should
// end at a 200ms deadline. It is built like handlers.hangGuardDeadline, which
// this package cannot import: a passing run never reaches it, so it does not
// make the result depend on runner load.
const o1jgHangGuardCeiling = 30 * time.Second

func o1jgHangGuard(t *testing.T) time.Time {
	t.Helper()
	guard := time.Now().Add(o1jgHangGuardCeiling)
	if d, ok := t.Deadline(); ok {
		if reportBy := d.Add(-5 * time.Second); reportBy.Before(guard) {
			guard = reportBy
		}
	}
	if floor := time.Now().Add(time.Second); guard.Before(floor) {
		guard = floor
	}
	return guard
}

// runUntilGuard runs fn in a goroutine and fails the test, by name, if it is
// still running at the guard.
func runUntilGuard(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(time.Until(o1jgHangGuard(t))):
		t.Fatalf("%s was still running at the hang guard: the auto-apply has no deadline", what)
	}
}

func hungFinding() models.ContainerUpdateInfo {
	return models.ContainerUpdateInfo{
		ContainerID:   "c1",
		ContainerName: "web",
		Image:         "nginx:latest",
		ImageRef:      "nginx:latest",
		State:         "running",
		StackID:       "s1",
	}
}

func onlyHistoryRow(t *testing.T, svc *SchedulerService) models.UpdateHistoryEntry {
	t.Helper()
	rows, n, err := svc.db.GetUpdateHistory(models.UpdateHistoryFilters{StackID: "s1"})
	require.NoError(t, err)
	require.Equal(t, 1, n, "the hung update must leave exactly one history row")
	return rows[0]
}

func requireStackFree(t *testing.T, lock *OperationLock) {
	t.Helper()
	token, err := lock.Acquire("s1", OpKindStart)
	require.NoError(t, err, "the auto-apply left the stack's operation lock held")
	lock.Release("s1", token)
}

// TestAutoUpdateDeadline_ImmediateHungUpdateEnds is agent-os-o1jg's
// acceptance. Immediate mode (the seeded default) applies on the scan tick,
// and runCycle used to hand RunAutoUpdates the scheduler's cancel-only context,
// so a docker pull that never returned held the stack's lock until restart.
// The pass must end at its deadline, record the run as failed naming the
// timeout, and release the lock.
func TestAutoUpdateDeadline_ImmediateHungUpdateEnds(t *testing.T) {
	checker := newHungUpdateChecker(t, hungFinding())
	svc := newApplyFixture(t, checker)
	svc.applyTimeout = 200 * time.Millisecond
	lock := NewOperationLock()
	svc.SetOperationLock(lock)
	seedContainerPolicy(t, svc, "c1")

	runUntilGuard(t, "runCycle", func() { svc.runCycle(context.Background()) })

	checker.mu.Lock()
	hadDeadline := checker.hadDeadline
	checker.mu.Unlock()
	assert.True(t, hadDeadline, "UpdateContainer must run under a deadline")

	row := onlyHistoryRow(t, svc)
	assert.Equal(t, "failed", row.Status)
	require.NotNil(t, row.ErrorMessage)
	assert.Equal(t, "auto-update timed out after 200ms: context deadline exceeded", *row.ErrorMessage)

	requireStackFree(t, lock)
}

// TestAutoUpdateDeadline_ScheduledHungUpdateEnds is the scheduled path on the same
// instrument: it was already bounded, and must stay bounded by the same value.
func TestAutoUpdateDeadline_ScheduledHungUpdateEnds(t *testing.T) {
	checker := newHungUpdateChecker(t, hungFinding())
	svc := newApplyFixture(t, checker)
	svc.applyTimeout = 200 * time.Millisecond
	lock := NewOperationLock()
	svc.SetOperationLock(lock)
	seedContainerPolicy(t, svc, "c1")
	require.NoError(t, svc.db.SetCachedUpdates([]models.CachedUpdate{{
		ContainerID: "c1", ContainerName: "web", ImageRef: "nginx:latest", StackID: "s1",
		ScannedAt: time.Now().Format(time.RFC3339),
	}}))

	runUntilGuard(t, "applyNow", func() {
		assert.True(t, svc.applyNow(context.Background()), "the apply must run, not defer")
	})

	row := onlyHistoryRow(t, svc)
	assert.Equal(t, "failed", row.Status)
	require.NotNil(t, row.ErrorMessage)
	assert.Equal(t, "auto-update timed out after 200ms: context deadline exceeded", *row.ErrorMessage)

	requireStackFree(t, lock)
}

// TestAutoUpdateDeadline_ParentCancelIsNotATimeout pins the other side:
// Stop() cancelling the scheduler's context is a shutdown, and the history row
// must not call it a timeout.
func TestAutoUpdateDeadline_ParentCancelIsNotATimeout(t *testing.T) {
	checker := newHungUpdateChecker(t, hungFinding())
	svc := newApplyFixture(t, checker)
	lock := NewOperationLock()
	svc.SetOperationLock(lock)
	seedContainerPolicy(t, svc, "c1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-checker.entered:
			cancel()
		case <-checker.release:
		}
	}()

	runUntilGuard(t, "runCycle", func() { svc.runCycle(ctx) })

	row := onlyHistoryRow(t, svc)
	assert.Equal(t, "failed", row.Status)
	require.NotNil(t, row.ErrorMessage)
	assert.Equal(t, "context canceled", *row.ErrorMessage)

	requireStackFree(t, lock)
}
