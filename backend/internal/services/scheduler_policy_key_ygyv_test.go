package services

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-ygyv. A container's auto-update policy was keyed on the Docker
// container ID. Every update and every compose redeploy recreates the
// container under a new ID, so after the first update the policy matched
// nothing and auto-update stopped for that container with no message. Policies
// are now keyed on the container's name, and rows written before that are
// re-keyed by the scheduler, which can ask Docker for a live container's name.

// ygyvID builds a full 64-hex container ID, the shape Docker gives.
func ygyvID(seed string) string {
	return strings.Repeat(seed, 64/len(seed))
}

var (
	ygyvOldID = ygyvID("a1")
	ygyvNewID = ygyvID("b2")
)

// ygyvDocker answers inspects from a fixed table of live containers (ID ->
// name) and records every container UpdateContainer is asked to act on.
type ygyvDocker struct {
	mu         sync.Mutex
	live       map[string]string
	inspectErr error
	updated    []string
}

func (d *ygyvDocker) CheckForUpdates(context.Context, DashboardDB) ([]models.ContainerUpdateInfo, error) {
	return nil, nil
}

func (d *ygyvDocker) UpdateContainer(_ context.Context, containerID string, _ DashboardDB) (models.UpdateResult, truth.ActionResult) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.updated = append(d.updated, containerID)
	return models.UpdateResult{}, truth.Success("image advanced")
}

func (d *ygyvDocker) InspectContainer(_ context.Context, id string) (container.InspectResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.inspectErr != nil {
		return container.InspectResponse{}, d.inspectErr
	}
	name, ok := d.live[id]
	if !ok {
		return container.InspectResponse{}, notFoundError{msg: "Error: No such container: " + id}
	}
	return container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: id, Name: "/" + name}}, nil
}

func (d *ygyvDocker) updatedIDs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.updated...)
}

// seedYgyvPolicy stores a container policy with the given target, as the
// Updates tab saved it: before the fix, a container ID; after, a name.
func seedYgyvPolicy(t *testing.T, svc *SchedulerService, id, target string, failures int) {
	t.Helper()
	now := time.Now().Format(time.RFC3339)
	require.NoError(t, svc.db.UpsertAutoUpdatePolicy(&models.AutoUpdatePolicy{
		ID: id, TargetType: "container", TargetID: target, Enabled: true,
		ConsecutiveFailures: failures, CreatedAt: now, UpdatedAt: now,
	}))
}

func ygyvPolicies(t *testing.T, svc *SchedulerService) map[string]models.AutoUpdatePolicy {
	t.Helper()
	all, err := svc.db.GetAutoUpdatePolicies()
	require.NoError(t, err)
	out := map[string]models.AutoUpdatePolicy{}
	for _, p := range all {
		out[p.TargetID] = p
	}
	return out
}

// TestContainerPolicySurvivesRecreateYgyv: a container with a policy is
// updated, which recreates it under a new ID, and the next pass still finds
// its policy.
func TestContainerPolicySurvivesRecreateYgyv(t *testing.T) {
	t.Run("policy saved by the Updates tab, keyed on the name", func(t *testing.T) {
		docker := &ygyvDocker{live: map[string]string{ygyvNewID: "web"}}
		svc := newApplyFixture(t, docker)
		seedYgyvPolicy(t, svc, "p-web", "web", 0)

		svc.RunAutoUpdates(context.Background(), []models.CachedUpdate{{ContainerID: ygyvNewID, ContainerName: "web", ImageRef: "nginx:latest"}})

		require.Equal(t, []string{ygyvNewID}, docker.updatedIDs(), "the recreated container's update must find its policy")
	})

	t.Run("policy saved before the fix, keyed on the old container ID", func(t *testing.T) {
		docker := &ygyvDocker{live: map[string]string{ygyvOldID: "web"}}
		svc := newApplyFixture(t, docker)
		seedYgyvPolicy(t, svc, "p-web", ygyvOldID, 0)

		// First pass: the container still has the ID the policy was saved with.
		svc.RunAutoUpdates(context.Background(), []models.CachedUpdate{{ContainerID: ygyvOldID, ContainerName: "web", ImageRef: "nginx:latest"}})
		require.Equal(t, []string{ygyvOldID}, docker.updatedIDs())

		// The update recreated it: the old ID is gone, a new one is live.
		docker.mu.Lock()
		docker.live = map[string]string{ygyvNewID: "web"}
		docker.mu.Unlock()

		svc.RunAutoUpdates(context.Background(), []models.CachedUpdate{{ContainerID: ygyvNewID, ContainerName: "web", ImageRef: "nginx:latest"}})
		require.Equal(t, []string{ygyvOldID, ygyvNewID}, docker.updatedIDs(), "the second pass must still find the policy")
	})
}

// TestLegacyContainerPolicyIsRekeyedYgyv: a row keyed on a container ID is
// moved to the container's name, keeping its id and state, from Docker for a
// live container and from update_history for one an update replaced. A name
// that already has a policy keeps it. A row that resolves to no name, and every
// row while Docker answers with an error other than not-found, is left alone.
// Running it again changes nothing.
func TestLegacyContainerPolicyIsRekeyedYgyv(t *testing.T) {
	orphanID := ygyvID("c3")
	unknownID := ygyvID("d4")
	takenID := ygyvID("e5")
	clashLiveID := ygyvID("f6")
	clashOrphanID := ygyvID("07")

	docker := &ygyvDocker{live: map[string]string{
		ygyvOldID:   "web",
		takenID:     "db",
		clashLiveID: "cache",
	}}
	svc := newApplyFixture(t, docker)

	seedYgyvPolicy(t, svc, "p-live", ygyvOldID, 2)
	seedYgyvPolicy(t, svc, "p-orphan", orphanID, 1)
	seedYgyvPolicy(t, svc, "p-unknown", unknownID, 0)
	seedYgyvPolicy(t, svc, "p-db-new", "db", 0) // saved by name after the fix
	seedYgyvPolicy(t, svc, "p-db-old", takenID, 1)
	// An orphan and the live container's row resolve to the same name: the
	// orphan is seeded first, so only the live-first order keeps the live row.
	seedYgyvPolicy(t, svc, "p-cache-orphan", clashOrphanID, 2)
	seedYgyvPolicy(t, svc, "p-cache-live", clashLiveID, 0)
	for id, name := range map[string]string{orphanID: "api", clashOrphanID: "cache"} {
		require.NoError(t, svc.db.InsertUpdateHistory(&models.UpdateHistoryEntry{
			ID: "h-" + name, ContainerID: id, ContainerName: name, Image: "x:latest",
			Status: "success", Trigger: "auto", StartedAt: time.Now().Format(time.RFC3339),
		}))
	}

	want := map[string]struct {
		id       string
		failures int
	}{
		"web":     {"p-live", 2},
		"api":     {"p-orphan", 1},
		unknownID: {"p-unknown", 0},
		"db":      {"p-db-new", 0},
		"cache":   {"p-cache-live", 0},
	}
	check := func() {
		t.Helper()
		got := ygyvPolicies(t, svc)
		require.Len(t, got, len(want), "policies after the re-key: %v", got)
		for target, w := range want {
			p, ok := got[target]
			require.True(t, ok, "no policy for %s: %v", target, got)
			require.Equal(t, w.id, p.ID, "policy for %s", target)
			require.Equal(t, w.failures, p.ConsecutiveFailures, "policy for %s", target)
			require.True(t, p.Enabled)
		}
	}

	svc.RunAutoUpdates(context.Background(), nil)
	check()
	svc.RunAutoUpdates(context.Background(), nil)
	check()
}

// TestLegacyContainerPolicyWaitsForDockerYgyv: while Docker cannot be asked,
// nothing is re-keyed, not even rows update_history could name, because the
// live row of a name must win and liveness is unknown.
func TestLegacyContainerPolicyWaitsForDockerYgyv(t *testing.T) {
	docker := &ygyvDocker{inspectErr: errors.New("Cannot connect to the Docker daemon")}
	svc := newApplyFixture(t, docker)
	seedYgyvPolicy(t, svc, "p-old", ygyvOldID, 0)
	require.NoError(t, svc.db.InsertUpdateHistory(&models.UpdateHistoryEntry{
		ID: "h1", ContainerID: ygyvOldID, ContainerName: "web", Image: "x:latest",
		Status: "success", Trigger: "auto", StartedAt: time.Now().Format(time.RFC3339),
	}))

	svc.RunAutoUpdates(context.Background(), nil)

	_, err := svc.db.GetAutoUpdatePolicy("container", ygyvOldID)
	require.NoError(t, err, "the row stays on its ID until Docker answers")
	_, err = svc.db.GetAutoUpdatePolicy("container", "web")
	require.True(t, errors.Is(err, errdefs.ErrNotFound))

	// The other side, same fixture: Docker answers, the row moves.
	docker.mu.Lock()
	docker.inspectErr = nil
	docker.mu.Unlock()
	svc.RunAutoUpdates(context.Background(), nil)
	_, err = svc.db.GetAutoUpdatePolicy("container", "web")
	require.NoError(t, err)
}

// TestLegacyContainerPolicyRekeyedOnScanTickYgyv: the re-key also runs on a
// scan tick with auto-update switched off, because the screens read policies
// by name whether or not anything is applied.
func TestLegacyContainerPolicyRekeyedOnScanTickYgyv(t *testing.T) {
	docker := &ygyvDocker{live: map[string]string{ygyvOldID: "web"}}
	svc := newTestScheduler(t, docker)
	require.NoError(t, svc.db.SetSetting("auto_update_enabled", "false"))
	seedYgyvPolicy(t, svc, "p-old", ygyvOldID, 0)

	svc.runCycle(context.Background())

	p, err := svc.db.GetAutoUpdatePolicy("container", "web")
	require.NoError(t, err)
	require.Equal(t, "p-old", p.ID)
}
