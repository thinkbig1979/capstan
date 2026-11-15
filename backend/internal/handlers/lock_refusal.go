package handlers

import (
	"net/http"
	"reflect"
	"slices"
	"strings"

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
// is the same whatever the guesses were. The message is "Unlock env values
// to <action>: <reason>".
//
// The backup routes that change where backups go, with what credential or
// what they keep, and the ones that restore, prune or initialise a
// repository, use it too (agent-os-h71i): a locked session that could point
// the repository at a place it controls would read every stack's .env.
//
// A handler whose request may not need the refusal (an empty env create, a
// backup settings save that only edits the schedule) gates with
// `if <needs it> && refuseLocked(...)`, still before the lock and the disk.
//
// TestLockedRefusalGuard_ly6i keeps every such handler on this helper.
func refuseLocked(c *gin.Context, action, reason string) bool {
	if envUnlocked(c) {
		return false
	}
	c.JSON(http.StatusForbidden, models.NewAppError(
		http.StatusForbidden,
		models.ErrEnvLocked,
		"Unlock env values to "+action+": "+reason,
	))
	return true
}

// The reasons a refusal gives, after the action.
const (
	// lockedEchoReason: the reply passes the caller's own text through the
	// secret masking (agent-os-ly6i).
	lockedEchoReason = "while locked, the reply could show which text matches a secret"
	// lockedBackupReason: the request changes where backups go, with what
	// credential, or what they keep (agent-os-h71i).
	lockedBackupReason = "this needs your password, because it changes where backups go or what they keep"
)

// setsFieldOutside reports whether req, a pointer to a request struct, sets a
// field whose json name is not in allowed. A field is set when it is not its
// zero value: a non-nil pointer, a non-empty string. It is an allow list on
// purpose (agent-os-97pn): a field added to the request later is refused
// while locked until someone names it here, instead of being let through.
func setsFieldOutside(req any, allowed ...string) bool {
	v := reflect.Indirect(reflect.ValueOf(req))
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		if v.Field(i).IsZero() {
			continue
		}
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name == "" {
			name = t.Field(i).Name
		}
		if !slices.Contains(allowed, name) {
			return true
		}
	}
	return false
}
