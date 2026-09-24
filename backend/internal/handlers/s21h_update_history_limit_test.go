package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestGetUpdateHistory_LimitIsCapped pins agent-os-s21h: a client-supplied
// ?limit above the maximum is served the maximum, and the response's limit and
// totalPages describe the page actually served. The seed (250) is larger than
// the cap, so a handler honouring the limit literally returns all 250 rows.
// The below-cap rows are the control: they prove the cap is a ceiling and not a
// clamp of every request to one value.
//
// The cap is spelled as a literal 100 rather than maxUpdateHistoryLimit so the
// test compiles, and fails on the assertion, against the pre-fix handler.
func TestGetUpdateHistory_LimitIsCapped(t *testing.T) {
	handler := newTestResourcesHandler(t)
	router := setupResourcesRouter(handler)

	const seeded = 250
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < seeded; i++ {
		require.NoError(t, handler.db.InsertUpdateHistory(&models.UpdateHistoryEntry{
			ID:            fmt.Sprintf("h%03d", i),
			ContainerID:   "c1",
			ContainerName: "web",
			Image:         "nginx:latest",
			Status:        "success",
			Trigger:       "manual",
			StartedAt:     base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
		}))
	}

	cases := []struct {
		limit      string
		wantRows   int
		wantLimit  int
		wantTotalP int
	}{
		{"999999999", 100, 100, 3},
		{"9223372036854775807", 100, 100, 3},
		{"101", 100, 100, 3},
		{"100", 100, 100, 3},
		{"25", 25, 25, 10},
		{"7", 7, 7, 36},
	}
	for _, tc := range cases {
		t.Run("limit="+tc.limit, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/resources/updates/history?page=1&limit="+tc.limit, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)

			var body struct {
				Entries    []models.UpdateHistoryEntry `json:"entries"`
				Total      int                         `json:"total"`
				Limit      int                         `json:"limit"`
				TotalPages int                         `json:"totalPages"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.Equal(t, seeded, body.Total, "total counts every matching row, not the page")
			require.Equal(t, tc.wantRows, len(body.Entries), "rows served")
			require.Equal(t, tc.wantLimit, body.Limit, "limit reports what was applied, not what was asked for")
			require.Equal(t, tc.wantTotalP, body.TotalPages, "totalPages is computed from the applied limit")
		})
	}
}
