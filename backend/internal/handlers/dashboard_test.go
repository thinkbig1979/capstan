package handlers

import (
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/stretchr/testify/assert"
)

// ctr is a small helper for building container snapshots in tests.
func ctr(project, state string) models.DashboardContainerInfo {
	return models.DashboardContainerInfo{ProjectName: project, State: state}
}

// TestCountLiveStackStatuses locks in the FLAG 1 fix: stack running/stopped is
// derived from the already-fetched container snapshot (matched by compose
// project), not from a per-stack `docker compose ps` or the stale DB status.
//
// The decisive case is two stacks sharing a project name ("docker"): because
// Docker namespaces compose projects by name, both must count against that
// project's live state — reproducing the Stacks view's count (running 3) without
// any subprocess. running + stopped must always equal the total stack count.
func TestCountLiveStackStatuses(t *testing.T) {
	stacks := []models.Stack{
		{ProjectName: "docker"},    // shares project with the next stack
		{ProjectName: "docker"},    // same project -> both count running
		{ProjectName: "ptnextjs"},  // its own running project
		{ProjectName: "idle"},      // has only a stopped container
		{ProjectName: "gone"},      // no containers at all
		{ProjectName: ""},          // unregistered project -> never running
	}
	containers := []models.DashboardContainerInfo{
		ctr("docker", "running"),
		ctr("docker", "exited"), // a second, stopped container in the same project
		ctr("ptnextjs", "running"),
		ctr("idle", "exited"),
		ctr("", "running"), // no project label -> ignored
	}

	running, stopped := countLiveStackStatuses(stacks, containers)

	assert.Equal(t, 3, running, "both 'docker' stacks + ptnextjs are running")
	assert.Equal(t, 3, stopped, "idle, gone, and the empty-project stack are not running")
	assert.Equal(t, len(stacks), running+stopped, "every stack falls into exactly one bucket")
}

func TestCountLiveStackStatuses_NoContainers(t *testing.T) {
	stacks := []models.Stack{{ProjectName: "a"}, {ProjectName: "b"}}
	running, stopped := countLiveStackStatuses(stacks, nil)
	assert.Equal(t, 0, running)
	assert.Equal(t, 2, stopped)
}

func TestCountLiveStackStatuses_Empty(t *testing.T) {
	running, stopped := countLiveStackStatuses(nil, nil)
	assert.Equal(t, 0, running)
	assert.Equal(t, 0, stopped)
}
