package services

import "github.com/docker/docker/api/types"

// summarizeDiskUsage is the one place Docker's disk usage is summed into the
// per-category figures the UI shows. It is pure so the arithmetic can be tested
// without a daemon (DockerService.client is a concrete client with no fake).
//
// Images use LayersSize: summing each image's Size counts every shared layer
// once per image that uses it, and `docker system df` reports LayersSize.
// Docker reports a volume size it could not compute as -1, or leaves
// UsageData nil; adding those would understate the total (and a negative sum
// renders as "0 B"), so they are skipped and VolumesSizeKnown says so.
func summarizeDiskUsage(du types.DiskUsage) DiskUsageBreakdown {
	out := DiskUsageBreakdown{
		Images:           du.LayersSize,
		VolumesSizeKnown: true,
	}
	for _, ctr := range du.Containers {
		if ctr != nil && ctr.SizeRw > 0 {
			out.Containers += ctr.SizeRw
		}
	}
	for _, vol := range du.Volumes {
		if vol == nil || vol.UsageData == nil || vol.UsageData.Size < 0 {
			out.VolumesSizeKnown = false
			continue
		}
		out.Volumes += vol.UsageData.Size
	}
	for _, bc := range du.BuildCache {
		if bc != nil {
			out.BuildCache += bc.Size
		}
	}
	out.Total = out.Images + out.Containers + out.Volumes + out.BuildCache
	return out
}
