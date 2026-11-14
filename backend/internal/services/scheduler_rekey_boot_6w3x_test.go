package services

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
)

// agent-os-6w3x: with the update scan off the scheduler never starts, and
// nothing else re-keyed container-ID policies. Boot now runs the re-key on its
// own and retries it until it completes. Named _ygyv too, so the bead's
// `go test ./internal/services -run ygyv` selects these.

// countingInspector counts InspectContainer calls and can fail them.
type countingInspector struct {
	*ygyvDocker
	mu    sync.Mutex
	calls int
	fail  error
}

func (c *countingInspector) InspectContainer(ctx context.Context, id string) (container.InspectResponse, error) {
	c.mu.Lock()
	c.calls++
	fail := c.fail
	c.mu.Unlock()
	if fail != nil {
		return container.InspectResponse{}, fail
	}
	return c.ygyvDocker.InspectContainer(ctx, id)
}

func (c *countingInspector) setFail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fail = err
}

func (c *countingInspector) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

var errDockerDown6w3x = errors.New("docker daemon unreachable (test sentinel)")

func requireKeyedByName6w3x(t *testing.T, svc *SchedulerService) {
	t.Helper()
	p, err := svc.db.GetAutoUpdatePolicy("container", "web")
	require.NoError(t, err)
	require.NotNil(t, p, "the legacy row must be re-keyed to the container name: %v", ygyvPolicies(t, svc))
	require.Equal(t, "p-old", p.ID)
}

func TestBootRekeyWithScanOff_6w3x_ygyv(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := newTestScheduler(t, &ygyvDocker{live: map[string]string{ygyvOldID: "web"}})
		seedYgyvPolicy(t, svc, "p-old", ygyvOldID, 0)

		svc.RekeyLegacyContainerPoliciesInBackground() // never Start: scanning is off
		synctest.Wait()

		requireKeyedByName6w3x(t, svc)
		svc.Close()
	})
}

func TestBootRekeyRetriesAfterDockerError_6w3x_ygyv(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		docker := &countingInspector{ygyvDocker: &ygyvDocker{live: map[string]string{ygyvOldID: "web"}}, fail: errDockerDown6w3x}
		svc := newTestScheduler(t, docker)
		seedYgyvPolicy(t, svc, "p-old", ygyvOldID, 0)

		svc.RekeyLegacyContainerPoliciesInBackground()
		synctest.Wait()
		require.Equal(t, 1, docker.callCount())
		require.Contains(t, ygyvPolicies(t, svc), ygyvOldID, "a failed run writes nothing")

		docker.setFail(nil)
		time.Sleep(rekeyRetryFirst)
		synctest.Wait()

		require.Equal(t, 2, docker.callCount(), "one retry after the first wait")
		requireKeyedByName6w3x(t, svc)
		svc.Close()
	})
}

// TestBootRekeyRetryBacksOffAndStopsOnClose_6w3x_ygyv: while Docker stays
// down the waits double up to the cap, and Close ends the retries.
func TestBootRekeyRetryBacksOffAndStopsOnClose_6w3x_ygyv(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		docker := &countingInspector{ygyvDocker: &ygyvDocker{live: map[string]string{ygyvOldID: "web"}}, fail: errDockerDown6w3x}
		svc := newTestScheduler(t, docker)
		seedYgyvPolicy(t, svc, "p-old", ygyvOldID, 0)

		svc.RekeyLegacyContainerPoliciesInBackground()
		synctest.Wait()
		// Waits: 30s, 1m, 2m, 4m, 8m, then 10m each. After 30s+1m+2m+4m+8m
		// the 6th try has run; the 7th comes 10m later, not 16m.
		time.Sleep(30*time.Second + time.Minute + 2*time.Minute + 4*time.Minute + 8*time.Minute)
		synctest.Wait()
		require.Equal(t, 6, docker.callCount())
		time.Sleep(rekeyRetryMax)
		synctest.Wait()
		require.Equal(t, 7, docker.callCount(), "the wait is capped at rekeyRetryMax")

		svc.Close()
		time.Sleep(time.Hour)
		synctest.Wait()
		require.Equal(t, 7, docker.callCount(), "no try after Close")
	})
}

// TestBootRekeyAfterCloseStartsNothing_6w3x_ygyv: a closed scheduler starts
// no re-key.
func TestBootRekeyAfterCloseStartsNothing_6w3x_ygyv(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		docker := &countingInspector{ygyvDocker: &ygyvDocker{live: map[string]string{ygyvOldID: "web"}}}
		svc := newTestScheduler(t, docker)
		seedYgyvPolicy(t, svc, "p-old", ygyvOldID, 0)

		svc.Close()
		svc.RekeyLegacyContainerPoliciesInBackground()
		synctest.Wait()

		require.Equal(t, 0, docker.callCount())
	})
}
