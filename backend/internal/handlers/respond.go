package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// renderResult writes the ActionResult as JSON to a gin context using the
// appropriate HTTP status code. All action endpoints should use this
// to ensure consistent wire format across domains.
func renderResult(c *gin.Context, r truth.ActionResult) {
	c.JSON(r.HTTPStatus(), r)
}

// handleError writes err as a JSON error response, using the AppError's
// status and code when available and falling back to a generic 500.
func handleError(c *gin.Context, err error) {
	if appErr, ok := err.(*models.AppError); ok {
		c.JSON(appErr.Status, appErr)
		return
	}

	c.JSON(http.StatusInternalServerError, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error"))
}

// userIDFrom extracts the authenticated userID from the gin context,
// defaulting to "anonymous" when unset.
func userIDFrom(c *gin.Context) string {
	userID := c.GetString("userID")
	if userID == "" {
		userID = "anonymous"
	}
	return userID
}

// logActionFromContext logs an action using the userID found on the gin
// context, delegating to the given ActionLogger.
func logActionFromContext(l *services.ActionLogger, c *gin.Context, stackID *string, action string, detail interface{}) {
	l.LogWithRequest(middleware.RequestIDFrom(c), userIDFrom(c), stackID, action, detail)
}
