package handlers

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// TestUpdateStack_StoppedEarlyLeavesSkippedRows is agent-os-z91e.47 site 2.
// A manual stack update stops at the first failed service; the services after
// it used to get no update_history row, so the Updates history showed one
// failed row and nothing for the rest. With no Docker service the first
// service fails at once (UpdateComposeServiceStreaming's nil-receiver arm),
// which takes the same failure branch as any other failed service. The failed
// row is the control: it is written either way.
func TestUpdateStack_StoppedEarlyLeavesSkippedRows(t *testing.T) {
	h := newTestResourcesHandlerWithJobManager(t)
	seedStack(t, h.db, "s1", "web")
	require.NoError(t, h.db.SetCachedUpdates([]models.CachedUpdate{
		cachedUpdate("s1", "api"),
		cachedUpdate("s1", "db"),
		cachedUpdate("s1", "worker"),
	}))

	w := postUpdateStack(t, h, "s1")
	require.Equal(t, http.StatusAccepted, w.Code)
	jobID, _ := decodeBody(t, w)["jobId"].(string)
	require.NotEmpty(t, jobID)
	waitForJob(t, h, jobID, services.StatusError)

	failed, _, err := h.db.GetUpdateHistory(models.UpdateHistoryFilters{StackID: "s1", Status: "failed"})
	require.NoError(t, err)
	require.Len(t, failed, 1, "the service that failed records one failed row")

	skipped, _, err := h.db.GetUpdateHistory(models.UpdateHistoryFilters{StackID: "s1", Status: "skipped"})
	require.NoError(t, err)
	require.Len(t, skipped, 2, "each service the stop left un-updated records one skipped row")
	failedService := failed[0].ContainerName
	names := []string{}
	for _, r := range skipped {
		names = append(names, r.ContainerName)
		assert.Equal(t, "manual", r.Trigger)
		assert.Equal(t, "nginx:latest", r.Image)
		require.NotNil(t, r.StackName)
		assert.Equal(t, "web", *r.StackName)
		require.NotNil(t, r.ErrorMessage)
		assert.Equal(t, `not started: service "`+failedService+`" failed earlier in this stack update`, *r.ErrorMessage)
		require.NotNil(t, r.CompletedAt, "a skipped row is complete, so retention can age it out")
		assert.Equal(t, r.StartedAt, *r.CompletedAt)
		assert.Nil(t, r.DurationMs, "nothing ran, so there is no duration")
	}
	assert.ElementsMatch(t, []string{"api", "db", "worker"}, append(names, failedService),
		"the failed row and the skipped rows together name every service")
}
