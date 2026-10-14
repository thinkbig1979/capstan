package services

// This file holds the services package's structs that handlers serve as a
// response body unchanged, and nothing else.
// backend/tygo.yaml generates frontend/src/types/generated-services.ts from
// this file alone (include_files), because the rest of the package is full of
// service structs, jobs and internal records that are not a JSON contract.
// tygo v0.2.21 filters by file, not by type (tygo/config.go, IsFileIgnored),
// so a struct that is served as-is belongs here and anything else does not.

import "time"

// DiskUsageBreakdown is the per-category Docker disk usage served inside
// GET /dashboard/stats's diskUsage field.
type DiskUsageBreakdown struct {
	Images     int64 `json:"images"`
	Containers int64 `json:"containers"`
	Volumes    int64 `json:"volumes"`
	BuildCache int64 `json:"buildCache"`
	Total      int64 `json:"total"`
	// VolumesSizeKnown is false when Docker reported at least one volume's
	// size as unavailable (-1 or no usage data). Volumes then sums only the
	// known ones, so it is a floor, not a measurement.
	VolumesSizeKnown bool `json:"volumesSizeKnown"`
}

// HostSummary is GET /resources/host/summary's body: per-category counts,
// sizes and what can be freed, all from one DiskUsage snapshot (HostSpaceService).
type HostSummary struct {
	Disk       HostDiskSummary       `json:"disk"`
	Containers HostContainerSummary  `json:"containers"`
	Images     HostImageSummary      `json:"images"`
	Volumes    HostVolumeSummary     `json:"volumes"`
	Networks   HostNetworkSummary    `json:"networks"`
	BuildCache HostBuildCacheSummary `json:"buildCache"`
	MeasuredAt time.Time             `json:"measuredAt"`
}

// HostDiskSummary is the headline. Reclaimable is what one click on the
// dialog's defaults frees (images, build cache, containers); ReclaimableAll
// adds the volumes, which the dialog leaves unticked. InUse is Total minus
// Reclaimable, floored at 0, so the two parts of the disk bar add up.
type HostDiskSummary struct {
	Total            int64 `json:"total"`
	InUse            int64 `json:"inUse"`
	Reclaimable      int64 `json:"reclaimable"`
	ReclaimableAll   int64 `json:"reclaimableAll"`
	VolumesSizeKnown bool  `json:"volumesSizeKnown"`
}

// HostContainerSummary: Stopped counts exited, created and dead containers, so
// paused and restarting ones are in Count but in neither Running nor Stopped.
type HostContainerSummary struct {
	Count            int   `json:"count"`
	Running          int   `json:"running"`
	Stopped          int   `json:"stopped"`
	ReclaimableCount int   `json:"reclaimableCount"`
	ReclaimableBytes int64 `json:"reclaimableBytes"`
}

// HostImageSummary: Bytes is LayersSize (shared layers counted once);
// ReclaimableBytes sums each candidate's Size minus SharedSize, a lower bound.
type HostImageSummary struct {
	Count            int   `json:"count"`
	InUse            int   `json:"inUse"`
	Bytes            int64 `json:"bytes"`
	ReclaimableCount int   `json:"reclaimableCount"`
	ReclaimableBytes int64 `json:"reclaimableBytes"`
}

// HostVolumeSummary: SizeKnown false means Bytes sums only the volumes whose
// size Docker could compute, so it is a floor.
type HostVolumeSummary struct {
	Count            int   `json:"count"`
	Attached         int   `json:"attached"`
	Bytes            int64 `json:"bytes"`
	SizeKnown        bool  `json:"sizeKnown"`
	ReclaimableCount int   `json:"reclaimableCount"`
	ReclaimableBytes int64 `json:"reclaimableBytes"`
}

// HostNetworkSummary has no sizes: networks use no disk.
type HostNetworkSummary struct {
	Count            int `json:"count"`
	BuiltIn          int `json:"builtIn"`
	StackOwned       int `json:"stackOwned"`
	ReclaimableCount int `json:"reclaimableCount"`
}

type HostBuildCacheSummary struct {
	Count            int   `json:"count"`
	Bytes            int64 `json:"bytes"`
	ReclaimableBytes int64 `json:"reclaimableBytes"`
}

// HostCandidates is GET /resources/host/candidates's body: the items the
// Free-up-space dialog lists. Every slice is non-nil, so an empty category
// sends [] rather than null.
type HostCandidates struct {
	Images     []HostCandidateItem      `json:"images"`
	BuildCache HostBuildCacheCandidates `json:"buildCache"`
	Containers []HostCandidateItem      `json:"containers"`
	Volumes    []HostCandidateItem      `json:"volumes"`
	Networks   []HostCandidateItem      `json:"networks"`
}

// HostCandidateItem is one removable object. Size is omitted when Docker did
// not report it, so an unknown size is never sent as a measured 0.
type HostCandidateItem struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Size   *int64 `json:"size,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// HostBuildCacheCandidates: build cache is removed as a whole, not per record.
type HostBuildCacheCandidates struct {
	Count int   `json:"count"`
	Bytes int64 `json:"bytes"`
}

// HostFreeSpaceRequest is POST /resources/host/free-space's body: the IDs the
// user ticked in the Free-up-space dialog. It is here, unlike other request
// bodies, so the dialog's hook sends the generated shape. A category left out
// removes nothing in it.
type HostFreeSpaceRequest struct {
	Images     []string `json:"images,omitempty"`
	Containers []string `json:"containers,omitempty"`
	Volumes    []string `json:"volumes,omitempty"`
	Networks   []string `json:"networks,omitempty"`
	BuildCache bool     `json:"buildCache,omitempty"`
}

// HostFreeSpaceResult is what a free-space run did, served as the
// ActionResult's details: one key per field.
type HostFreeSpaceResult struct {
	Images     HostFreeSpaceCategoryResult   `json:"images"`
	Containers HostFreeSpaceCategoryResult   `json:"containers"`
	Volumes    HostFreeSpaceCategoryResult   `json:"volumes"`
	Networks   HostFreeSpaceCategoryResult   `json:"networks"`
	BuildCache HostFreeSpaceBuildCacheResult `json:"buildCache"`
	// SpaceReclaimed is Docker's disk use before the run minus after it,
	// floored at 0. Omitted when the after-read failed: unknown, not 0.
	SpaceReclaimed *int64 `json:"spaceReclaimed,omitempty"`
}

// HostFreeSpaceCategoryResult: every requested ID lands in exactly one list.
// The lists are never nil, so an empty one is sent as [].
type HostFreeSpaceCategoryResult struct {
	Removed []string                 `json:"removed"`
	Failed  []HostFreeSpaceItemIssue `json:"failed"`
	Skipped []HostFreeSpaceItemIssue `json:"skipped"`
}

// HostFreeSpaceItemIssue names an ID that was not removed, and why.
type HostFreeSpaceItemIssue struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// HostFreeSpaceBuildCacheResult: build cache is pruned as a whole. Removed is
// the number of records Docker deleted. Failed or Skipped carries the reason
// when the prune failed or was not run although requested.
type HostFreeSpaceBuildCacheResult struct {
	Requested      bool   `json:"requested"`
	Removed        int    `json:"removed"`
	SpaceReclaimed int64  `json:"spaceReclaimed"`
	Failed         string `json:"failed,omitempty"`
	Skipped        string `json:"skipped,omitempty"`
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
//
// ReclaimableBytes is the dangling images only; CacheReclaimableBytes is the
// build cache a run would also prune. They are separate because the history
// row records them separately (bytesReclaimed, cacheBytesReclaimed), and the
// operator-facing figure is their sum.
type DockerCleanupPreview struct {
	Candidates            []DockerCleanupCandidate `json:"candidates"`
	ReclaimableBytes      int64                    `json:"reclaimableBytes"`
	CacheReclaimableBytes int64                    `json:"cacheReclaimableBytes"`
	MinAgeHours           int                      `json:"minAgeHours"`
}

// The three value sets below are served inside update-job payloads (Job,
// LogLine, and the update_job_* frames on /ws/events). They live here so tygo
// emits them as literal unions (enum_style: union in tygo.yaml) and the
// frontend asserts its hand-written copies equal to them at compile time
// (agent-os-th4h). Add a value here and `tsc -b` fails until the frontend
// handles it.

// tygo builds a union only when the constants' names start with their type's
// name (tygo/write_toplevel.go, detectEnumGroup), hence Stream* and
// JobTargetType* below; any other prefix silently emits `string`.

// Status represents the lifecycle state of an update job.
type Status string

const (
	StatusQueued     Status = "queued"
	StatusPulling    Status = "pulling"
	StatusRecreating Status = "recreating"
	StatusSuccess    Status = "success"
	StatusError      Status = "error"
)

// Stream is one of the three allowed stream values for a LogLine.
type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
	StreamStatus Stream = "status"
)

// JobTargetType is what an update job updates: one container or a whole stack.
type JobTargetType string

const (
	JobTargetTypeContainer JobTargetType = "container"
	JobTargetTypeStack     JobTargetType = "stack"
)

// DoneStatus is the status a `done` frame on /ws/jobs carries: the terminal
// subset of Status. Values are literals, not DoneStatus(StatusSuccess), because
// tygo cannot evaluate a conversion and emits `unknown` for it; a test ties the
// set to Status instead (agent-os-rc69).
type DoneStatus string

const (
	DoneStatusSuccess DoneStatus = "success"
	DoneStatusError   DoneStatus = "error"
)

// StackStatus is a stack's status as the stacks API and the stack_status frame
// on /ws/events report it. models.Stack.Status and models.StackEvent.Status
// stay plain strings (models cannot import services); producers convert with
// string(StackStatusX) (agent-os-rc69).
type StackStatus string

const (
	StackStatusRunning StackStatus = "running"
	StackStatusStopped StackStatus = "stopped"
	StackStatusPartial StackStatus = "partial"
	StackStatusPaused  StackStatus = "paused"
	StackStatusUnknown StackStatus = "unknown"
	StackStatusError   StackStatus = "error"
)
