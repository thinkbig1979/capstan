package handlers

import (
	"errors"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// acquireStackLock takes stackID's operation lock for a handler, or answers
// 409 OPERATION_IN_PROGRESS (the lifecycle routes' code) and returns ok=false.
// The returned release is safe to call more than once. With no lock wired
// (test-built handlers) it takes nothing and release is a no-op.
func acquireStackLock(c *gin.Context, lock *services.OperationLock, stackID, kind string) (release func(), ok bool) {
	if lock == nil {
		return func() {}, true
	}
	token, err := lock.Acquire(stackID, kind)
	if err != nil {
		handleError(c, models.NewAppError(http.StatusConflict, models.ErrOperationInProgress, err.Error()))
		return nil, false
	}
	return sync.OnceFunc(func() { lock.Release(stackID, token) }), true
}

// acquireExclusiveLock is acquireStackLock for an operation that takes every
// stack's turn at once (a container prune): it answers 409 OPERATION_IN_PROGRESS
// naming the stack operation in the way, or the other exclusive holder, and
// returns ok=false. With no lock wired it takes nothing.
func acquireExclusiveLock(c *gin.Context, lock *services.OperationLock, kind string) (release func(), ok bool) {
	if lock == nil {
		return func() {}, true
	}
	token, err := lock.AcquireExclusive(kind)
	if err != nil {
		handleError(c, models.NewAppError(http.StatusConflict, models.ErrOperationInProgress, err.Error()))
		return nil, false
	}
	return sync.OnceFunc(func() { lock.ReleaseExclusive(token) }), true
}

// projectNameLookup is the one stacks-table read refuseSharedProjectName needs.
type projectNameLookup interface {
	GetStackByProjectName(projectName string) (*models.Stack, error)
}

// refuseSharedProjectName answers 409 AMBIGUOUS_STACK and returns true when
// another stack carries stack's compose project name: a compose command run
// with that `-p` would act on the other stack's containers too, under only
// this stack's lock (agent-os-z91e.38, owner decision D28). A lookup fault
// refuses with a 500 (fail closed). Called before the lock is taken and before
// anything is logged or persisted, so a refusal leaves no trace; the compose
// paths in DockerService refuse again for callers that are not handlers.
func refuseSharedProjectName(c *gin.Context, db projectNameLookup, stack *models.Stack) bool {
	_, err := db.GetStackByProjectName(stack.ProjectName)
	switch {
	case err == nil, errors.Is(err, errdefs.ErrNotFound):
		return false
	case errors.Is(err, errdefs.ErrAmbiguous):
		refuseAmbiguousStack(c, err)
	default:
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR",
			"Failed to check whether another stack shares this stack's compose project name", err))
	}
	return true
}
