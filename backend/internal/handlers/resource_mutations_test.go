package handlers

import (
	"testing"

	"github.com/docker/docker/api/types/image"
	"github.com/stretchr/testify/assert"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// ---- classifyImageDeleteResponse (finding #12) ----

func TestClassifyImageDeleteResponse_EmptySlice(t *testing.T) {
	ar := classifyImageDeleteResponse(nil)
	assert.Equal(t, truth.OutcomeNoChange, ar.Outcome)
}

func TestClassifyImageDeleteResponse_DeletedEntry(t *testing.T) {
	// A Deleted entry means the image layer was actually removed.
	resp := []image.DeleteResponse{
		{Deleted: "sha256:abc123"},
	}
	ar := classifyImageDeleteResponse(resp)
	assert.Equal(t, truth.OutcomeSuccess, ar.Outcome, "Deleted entry must produce success")
	assert.Equal(t, "image removed", ar.Reason)
	assert.NotNil(t, ar.Details)
	deleted, ok := ar.Details["deleted"].([]string)
	assert.True(t, ok, "details.deleted must be []string")
	assert.Contains(t, deleted, "sha256:abc123")
}

func TestClassifyImageDeleteResponse_UntaggedOnly(t *testing.T) {
	// Only Untagged entries: the image still exists under another tag.
	// This is the false-success guard for finding #12.
	resp := []image.DeleteResponse{
		{Untagged: "myimage:latest"},
		{Untagged: "myimage:v1"},
	}
	ar := classifyImageDeleteResponse(resp)
	assert.Equal(t, truth.OutcomeNoChange, ar.Outcome,
		"untagged-only response must not be success (finding #12)")
	assert.Contains(t, ar.Reason, "still referenced")
	untagged, ok := ar.Details["untagged"].([]string)
	assert.True(t, ok)
	assert.Len(t, untagged, 2)
	assert.Empty(t, ar.Details["deleted"])
}

func TestClassifyImageDeleteResponse_DeletedAndUntagged(t *testing.T) {
	// A mix: tag removed AND layer deleted.
	resp := []image.DeleteResponse{
		{Untagged: "myimage:old"},
		{Deleted: "sha256:abc123"},
	}
	ar := classifyImageDeleteResponse(resp)
	assert.Equal(t, truth.OutcomeSuccess, ar.Outcome,
		"at least one Deleted entry must produce success")
	deleted, _ := ar.Details["deleted"].([]string)
	assert.Len(t, deleted, 1)
	untagged, _ := ar.Details["untagged"].([]string)
	assert.Len(t, untagged, 1)
}

// ---- classifyImagePruneReport (finding #13) ----

func TestClassifyImagePruneReport_NothingPruned(t *testing.T) {
	ar := classifyImagePruneReport(nil, 0)
	assert.Equal(t, truth.OutcomeNoChange, ar.Outcome)
	assert.Equal(t, "nothing to prune", ar.Reason)
}

func TestClassifyImagePruneReport_UntaggedEntriesCountCorrectly(t *testing.T) {
	// Finding #13: untagged entries must be counted, not dropped.
	deleted := []image.DeleteResponse{
		{Untagged: "myimage:tag1"},
		{Untagged: "myimage:tag2"},
	}
	ar := classifyImagePruneReport(deleted, 1024)
	assert.Equal(t, truth.OutcomeSuccess, ar.Outcome)
	tagsRemoved, _ := ar.Details["tagsRemoved"].(int)
	assert.Equal(t, 2, tagsRemoved, "tagsRemoved must count untagged entries (finding #13)")
	imagesDeleted, _ := ar.Details["imagesDeleted"].(int)
	assert.Equal(t, 0, imagesDeleted, "no actual layer deletions in this case")
}

func TestClassifyImagePruneReport_DeletedLayersCountCorrectly(t *testing.T) {
	deleted := []image.DeleteResponse{
		{Deleted: "sha256:abc"},
		{Deleted: "sha256:def"},
	}
	ar := classifyImagePruneReport(deleted, 2048)
	assert.Equal(t, truth.OutcomeSuccess, ar.Outcome)
	imagesDeleted, _ := ar.Details["imagesDeleted"].(int)
	assert.Equal(t, 2, imagesDeleted)
	spaceReclaimed, _ := ar.Details["spaceReclaimed"].(uint64)
	assert.Equal(t, uint64(2048), spaceReclaimed)
}

func TestClassifyImagePruneReport_MixedEntries(t *testing.T) {
	deleted := []image.DeleteResponse{
		{Untagged: "foo:latest"},
		{Deleted: "sha256:aaa"},
	}
	ar := classifyImagePruneReport(deleted, 512)
	assert.Equal(t, truth.OutcomeSuccess, ar.Outcome)
	assert.Equal(t, 1, ar.Details["imagesDeleted"])
	assert.Equal(t, 1, ar.Details["tagsRemoved"])
}

// ---- stackDirIsInsideRoot (finding #6 path guard) ----

func TestStackDirIsInsideRoot_Valid(t *testing.T) {
	assert.True(t, stackDirIsInsideRoot("/stacks/mystack", "/stacks"))
	assert.True(t, stackDirIsInsideRoot("/stacks/nested/deep", "/stacks"))
}

func TestStackDirIsInsideRoot_Invalid(t *testing.T) {
	// Root itself must be rejected (not inside, it IS the root).
	assert.False(t, stackDirIsInsideRoot("/stacks", "/stacks"),
		"root directory itself must not pass the guard")
	// Path traversal attempts.
	assert.False(t, stackDirIsInsideRoot("/etc/passwd", "/stacks"))
	assert.False(t, stackDirIsInsideRoot("/stacks-evil/thing", "/stacks"))
	assert.False(t, stackDirIsInsideRoot("/", "/stacks"))
	// Empty values.
	assert.False(t, stackDirIsInsideRoot("", "/stacks"))
	assert.False(t, stackDirIsInsideRoot("/stacks/foo", ""))
}
