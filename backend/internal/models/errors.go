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
