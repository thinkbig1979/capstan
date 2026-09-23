package services

// This file holds the services package's structs that handlers serve as a
// response body unchanged, and nothing else.
// backend/tygo.yaml generates frontend/src/types/generated-services.ts from
// this file alone (include_files), because the rest of the package is full of
// service structs, jobs and internal records that are not a JSON contract.
// tygo v0.2.21 filters by file, not by type (tygo/config.go, IsFileIgnored),
// so a struct that is served as-is belongs here and anything else does not.

// DiskUsageBreakdown is the per-category Docker disk usage served inside
// GET /dashboard/stats's diskUsage field.
type DiskUsageBreakdown struct {
	Images     int64 `json:"images"`
	Containers int64 `json:"containers"`
	Volumes    int64 `json:"volumes"`
	BuildCache int64 `json:"buildCache"`
	Total      int64 `json:"total"`
}

// DockerCleanupCandidate is one image a run would remove. Repository is empty
// for the fully-untagged form; see danglingRepository.
type DockerCleanupCandidate struct {
	ID         string `json:"id"`
	Repository string `json:"repository,omitempty"`
	Size       int64  `json:"size"`
	Created    int64  `json:"created"`
}

// DockerCleanupPreview is what a run WOULD remove. Producing it must not remove
// anything.
type DockerCleanupPreview struct {
	Candidates       []DockerCleanupCandidate `json:"candidates"`
	ReclaimableBytes int64                    `json:"reclaimableBytes"`
	MinAgeHours      int                      `json:"minAgeHours"`
}
