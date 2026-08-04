package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestHandleError_WrappedAppErrorKeepsStatus pins N9 (agent-os-4pa.3): handleError
// must surface an *AppError's status even when it is wrapped with %w, instead of
// collapsing to a generic 500. Seen failing first against the type-assertion form
// (err.(*models.AppError)), which does not traverse a wrap.
func TestHandleError_WrappedAppErrorKeepsStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	appErr := models.NewAppError(http.StatusConflict, "CONFLICT", "already exists")
	wrapped := fmt.Errorf("create user: %w", appErr)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	handleError(c, wrapped)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d — a wrapped AppError must keep its status", w.Code, http.StatusConflict)
	}
}

// TestHandleError_DirectAppErrorKeepsStatus is the unwrapped control: the behaviour
// that already worked must keep working.
func TestHandleError_DirectAppErrorKeepsStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	appErr := models.NewAppError(http.StatusBadRequest, "BAD_REQUEST", "nope")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	handleError(c, appErr)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestHandleError_PlainErrorFallsBackTo500 pins the fallback for a non-AppError.
func TestHandleError_PlainErrorFallsBackTo500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	handleError(c, errors.New("boom"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}
