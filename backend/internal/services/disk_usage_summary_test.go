package services

import (
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/volume"
)

func TestSummarizeDiskUsage(t *testing.T) {
	images := []*image.Summary{{ID: "a", Size: 300}, {ID: "b", Size: 250}}
	containers := []*container.Summary{{SizeRw: 10}, {SizeRw: 0}}
	cache := []*build.CacheRecord{{Size: 7}, {Size: 3}}

	t.Run("nil UsageData does not panic and marks volumes unknown", func(t *testing.T) {
		got := summarizeDiskUsage(types.DiskUsage{
			Volumes: []*volume.Volume{
				{Name: "known", UsageData: &volume.UsageData{Size: 40}},
				{Name: "no-usage"},
				nil,
			},
		})
		if got.VolumesSizeKnown {
			t.Errorf("VolumesSizeKnown = true, want false for a volume with nil UsageData")
		}
		if got.Volumes != 40 {
			t.Errorf("Volumes = %d, want 40 (only the known volume)", got.Volumes)
		}
	})

	t.Run("a -1 volume size is excluded and marks volumes unknown", func(t *testing.T) {
		got := summarizeDiskUsage(types.DiskUsage{
			Volumes: []*volume.Volume{
				{Name: "known", UsageData: &volume.UsageData{Size: 40}},
				{Name: "unavailable", UsageData: &volume.UsageData{Size: -1, RefCount: -1}},
			},
		})
		if got.VolumesSizeKnown {
			t.Errorf("VolumesSizeKnown = true, want false for a Size -1 volume")
		}
		if got.Volumes != 40 {
			t.Errorf("Volumes = %d, want 40 (the -1 is not subtracted)", got.Volumes)
		}
	})

	t.Run("all sizes known gives the exact sums", func(t *testing.T) {
		got := summarizeDiskUsage(types.DiskUsage{
			LayersSize: 500,
			Images:     images,
			Containers: containers,
			Volumes: []*volume.Volume{
				{Name: "v1", UsageData: &volume.UsageData{Size: 40}},
				{Name: "v2", UsageData: &volume.UsageData{Size: 0}},
			},
			BuildCache: cache,
		})
		want := DiskUsageBreakdown{
			Images:              500,
			Containers:          10,
			Volumes:             40,
			BuildCache:          10,
			Total:               560,
			VolumesSizeKnown:    true,
			ContainersSizeKnown: true,
		}
		if got != want {
			t.Errorf("summarizeDiskUsage = %+v, want %+v", got, want)
		}
	})

	// agent-os-rwik: Docker reports a writable layer it could not measure as
	// SizeRw -1. Skipped silently, Containers read as exact while it was a floor.
	t.Run("a -1 container size is excluded and marks containers unknown", func(t *testing.T) {
		got := summarizeDiskUsage(types.DiskUsage{
			Containers: []*container.Summary{{SizeRw: 10}, {SizeRw: -1}, nil},
		})
		if got.ContainersSizeKnown {
			t.Errorf("ContainersSizeKnown = true, want false for a SizeRw -1 container")
		}
		if got.Containers != 10 || got.Total != 10 {
			t.Errorf("Containers=%d Total=%d, want 10 and 10 (the -1 is not subtracted)", got.Containers, got.Total)
		}
	})

	t.Run("no containers at all is a known zero", func(t *testing.T) {
		got := summarizeDiskUsage(types.DiskUsage{})
		if !got.ContainersSizeKnown || got.Containers != 0 {
			t.Errorf("got Containers=%d ContainersSizeKnown=%v, want 0 and true", got.Containers, got.ContainersSizeKnown)
		}
	})

	t.Run("no volumes at all is a known zero", func(t *testing.T) {
		got := summarizeDiskUsage(types.DiskUsage{})
		if !got.VolumesSizeKnown || got.Volumes != 0 {
			t.Errorf("got Volumes=%d VolumesSizeKnown=%v, want 0 and true", got.Volumes, got.VolumesSizeKnown)
		}
	})

	t.Run("images use LayersSize, not the per-image sum", func(t *testing.T) {
		// 300+250 counts the layers the two images share twice; Docker's
		// LayersSize counts each layer once, which is what `docker system df` shows.
		got := summarizeDiskUsage(types.DiskUsage{LayersSize: 500, Images: images})
		if got.Images != 500 {
			t.Errorf("Images = %d, want LayersSize 500, not the per-image sum 550", got.Images)
		}
	})
}
