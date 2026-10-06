package handlers

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
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
