package services

import (
	"errors"
	"log/slog"

	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// StackStatusStore is where a stack's verified status is kept. The stored
// value is what the stack list shows when Docker cannot be reached.
type StackStatusStore interface {
	UpdateStackStatus(id, status string) error
}

// LifecycleStatus maps a verified lifecycle result to the stack status to
// store. It prefers the verified end state in Details["status"], which every
// *Verified call that reached verification sets, and falls back to the
// outcome when verification never ran.
func LifecycleStatus(ar truth.ActionResult) string {
	if ar.Details != nil {
		if s, ok := ar.Details["status"].(string); ok && s != "" {
			return s
		}
	}
	switch ar.Outcome {
	case truth.OutcomeSuccess, truth.OutcomeNoChange:
		return string(StackStatusRunning)
	case truth.OutcomePartial:
		return string(StackStatusPartial)
	default:
		return string(StackStatusError)
	}
}

// StoreLifecycleStatus stores the status a verified lifecycle result proves
// and returns it. Every caller of Start/Stop/Restart/DeleteVerified goes
// through it, so the stored status never lags what the stack did
// (agent-os-vush; kept that way by TestLifecycleCallsStoreStatus_Vush). A
// failed write is logged, not returned: the operation itself already happened.
//
// A result that started nothing (a refused compose, no Docker service) writes
// nothing, as operations.go stores nothing for a refused run; the mapped
// status is still returned for the caller's response.
func StoreLifecycleStatus(store StackStatusStore, stackID string, ar truth.ActionResult) string {
	status := LifecycleStatus(ar)
	if startedNothing(ar) {
		return status
	}
	if err := store.UpdateStackStatus(stackID, status); err != nil {
		slog.Warn("Failed to persist verified stack status", "stackID", stackID, "status", status, "error", err)
	}
	return status
}

// startedNothing reports a lifecycle result refused before Docker was asked
// to do anything.
func startedNothing(ar truth.ActionResult) bool {
	var refused composeRefusedError
	return errors.As(ar.Err, &refused) || errors.Is(ar.Err, ErrDockerUnavailable)
}
