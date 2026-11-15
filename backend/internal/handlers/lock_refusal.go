package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// refuseLocked answers 403 ENV_LOCKED and returns true when this session has
// not unlocked env values (agent-os-ly6i). Every handler whose reply can pass
// the caller's own text through the secret masking calls it first, before the
// operation lock, the disk and the lint: the masking stars any text equal to a
// secret, so a list of guesses sent by a locked session would come back with
// the secret's position starred. Saves count too: a locked save of a comment
// holding the guesses, then a locked GET that masks the file, is the same
// oracle in two calls. The refusal reads nothing of the request, so its text
// is the same whatever the guesses were. action finishes "Unlock env values
// to ...".
//
// TestLockedRefusalGuard_ly6i keeps every handler that lints or writes
// request compose or env text on this helper.
func refuseLocked(c *gin.Context, action string) bool {
	if envUnlocked(c) {
		return false
	}
	c.JSON(http.StatusForbidden, models.NewAppError(
		http.StatusForbidden,
		models.ErrEnvLocked,
		"Unlock env values to "+action+": while locked, the reply could show which text matches a secret",
	))
	return true
}
