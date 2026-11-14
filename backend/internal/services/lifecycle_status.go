package services

import (
	"errors"
	"log/slog"

	"github.com/thinkbig1979/capstan/backend/internal/models"
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

// StackStatusReader reads a stack's status as Docker reports it now.
// *DockerService satisfies it.
type StackStatusReader interface {
	Status(stack models.Stack) (string, []models.Container, error)
}

// The handlers' update seam and the scheduler's updateChecker both require
// it of *DockerService.
var _ StackStatusReader = (*DockerService)(nil)

// StoreObservedStackStatus stores the stack's status as Docker reports it
// after an operation changed some of its containers: an update
// (agent-os-txqx) or a single-container start, stop, restart or delete
// (agent-os-nyt5). Neither is a whole-stack verb, so its result cannot be
// mapped the way StoreLifecycleStatus maps one: a stopped service updated and
// stopped again, one failed service of several, or one container stopped of
// several, would store the wrong status for the whole stack. The stack is
// re-read instead. Every update entry point and container action calls it
// (kept that way by TestUpdateCallsStoreObservedStatus_Txqx).
//
// When every result started nothing (a refused operation, no Docker service),
// nothing is stored. A failed read stores nothing and is logged: an unknown
// state must not overwrite the last known one. The read bounds itself:
// Status runs compose ps under commandWithDeadline on its own background
// context and composeTimeout, so a cancelled job cannot cut it short and a
// hung compose ps cannot hold the caller past that deadline.
func StoreObservedStackStatus(store StackStatusStore, reader StackStatusReader, stack models.Stack, results ...truth.ActionResult) {
	started := false
	for _, ar := range results {
		if !startedNothing(ar) {
			started = true
		}
	}
	if !started {
		return
	}
	status, _, err := reader.Status(stack)
	if err != nil {
		slog.Warn("Could not read the stack's status after its containers changed; its stored status is unchanged", "stackID", stack.ID, "error", err)
		return
	}
	if err := store.UpdateStackStatus(stack.ID, status); err != nil {
		slog.Warn("Failed to persist stack status after its containers changed", "stackID", stack.ID, "status", status, "error", err)
	}
}

// startedNothing reports a lifecycle or update result refused before Docker
// was asked to do anything.
func startedNothing(ar truth.ActionResult) bool {
	var refused composeRefusedError
	var updateRefused updateRefusedError
	return errors.As(ar.Err, &refused) || errors.As(ar.Err, &updateRefused) || errors.Is(ar.Err, ErrDockerUnavailable)
}
