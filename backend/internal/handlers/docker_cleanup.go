package handlers

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// Scheduled Docker cleanup HTTP surface (agent-os-fn7x.3). Routes are registered
// by ResourcesHandler.RegisterRoutes in resources.go.

const (
	// defaultCleanupHistoryLimit is the page size a request that sends no limit
	// gets, matching defaultBackupHistoryLimit next door.
	defaultCleanupHistoryLimit = 50

	// maxCleanupHistoryLimit is a TRUE cap, not a substituted default: a
	// request for limit=1000 is served 100 rows, never 50.
	//
	// The distinction is the whole point. GetAuditLog (settings.go:1108) reads
	// `if err != nil || pageSize < 1 || pageSize > 100 { pageSize = 50 }`, which
	// SUBSTITUTES the default for anything out of range — so limit=1000 would
	// silently become 50 and a client paging through history would see a page
	// size it never asked for and cannot predict. The precedent copied here is
	// maxBackupHistoryLimit (backup.go:785, applied :829-830).
	//
	// A maximum is not optional on this route. MEASURED against
	// modernc.org/sqlite with `LIMIT 2 -> 2 rows` as the positive control
	// proving the instrument discriminates: `LIMIT -1` and `LIMIT -100` both
	// returned 3 of 3 rows, i.e. ANY negative means "no limit" and returns the
	// whole table. `LIMIT 0` returns 0 rows, which is a different lie — an empty
	// page reported as the history. So the bound below is explicit on both
	// sides, and a non-positive value keeps the default rather than reaching the
	// database. agent-os-s21h is the open bug from /updates/history having no
	// maximum.
	maxCleanupHistoryLimit = 100
)

// cleanupPolicyResponse is the wire shape of GET/PUT /resources/cleanup/policy.
//
// MinAllowedAgeHours and MinAllowedIntervalHours are the SERVER floors, sent so
// the UI can show what will be rejected instead of discovering it on submit.
// Precedent: GetLogRetention returns minRetentionDays alongside the configured
// value (settings.go:442) for exactly this reason.
type cleanupPolicyResponse struct {
	Enabled                 bool `json:"enabled"`
	MinAgeHours             int  `json:"minAgeHours"`
	IntervalHours           int  `json:"intervalHours"`
	MinAllowedAgeHours      int  `json:"minAllowedAgeHours"`
	MinAllowedIntervalHours int  `json:"minAllowedIntervalHours"`
}

func newCleanupPolicyResponse(p services.DockerCleanupPolicy) cleanupPolicyResponse {
	return cleanupPolicyResponse{
		Enabled:                 p.Enabled,
		MinAgeHours:             p.MinAgeHours,
		IntervalHours:           p.IntervalHours,
		MinAllowedAgeHours:      services.MinCleanupAgeHours,
		MinAllowedIntervalHours: services.MinCleanupIntervalHours,
	}
}

// getCleanupPolicy serves GET /resources/cleanup/policy.
//
// An ABSENT policy returns the defaults — that is the disabled-by-default state
// every fresh install is in (FR7). An UNREADABLE one returns 500 rather than
// rendering "disabled", because a policy page showing "off" during a database
// fault is indistinguishable from an operator having turned it off, and the only
// other symptom is the disk filling. ResolveDockerCleanupPolicy draws that line
// (it maps sql.ErrNoRows, and only that, to the default); this handler just
// honours it. Same rule as upsertAutoUpdatePolicy (updates.go:890-895,
// agent-os-1gqn) and GetLogRetention (settings.go, agent-os-r1kc).
func (h *ResourcesHandler) getCleanupPolicy(c *gin.Context) {
	policy, err := services.ResolveDockerCleanupPolicy(h.db)
	if err != nil {
		slog.Error("Failed to read the Docker cleanup policy", "error", err)
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR",
			"Failed to read the Docker cleanup policy", err))
		return
	}
	c.JSON(http.StatusOK, newCleanupPolicyResponse(policy))
}

// updateCleanupPolicy serves PUT /resources/cleanup/policy. Every field is
// optional so a client can change one without knowing the others; at least one
// must be present.
//
// Validation happens BEFORE any write, so a request carrying one good and one
// bad field stores neither. The alternative — validate-and-write per field —
// leaves the policy half applied, which is worse than rejecting the whole
// request.
func (h *ResourcesHandler) updateCleanupPolicy(c *gin.Context) {
	var req struct {
		Enabled       *bool `json:"enabled"`
		MinAgeHours   *int  `json:"minAgeHours"`
		IntervalHours *int  `json:"intervalHours"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		handleError(c, models.NewAppError(http.StatusBadRequest, models.ErrValidation, "Invalid request body"))
		return
	}
	if req.Enabled == nil && req.MinAgeHours == nil && req.IntervalHours == nil {
		handleError(c, models.NewAppError(http.StatusBadRequest, models.ErrValidation,
			"Provide at least one of enabled, minAgeHours or intervalHours"))
		return
	}

	// The server floor is enforced here rather than trusted from the client. The
	// age floor is this feature's ONLY retention mechanism, and a prune is
	// irreversible, so the API rejects a value below it instead of quietly
	// clamping: an operator who asked for 0 and was given 1 would believe 0 was
	// stored. (The service clamps as well, as defence in depth — that clamp is
	// fn7x.2's and is not the operator-facing contract.)
	if req.MinAgeHours != nil && *req.MinAgeHours < services.MinCleanupAgeHours {
		handleError(c, models.NewAppError(http.StatusBadRequest, models.ErrValidation,
			"minAgeHours must be at least "+strconv.Itoa(services.MinCleanupAgeHours)))
		return
	}
	if req.IntervalHours != nil && *req.IntervalHours < services.MinCleanupIntervalHours {
		handleError(c, models.NewAppError(http.StatusBadRequest, models.ErrValidation,
			"intervalHours must be at least "+strconv.Itoa(services.MinCleanupIntervalHours)))
		return
	}

	// Capture the pre-write policy so the re-arm below fires only on a change that
	// actually moves the ticker. handlers/settings.go:749-754 is the precedent: it
	// restarts the scan scheduler only when the interval really changed.
	//
	// A read fault here is NOT folded silently into the re-arm decision. It is
	// logged and then treated as "re-arm anyway", because failing to re-arm an
	// operator who just opted in is the worse of the two errors -- but a fault
	// that only ever widens a branch, with nothing said, is the softened-error
	// shape scripts/check-getter-errors.sh exists to catch, and the same lie
	// getCleanupPolicy refuses to tell.
	before, beforeErr := services.ResolveDockerCleanupPolicy(h.db)
	if beforeErr != nil {
		slog.Error("Could not read the previous Docker cleanup policy; re-arming the cleanup tick unconditionally, which discards any interval already counting down",
			"error", beforeErr)
	}

	applied := gin.H{}
	if req.Enabled != nil {
		if err := h.db.SetSetting(services.SettingDockerCleanupEnabled, strconv.FormatBool(*req.Enabled)); err != nil {
			h.cleanupSettingWriteFailed(c, services.SettingDockerCleanupEnabled, err)
			return
		}
		applied["enabled"] = *req.Enabled
	}
	if req.MinAgeHours != nil {
		if err := h.db.SetSetting(services.SettingDockerCleanupMinAgeHours, strconv.Itoa(*req.MinAgeHours)); err != nil {
			h.cleanupSettingWriteFailed(c, services.SettingDockerCleanupMinAgeHours, err)
			return
		}
		applied["minAgeHours"] = *req.MinAgeHours
	}
	if req.IntervalHours != nil {
		if err := h.db.SetSetting(services.SettingDockerCleanupIntervalHours, strconv.Itoa(*req.IntervalHours)); err != nil {
			h.cleanupSettingWriteFailed(c, services.SettingDockerCleanupIntervalHours, err)
			return
		}
		applied["intervalHours"] = *req.IntervalHours
	}

	logActionFromContext(h.actionLog, c, nil, services.ActionUpdateSettings, applied)

	// Re-arm the tick from what was just stored, so opting in takes effect now
	// rather than at the next process restart -- but ONLY on a change that moves
	// the ticker. StartFromPolicy calls Stop first and is therefore not
	// idempotent: re-arming on every PUT would discard a mid-flight interval, so
	// a request touching only minAgeHours must not postpone a cleanup that was
	// already counting down. Nil on a Docker-less host, where there is nothing to
	// arm.
	if h.cleanupArmer != nil {
		tickerMoved := beforeErr != nil ||
			(req.Enabled != nil && *req.Enabled != before.Enabled) ||
			(req.IntervalHours != nil && *req.IntervalHours != before.IntervalHours)
		if tickerMoved {
			h.cleanupArmer.StartFromPolicy()
		}
	}

	policy, err := services.ResolveDockerCleanupPolicy(h.db)
	if err != nil {
		// The write succeeded; only the read-back failed. Say so rather than
		// reporting a failed update.
		slog.Error("Stored the Docker cleanup policy but could not read it back", "error", err)
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR",
			"The Docker cleanup policy was saved but could not be read back", err))
		return
	}
	c.JSON(http.StatusOK, newCleanupPolicyResponse(policy))
}

func (h *ResourcesHandler) cleanupSettingWriteFailed(c *gin.Context, key string, err error) {
	// Setting KEYS are not secret and naming the one that failed is what makes
	// the failure actionable; the value never reaches the message.
	slog.Error("Failed to store a Docker cleanup setting", "key", key, "error", err)
	handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR",
		"Failed to store the Docker cleanup policy", err))
}

// previewCleanup serves POST /resources/cleanup/preview: what a run WOULD remove,
// removing nothing.
//
// POST rather than GET even though it mutates nothing: it takes a body (the
// candidate floor an operator is trying out before saving it) and it makes a
// Docker API call, so it is not cacheable in the way a GET implies.
//
// minAgeHours in the body is optional and defaults to the stored policy. When
// present it is validated against the same server floor the PUT uses, through
// the same code path, so "what will this floor remove" cannot be asked for a
// floor the policy could never hold.
func (h *ResourcesHandler) previewCleanup(c *gin.Context) {
	if h.cleanup == nil {
		respondDockerErr(c, services.ErrDockerUnavailable, http.StatusServiceUnavailable,
			"DOCKER_UNAVAILABLE", DockerUnavailableMessage)
		return
	}

	minAge, ok := h.cleanupMinAgeFromRequest(c)
	if !ok {
		return
	}

	preview, err := h.cleanup.Preview(c.Request.Context(), minAge)
	if err != nil {
		slog.Error("Failed to preview the Docker cleanup", "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to preview the Docker cleanup")
		return
	}
	c.JSON(http.StatusOK, preview)
}

// runCleanup serves POST /resources/cleanup/run: a cleanup now, on the operator's
// say-so.
//
// It runs whether or not the schedule is enabled. `enabled` governs the TICK; a
// manual run is an operator explicitly asking, which is the same distinction
// POST /resources/images/prune already embodies.
//
// IT LEAVES AN AUDIT TRAIL AND BROADCASTS, because it is destructive. All ten
// sibling mutations in resource_mutations.go log ActionPrune and the prune routes
// also broadcast resource_changed; a scheduled-cleanup run that left no trail
// while a manual image prune left one is the first inconsistency an operator
// hits.
func (h *ResourcesHandler) runCleanup(c *gin.Context) {
	if h.cleanup == nil {
		respondDockerErr(c, services.ErrDockerUnavailable, http.StatusServiceUnavailable,
			"DOCKER_UNAVAILABLE", DockerUnavailableMessage)
		return
	}

	minAge, ok := h.cleanupMinAgeFromRequest(c)
	if !ok {
		return
	}

	run, err := h.cleanup.Execute(c.Request.Context(), services.TriggerManual, minAge)
	if err != nil {
		// Execute records a failed run row before returning, so the history is
		// already honest; this only turns the error into a response.
		slog.Error("Docker cleanup run failed", "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "INTERNAL_ERROR", "Docker cleanup failed")
		return
	}

	logActionFromContext(h.actionLog, c, nil, services.ActionPrune, gin.H{
		"resource":              "docker_cleanup",
		"images_deleted":        run.ImagesDeleted,
		"space_reclaimed":       run.BytesReclaimed,
		"cache_space_reclaimed": run.CacheBytesReclaimed,
		"min_age_hours":         run.MinAgeHours,
	})
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "docker_cleanup", Timestamp: time.Now()})

	c.JSON(http.StatusOK, run)
}

// cleanupMinAgeFromRequest resolves the age floor for a preview or a run: the
// optional body value when present, the stored policy otherwise. It writes the
// error response itself and reports whether the caller may proceed.
//
// An unreadable policy REFUSES rather than falling back to
// DefaultCleanupMinAgeHours. Pruning under a floor nobody chose, on the strength
// of a database fault, is what agent-os-rltu and agent-os-r1kc settled against.
func (h *ResourcesHandler) cleanupMinAgeFromRequest(c *gin.Context) (int, bool) {
	var req struct {
		MinAgeHours *int `json:"minAgeHours"`
	}
	// An absent or empty body is the ordinary case, not an error: ShouldBindJSON
	// returns io.EOF for it. Only a body that is present and malformed is
	// rejected, which is why this discriminates on the error rather than
	// ignoring it — a client that sent `{"minAgeHours":"nope"}` must not be
	// silently served the policy default.
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		handleError(c, models.NewAppError(http.StatusBadRequest, models.ErrValidation, "Invalid request body"))
		return 0, false
	}

	if req.MinAgeHours != nil {
		if *req.MinAgeHours < services.MinCleanupAgeHours {
			handleError(c, models.NewAppError(http.StatusBadRequest, models.ErrValidation,
				"minAgeHours must be at least "+strconv.Itoa(services.MinCleanupAgeHours)))
			return 0, false
		}
		return *req.MinAgeHours, true
	}

	policy, err := services.ResolveDockerCleanupPolicy(h.db)
	if err != nil {
		slog.Error("Failed to read the Docker cleanup policy", "error", err)
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR",
			"Failed to read the Docker cleanup policy", err))
		return 0, false
	}
	return policy.MinAgeHours, true
}

// getCleanupHistory serves GET /resources/cleanup/history: recorded runs, newest
// first.
//
// THE LIMIT IS CLAMPED HERE, in the handler, not inside GetDockerCleanupRuns.
// That function puts limit straight into `LIMIT ?` with no validation, and this
// route is its only caller, so a handler clamp closes the defect for the whole
// reachable surface. Clamping inside it would split a convention with its
// sibling GetBackupRuns, which is the same shape and out of scope here.
//
// An unparseable, zero or negative limit keeps the default rather than reaching
// SQLite — see maxCleanupHistoryLimit for the measurement that makes the
// negative case load-bearing. parseQueryParamInt (git.go:327) was not used: it
// returns min on an unparseable value, so parseQueryParamInt(l, 1, 100) would
// serve a one-row page for `limit=nonsense` instead of the documented default.
func (h *ResourcesHandler) getCleanupHistory(c *gin.Context) {
	limit := defaultCleanupHistoryLimit
	// The Atoi error is deliberately not surfaced, and this site is recorded in
	// scripts/check-getter-errors-baseline.txt for that reason. That scanner's
	// subject is a DATABASE or DAEMON fault read as a default; this is a client
	// query parameter, where "limit=nonsense" has no answer to report and the
	// documented default is the honest response. Identical shape, already
	// baselined, at getUpdateHistory (updates.go:757,762) and getHistory
	// (backup.go:820,825) -- this is the eighth instance of one accepted pattern,
	// not a new kind of site.
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	if limit > maxCleanupHistoryLimit {
		limit = maxCleanupHistoryLimit
	}

	runs, err := h.db.GetDockerCleanupRuns(limit)
	if err != nil {
		slog.Error("Failed to read the Docker cleanup history", "error", err)
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR",
			"Failed to read the Docker cleanup history", err))
		return
	}
	if runs == nil {
		runs = []models.DockerCleanupRun{}
	}
	c.JSON(http.StatusOK, gin.H{"runs": runs, "limit": limit})
}
