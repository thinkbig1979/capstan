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
