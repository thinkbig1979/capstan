package services

import (
	"context"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-dn1z (Edwin, 2026-11-15). A one-off container, made by
// `docker compose run`, carries com.docker.compose.oneoff=True and the same
// project and service labels as its service's container. It is hidden from the
// Updates list, and an update aimed at one is refused: the compose path would
// recreate the SERVICE container, not the one-off.

func dn1zLabels(oneOff string) map[string]string {
	return map[string]string{
		"com.docker.compose.project": g482ProjectKnown,
		"com.docker.compose.service": "web",
		"com.docker.compose.oneoff":  oneOff,
	}
}

// dn1zListFake is bx43FakeDocker with a ContainerList that answers.
type dn1zListFake struct {
	*bx43FakeDocker
	list []container.Summary
}

func (f *dn1zListFake) ContainerList(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
	f.record("ContainerList")
	return f.list, nil
}

func TestCheckForUpdates_HidesOneOff_Dn1z(t *testing.T) {
	orig := truth.RemoteRegistryDigest
	truth.RemoteRegistryDigest = func(context.Context, string) (string, error) {
		return "sha256:1111111111111111111111111111111111111111111111111111111111111111", nil
	}
	t.Cleanup(func() { truth.RemoteRegistryDigest = orig })

	fake := &dn1zListFake{
		bx43FakeDocker: &bx43FakeDocker{},
		list: []container.Summary{
			{ID: "svc", Names: []string{"/proj-web-1"}, ImageID: bx43ImageID, State: "running", Labels: dn1zLabels("False")},
			{ID: "oneoff", Names: []string{"/proj-web-run-abc"}, ImageID: bx43ImageID, State: "running", Labels: dn1zLabels("True")},
		},
	}
	svc := &DockerService{updateClient: fake}

	updates, err := svc.CheckForUpdates(context.Background(), nil)
	if err != nil {
		t.Fatalf("CheckForUpdates: %v", err)
	}
	var ids []string
	for _, u := range updates {
		ids = append(ids, u.ContainerID)
	}
	// Positive side on the same instrument: the service container IS listed,
	// so an empty result cannot pass for "hidden".
	if len(ids) != 1 || ids[0] != "svc" {
		t.Fatalf("update candidates = %v, want only the service container [svc]: a one-off from compose run must not be offered as an update", ids)
	}
}

func TestUpdateContainer_RefusesOneOff_Dn1z(t *testing.T) {
	run := map[string]func(svc *DockerService) truth.ActionResult{
		"UpdateContainer": func(svc *DockerService) truth.ActionResult {
			_, ar := svc.UpdateContainer(context.Background(), bx43ContainerID, nil)
			return ar
		},
		"UpdateContainerStreaming": func(svc *DockerService) truth.ActionResult {
			_, ar := svc.UpdateContainerStreaming(context.Background(), bx43ContainerID, nil,
				func(LogLine) {}, func(Status) {})
			return ar
		},
	}
	for name, call := range run {
		t.Run(name, func(t *testing.T) {
			svc, fake := bx43Service(dn1zLabels("True"))

			ar := call(svc)

			if ar.Outcome != truth.OutcomeFailed || !strings.Contains(ar.Reason, "one-off") {
				t.Errorf("result = %q %q, want failed with a one-off reason", ar.Outcome, ar.Reason)
			}
			if !startedNothing(ar) {
				t.Errorf("startedNothing = false: a refused one-off update must not store a stack status")
			}
			if applied := fake.applyCalls(); len(applied) != 0 {
				t.Errorf("apply calls for a one-off: %v", applied)
			}
		})
	}
}

// Part 3: one-offs are shown separately under their stack and do not count
// toward its status.
func TestBuildStackStatuses_OneOffsSeparate_Dn1z(t *testing.T) {
	snapshot := []models.DashboardContainerInfo{
		{ProjectName: "web", ID: "w1", Name: "web-app-1", State: "running"},
		{ProjectName: "web", ID: "w2", Name: "web-app-run-1", State: "exited", OneOff: true},
		{ProjectName: "jobs", ID: "j1", Name: "jobs-task-run-1", State: "running", OneOff: true},
	}
	got := BuildStackStatuses(snapshot)

	require.Contains(t, got, "web")
	assert.Equal(t, "running", got["web"].Status, "an exited one-off must not make a running stack partial")
	assert.Equal(t, []string{"web-app-1"}, containerNames(got["web"].Containers))
	assert.Equal(t, []string{"web-app-run-1"}, containerNames(got["web"].OneOffs))

	require.Contains(t, got, "jobs")
	assert.Equal(t, "stopped", got["jobs"].Status, "a project with only one-offs has no service container running")
	assert.NotNil(t, got["jobs"].Containers)
	assert.Empty(t, got["jobs"].Containers)
	assert.Equal(t, []string{"jobs-task-run-1"}, containerNames(got["jobs"].OneOffs))
}

func TestGetAllContainersWithDetails_MarksOneOff_Dn1z(t *testing.T) {
	fake := &dn1zListFake{
		bx43FakeDocker: &bx43FakeDocker{},
		list: []container.Summary{
			{ID: "svc", Names: []string{"/proj-web-1"}, State: "exited", Labels: dn1zLabels("False")},
			{ID: "oneoff", Names: []string{"/proj-web-run-abc"}, State: "exited", Labels: dn1zLabels("True")},
		},
	}
	svc := &DockerService{updateClient: fake}
	rows, err := svc.GetAllContainersWithDetails(context.Background(), nil)
	require.NoError(t, err)
	oneOff := map[string]bool{}
	for _, r := range rows {
		oneOff[r.ID] = r.OneOff
	}
	assert.Equal(t, map[string]bool{"svc": false, "oneoff": true}, oneOff)
}
