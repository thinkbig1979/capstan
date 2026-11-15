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
// what they keep, which stacks are backed up (agent-os-tp3g), and the ones
// that restore, prune or initialise a repository, use it too (agent-os-h71i): a locked session that could point
// the repository at a place it controls would read every stack's .env.
//
// A git commit uses it too (agent-os-xmbg): its message is masked in a
// locked log. A git pull uses it too (agent-os-hblm): it lands the remote's text in a
// stack folder, which a locked view then masks. The git credential writes use
// it too (agent-os-mu4z): the global SSH key,
// HTTPS user and token, and a folder's own credentials.
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
	// lockedPullReason: a pull writes the remote's text into the stack folder,
	// where a locked view masks it, and a redeploy runs it (agent-os-hblm).
	lockedPullReason = "a pull writes the remote's files into your stacks, and a redeploy runs them"
	// The backup refusals (agent-os-h71i), one reason per action (zp59).
	lockedBackupSettingsReason = "backups hold every stack's .env, so whoever picks the repository can read them"
	// lockedBackupRetentionReason: a retention-only change (agent-os-wxap).
	lockedBackupRetentionReason = "a shorter retention lets the next prune delete backups"
	lockedRestoreReason         = "a restore writes backed-up files, secrets included, into your stacks"
	lockedDRRestoreReason       = "it writes backed-up files, secrets included, into your stacks"
	lockedPruneReason           = "pruning deletes backups"
	lockedRepoInitReason        = "the repository will hold every stack's .env"
	// lockedBackupPolicyReason: a policy write switches a stack's backups on
	// or off, or changes how they run (agent-os-tp3g).
	lockedBackupPolicyReason = "a backup policy decides which stacks are backed up and how"
	// lockedBlankedReason: a locked session was handed the env values blanked,
	// so a save from that state would write the blanks (agent-os-d1ue).
	lockedBlankedReason = "while locked, the values you see are blanked, so saving would overwrite every secret with them"
	// lockedAuditSearchReason: an audit-log search matches the entries'
	// details, hidden while locked (agent-os-3ajm).
	lockedAuditSearchReason = "the search reads the entries' details, which are hidden while locked"
	// lockedGitCredentialReason: the request changes the credential git uses
	// to reach the remote (agent-os-mu4z).
	lockedGitCredentialReason = "pushes and pulls use them"
	// lockedGitAuthorReason: the author lands in commits a locked log masks
	// (agent-os-xmbg).
	lockedGitAuthorReason = "a locked history masks the author with your secrets"
)

// gitAuthorFields are the git settings that set the commit author. Every git
// setting needs an unlock: the credentials because pushes and pulls use them
// (agent-os-mu4z), the author because a locked log masks it with the stack's
// secrets (agent-os-xmbg, review F3). A request setting only these gets the
// author's reason.
var gitAuthorFields = []string{"authorName", "authorEmail"}

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
