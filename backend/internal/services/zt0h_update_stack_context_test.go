package services

import (
	"context"
	"errors"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-zt0h. The Updates tab reads cached_updates, never the live scan, so
// the stack lookup flag and compose's location labels only reach it if the
// whole pipeline carries them: CheckForUpdates -> performScan ->
// SetCachedUpdates -> GetCachedUpdates. These arms drive that pipeline for
// real (a SchedulerService over a real DockerService and a migrated database)
// and read the cache back, so a layer that drops a field fails here.

const (
	zt0hRef    = "registry.test/zt0h/app:1"
	zt0hDir    = "/home/op/zt0h/docker"
	zt0hFiles  = "/home/op/zt0h/docker/docker-compose.a.yml,/home/op/zt0h/docker/docker-compose.b.yml"
	zt0hMgdDir = "/opt/stacks/known"
)

// zt0hScanFake is qyg72ListFake plus an ImageInspect that answers, so
// CheckForUpdates gets past its image read. Everything past that still records
// itself in beyond.
type zt0hScanFake struct{ *qyg72ListFake }

func (zt0hScanFake) ImageInspect(_ context.Context, _ string, _ ...dockerclient.ImageInspectOption) (image.InspectResponse, error) {
	return image.InspectResponse{
		RepoTags:    []string{zt0hRef},
		RepoDigests: []string{"registry.test/zt0h/app@sha256:local"},
	}, nil
}

func zt0hContainers() []container.Summary {
	compose := func(id, project, dir, files string) container.Summary {
		return container.Summary{
			ID: id, Names: []string{"/" + id}, ImageID: "img-" + id, State: "running",
			Labels: map[string]string{
				"com.docker.compose.project":              project,
				"com.docker.compose.service":              "web",
				"com.docker.compose.project.working_dir":  dir,
				"com.docker.compose.project.config_files": files,
			},
		}
	}
	return []container.Summary{
		compose("unmanaged", g482ProjectUnknown, zt0hDir, zt0hFiles),
		compose("managed", g482ProjectKnown, zt0hMgdDir, zt0hMgdDir+"/compose.yaml"),
		{ID: "plain", Names: []string{"/plain"}, ImageID: "img-plain", State: "running", Labels: map[string]string{}},
	}
}

// zt0hFailingLookup is a stacks table that cannot be read.
type zt0hFailingLookup struct{}

func (zt0hFailingLookup) GetStackByProjectName(string) (*models.Stack, error) {
	return nil, errors.New("zt0h: database is locked")
}

// zt0hFailingChecker is the real DockerService scan, handed an unreadable
// stacks table instead of the scheduler's db. The cache writes still go to
// the scheduler's real db.
type zt0hFailingChecker struct{ *DockerService }

func (c zt0hFailingChecker) CheckForUpdates(ctx context.Context, _ DashboardDB) ([]models.ContainerUpdateInfo, error) {
	return c.DockerService.CheckForUpdates(ctx, zt0hFailingLookup{})
}

type zt0hRow struct {
	stackID     string
	failed      bool
	workingDir  string
	configFiles string
}

func zt0hScanIntoCache(t *testing.T, failLookup bool) map[string]zt0hRow {
	t.Helper()
	evStubDigest(t, func(context.Context, string) (string, error) { return "sha256:remote", nil })
	fake := &qyg72ListFake{containers: zt0hContainers()}
	docker := &DockerService{updateClient: zt0hScanFake{fake}}
	db := g482HealthyDB(t)

	var checker updateChecker = docker
	if failLookup {
		checker = zt0hFailingChecker{docker}
	}
	if _, err := NewSchedulerService(checker, db, nil, nil).RunScan(context.Background()); err != nil {
		t.Fatalf("RunScan: %v", err)
	}
	if len(fake.beyond) != 0 {
		t.Fatalf("the scan called past its image read: %v", fake.beyond)
	}
	return zt0hReadCache(t, db)
}

func zt0hReadCache(t *testing.T, db *database.DB) map[string]zt0hRow {
	t.Helper()
	cached, err := db.GetCachedUpdates()
	if err != nil {
		t.Fatalf("GetCachedUpdates: %v", err)
	}
	rows := make(map[string]zt0hRow, len(cached))
	for _, cu := range cached {
		rows[cu.ContainerID] = zt0hRow{cu.StackID, cu.StackLookupFailed, cu.ComposeWorkingDir, cu.ComposeConfigFiles}
	}
	if len(rows) != 3 {
		t.Fatalf("premise: every container must reach the cache as an update, got %+v", rows)
	}
	return rows
}

func TestUpdateScan_CacheCarriesStackLookupAndComposeLocation(t *testing.T) {
	tests := []struct {
		name       string
		failLookup bool
		want       map[string]zt0hRow
	}{
		{
			name: "lookup succeeded",
			want: map[string]zt0hRow{
				// config_files keeps its comma: stored verbatim, never split.
				"unmanaged": {"", false, zt0hDir, zt0hFiles},
				"managed":   {g482StackID, false, zt0hMgdDir, zt0hMgdDir + "/compose.yaml"},
				"plain":     {"", false, "", ""},
			},
		},
		{
			// The must-not side: the same empty stackId as "unmanaged", but it
			// has to reach the cache as a failure, or the Updates tab calls a
			// managed stack "not managed by Capstan". A plain container never
			// looks anything up, so it stays false.
			name:       "lookup failed",
			failLookup: true,
			want: map[string]zt0hRow{
				"unmanaged": {"", true, zt0hDir, zt0hFiles},
				"managed":   {"", true, zt0hMgdDir, zt0hMgdDir + "/compose.yaml"},
				"plain":     {"", false, "", ""},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := zt0hScanIntoCache(t, tt.failLookup)
			for id, want := range tt.want {
				if got := rows[id]; got != want {
					t.Errorf("%s: got %+v, want %+v", id, got, want)
				}
			}
		})
	}
}
