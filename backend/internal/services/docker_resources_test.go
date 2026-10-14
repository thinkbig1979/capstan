package services

import (
	"testing"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"
)

func TestListImagesContainerCount(t *testing.T) {
	// ImageList always reports Containers -1: the client cannot ask for counts.
	listed := []image.Summary{
		{ID: "sha256:used", RepoTags: []string{"nginx:latest"}, Containers: -1},
		{ID: "sha256:idle", RepoTags: []string{"redis:7"}, Containers: -1},
		{ID: "sha256:new", Containers: -1},
	}

	t.Run("counts come from the DiskUsage snapshot keyed by ID", func(t *testing.T) {
		du := &dockertypes.DiskUsage{Images: []*image.Summary{
			{ID: "sha256:used", Containers: 2},
			{ID: "sha256:idle", Containers: 0},
			nil,
		}}
		got := toDockerImages(listed, du)
		if len(got) != 3 {
			t.Fatalf("len = %d, want 3", len(got))
		}
		if got[0].Containers != 2 || !got[0].ContainersKnown {
			t.Errorf("used image: Containers=%d known=%v, want 2 and true", got[0].Containers, got[0].ContainersKnown)
		}
		if got[1].Containers != 0 || !got[1].ContainersKnown {
			t.Errorf("idle image: Containers=%d known=%v, want 0 and true", got[1].Containers, got[1].ContainersKnown)
		}
		// Created between the two calls: absent from the snapshot, so unknown.
		if got[2].Containers != 0 || got[2].ContainersKnown {
			t.Errorf("image missing from snapshot: Containers=%d known=%v, want 0 and false", got[2].Containers, got[2].ContainersKnown)
		}
		if got[2].RepoTags[0] != "<none>" {
			t.Errorf("untagged image RepoTags = %v, want [<none>]", got[2].RepoTags)
		}
	})

	t.Run("a DiskUsage failure gives containersKnown false, not 0", func(t *testing.T) {
		got := toDockerImages(listed, nil)
		for _, img := range got {
			if img.ContainersKnown {
				t.Errorf("%s: ContainersKnown = true with no DiskUsage snapshot", img.ID)
			}
			if img.Containers != 0 {
				t.Errorf("%s: Containers = %d, want 0 (never the -1 from ImageList)", img.ID, img.Containers)
			}
		}
	})
}
