package models

// The two 401 codes are not interchangeable, and the frontend response
// interceptor (frontend/src/lib/api.ts) branches on the difference:
//
//   - ErrSessionExpired means "this session cannot be used" — no token, an
//     unusable token, a session row that is gone or past its expiry, or a
//     session whose user row no longer exists. The frontend logs the user out
//     and navigates to /login on this code. It is minted by
//     middleware.AuthMiddleware and by the handlers that discover the
//     session's user is gone (handlers/settings.go, handlers/auth.go).
//     handlers/ws.go follows it too as of agent-os-2zq, though nothing
//     consumes its handshake body today: a browser cannot read a failed
//     handshake, and frontend/src/lib/ws.ts keys reconnect policy on the
//     close codes (4401/4429) rather than on these JSON codes.
//   - ErrUnauthorized means "the credential you just supplied is wrong", with
//     the session itself untouched: a wrong login password, a wrong current
//     password on change-password, a wrong password at the env-unlock prompt.
//     The frontend shows the message and leaves the user where they are.
//
// Reaching for ErrSessionExpired on a credential path recreates agent-os-318,
// where mistyping your own password bounced you to /login mid-session.
const (
	ErrUnauthorized      = "UNAUTHORIZED"
	ErrForbidden         = "FORBIDDEN"
	ErrNotFound          = "NOT_FOUND"
	ErrValidation        = "VALIDATION_ERROR"
	ErrComposeValidation = "COMPOSE_VALIDATION_ERROR"
	ErrDockerUnavailable = "DOCKER_UNAVAILABLE"
	ErrDockerOperation   = "DOCKER_OPERATION"
	ErrGitDirty          = "GIT_DIRTY"
	ErrGitConflict       = "GIT_CONFLICT"
	ErrGitNotRepo        = "GIT_NOT_REPO"
	// ErrGitNoCommits is a repository that exists but has no commits yet — the
	// state `git init` leaves behind until the first commit. It is a ROUTINE
	// negative answer, not a client error, and it needs a code of its own
	// rather than ErrNotFound precisely because handlers/respond.go's
	// routineErrorCodes keys on the code: ErrNotFound is also the genuine
	// "Stack not found" error at 20 sites, so the two must not share (agent-os-n2df).
	ErrGitNoCommits = "GIT_NO_COMMITS"
	// ErrStackDirMissing is a stack whose configured directory does not exist
	// on disk. It is NOT routine and is deliberately absent from
	// handlers/respond.go's routineErrorCodes: an unmounted stacks volume
	// produces it for every stack at once, and that must stay a warning.
	//
	// It exists so that condition stops being answered as ErrGitNotRepo, which
	// sent an operator looking for a git problem when the volume was the fault.
	// The 404 is deliberate and unchanged from agent-os-pawv, whose contract
	// was that this answer be a 404 that SAYS WHY; only the code moves, and it
	// now says why more precisely than ErrGitNotRepo did (agent-os-n2df).
	ErrStackDirMissing       = "STACK_DIR_MISSING"
	ErrGitRemoteUnreachable  = "GIT_REMOTE_UNREACHABLE"
	ErrPathTraversal         = "PATH_TRAVERSAL"
	ErrDuplicateStack        = "DUPLICATE_STACK"
	ErrStackNotFound         = "STACK_NOT_FOUND"
	ErrSessionExpired        = "SESSION_EXPIRED"
	ErrSetupRequired         = "SETUP_REQUIRED"
	ErrSetupAlreadyDone      = "SETUP_ALREADY_DONE"
	ErrRateLimited           = "RATE_LIMITED"
	ErrEncryptionUnavailable = "ENCRYPTION_KEY_MISSING"
	ErrOperationInProgress   = "OPERATION_IN_PROGRESS"
	// ErrAmbiguousStack refuses an action on a container whose compose project
	// name is shared by more than one stack, so which stack it belongs to (and
	// so which stack's lock to take) cannot be told (agent-os-z91e.19).
	ErrAmbiguousStack = "AMBIGUOUS_STACK"
	// ErrBackupRepoUnreachable is a configured backup repository that could not
	// be read: it may exist and hold every snapshot the user has, but this
	// request could not see it. It is deliberately NOT ErrNotFound — the
	// snapshot listing used to answer this state with an empty 200, which reads
	// as "you have never taken a backup" and invites the user to initialise a
	// repository over one that is merely unreachable (agent-os-81vr).
	//
	// It carries 503 rather than 500: the request is well-formed and the server
	// is healthy, a dependency is not, and 503 is what makes the failure
	// retryable to a client that reads status classes.
	ErrBackupRepoUnreachable = "BACKUP_REPO_UNREACHABLE"
	// ErrBackupRepoUninitialized is a backup repository that does not exist
	// yet: the probe reached the configured location and found nothing there.
	// It is emphatically NOT ErrBackupRepoUnreachable — the recoveries are
	// opposites. Creating a repository is correct here and destructive-adjacent
	// there, which is the whole distinction agent-os-81vr introduced RepoState
	// to carry.
	//
	// It carries 409 rather than 404 or 500. The snapshot listing used to
	// answer this state with 500 "Failed to list snapshots" (agent-os-rg8h),
	// because a comment claimed the state fell through to an empty list and it
	// did not: restic exits 10, which becomes an error. 409 is the house shape
	// for "the server is fine, this resource is in a state that refuses the
	// request", it is what the same handler file already answers for
	// BACKUP_UNAVAILABLE, and it is the one status class the frontend's
	// classifyError passes `message` through intact. It used to be the ONLY
	// status class that did — 404 replaced the message with a fixed string and
	// a 5xx discarded it — but agent-os-mc4i fixed both arms, so that is no
	// longer a reason to prefer any status over another. 409 remains the
	// correct status here entirely on its own merits: "the server is fine,
	// this resource is in a state that refuses the request", matching what
	// this handler file already answers for BACKUP_UNAVAILABLE.
	ErrBackupRepoUninitialized = "BACKUP_REPO_UNINITIALIZED"
	// ErrGitBareRepo is an operation that needs a work tree asked of a bare
	// repository, which has none. 409: the server is fine, the repository is
	// in a state that refuses the request (agent-os-00zg).
	ErrGitBareRepo = "GIT_BARE_REPO"
	// ErrStackDeleteNestedStacks refuses a stack delete that would remove its
	// whole folder while other registered stacks live in subfolders of it:
	// their files would go and their rows would stay (agent-os-0s68). 409: the
	// request is fine, the folder's contents refuse it until those go first.
	ErrStackDeleteNestedStacks = "STACK_DELETE_NESTED_STACKS"

	// The commit and push refusals (agent-os-k81p.2). Every one is a 4xx or a
	// 502 and none is a 401: the frontend logs the user out on a 401 whose
	// body has no top-level code (agent-os-318), and these render through an
	// ActionResult whose code sits in details.code.
	//
	// ErrGitPushDisabled: the directory's push switch is off, or it has no
	// directories row. 403, nothing written.
	ErrGitPushDisabled = "GIT_PUSH_DISABLED"
	// ErrGitIdentityMissing: git_author_name or git_author_email is unset or
	// blank. 400, nothing written.
	ErrGitIdentityMissing = "GIT_IDENTITY_MISSING"
	// ErrGitNoUpstream: the branch tracks nothing, or tracks the local
	// repository itself (branch.<b>.remote "."). 409; Capstan never sets one.
	ErrGitNoUpstream = "GIT_NO_UPSTREAM"
	// ErrGitRemoteAhead: the remote has commits this branch lacks. 409, pull
	// first; a commit made in the same request is kept.
	ErrGitRemoteAhead = "GIT_REMOTE_AHEAD"
	// ErrGitEnvConfirmRequired: the file list names an env file and the
	// request did not set confirmEnv. 400, nothing written.
	ErrGitEnvConfirmRequired = "GIT_ENV_CONFIRM_REQUIRED"
	// ErrGitSSHUnsupported: the push would go over SSH but no SSH key applies
	// to the directory: its auth is https, or it inherits and no global key
	// is configured (agent-os-z0gl, D164). 409, refused before the push runs.
	ErrGitSSHUnsupported = "GIT_SSH_UNSUPPORTED"
	// ErrGitSSHKeyUnusable: the pull or push would go over SSH and the key
	// that applies cannot be used: missing, unreadable, readable by other
	// users, or a path ssh would misread (agent-os-z0gl). 409, refused before
	// the network git child; the message names the path and the fix.
	ErrGitSSHKeyUnusable = "GIT_SSH_KEY_UNUSABLE"
	// ErrGitDetachedHead: HEAD names no branch, so a commit would be
	// reachable from HEAD alone and a push has no branch to send. 409.
	ErrGitDetachedHead = "GIT_DETACHED_HEAD"
	// ErrGitOperationInProgress: a merge, cherry-pick or revert is underway in
	// the repository, or another git process holds its index lock. 409.
	ErrGitOperationInProgress = "GIT_OPERATION_IN_PROGRESS"
	// ErrGitPushRejected: the remote refused the update for a reason other
	// than being ahead (a protected branch, a declining hook). 409.
	ErrGitPushRejected = "GIT_PUSH_REJECTED"
	// ErrGitPushURLUnsupported: the push URL uses a transport Capstan does not
	// push over (git://, a <transport>:: helper). 409.
	ErrGitPushURLUnsupported = "GIT_PUSH_URL_UNSUPPORTED"
)

type AppError struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
	Status  int         `json:"-"`
	// Cause is the underlying error that produced this AppError, kept out of
	// the wire format on purpose (agent-os-2mhb): the response body
	// deliberately withholds it from the client, but handlers/respond.go's
	// logServerFault reads it so a 5xx still leaves a diagnostic record
	// instead of only the sanitised Message constant. Never serialized.
	Cause error `json:"-"`
}

func (e *AppError) Error() string {
	return e.Message
}

// Unwrap exposes Cause to errors.Is/errors.As/errors.Unwrap so a caller that
// wraps or inspects the chain (rather than reading Cause directly) still
// finds it.
func (e *AppError) Unwrap() error {
	return e.Cause
}

func NewAppError(status int, code string, message string) *AppError {
	return &AppError{
		Code:    code,
		Message: message,
		Status:  status,
	}
}

func NewAppErrorWithDetails(status int, code string, message string, details interface{}) *AppError {
	return &AppError{
		Code:    code,
		Message: message,
		Details: details,
		Status:  status,
	}
}

// NewAppErrorWithCause is like NewAppError but keeps the underlying error
// that produced it, so a 5xx still lets logServerFault emit the real failure
// even when the AppError itself carries only a sanitised, client-facing
// Message (agent-os-2mhb).
//
// Because Unwrap exposes Cause, errors.Is/errors.As on the returned AppError
// now see straight through it to cause. Attach the cause at the HTTP
// response boundary (handlers/respond.go), not inside a service or database
// call that returns the error upward — an AppError minted early and passed
// through several layers turns every later errors.Is/As check against it
// into an implicit check against cause too, which is easy to get wrong.
func NewAppErrorWithCause(status int, code string, message string, cause error) *AppError {
	return &AppError{
		Code:    code,
		Message: message,
		Status:  status,
		Cause:   cause,
	}
}
