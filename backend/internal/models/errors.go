package models

// The two 401 codes are not interchangeable, and the frontend response
// interceptor (frontend/src/lib/api.ts) branches on the difference:
//
//   - ErrSessionExpired means "this session cannot be used" — no token, an
//     unusable token, or a session row that is gone or past its expiry. The
//     frontend logs the user out and navigates to /login on this code.
//     Only middleware.AuthMiddleware mints it.
//   - ErrUnauthorized means "the credential you just supplied is wrong", with
//     the session itself untouched: a wrong login password, a wrong current
//     password on change-password, a wrong password at the env-unlock prompt.
//     The frontend shows the message and leaves the user where they are.
//
// Reaching for ErrSessionExpired on a credential path recreates agent-os-318,
// where mistyping your own password bounced you to /login mid-session.
const (
	ErrUnauthorized          = "UNAUTHORIZED"
	ErrForbidden             = "FORBIDDEN"
	ErrNotFound              = "NOT_FOUND"
	ErrValidation            = "VALIDATION_ERROR"
	ErrComposeValidation     = "COMPOSE_VALIDATION_ERROR"
	ErrDockerUnavailable     = "DOCKER_UNAVAILABLE"
	ErrDockerOperation       = "DOCKER_OPERATION"
	ErrGitDirty              = "GIT_DIRTY"
	ErrGitConflict           = "GIT_CONFLICT"
	ErrGitNotRepo            = "GIT_NOT_REPO"
	ErrPathTraversal         = "PATH_TRAVERSAL"
	ErrDuplicateStack        = "DUPLICATE_STACK"
	ErrStackNotFound         = "STACK_NOT_FOUND"
	ErrSessionExpired        = "SESSION_EXPIRED"
	ErrSetupRequired         = "SETUP_REQUIRED"
	ErrSetupAlreadyDone      = "SETUP_ALREADY_DONE"
	ErrRateLimited           = "RATE_LIMITED"
	ErrEncryptionUnavailable = "ENCRYPTION_KEY_MISSING"
	ErrOperationInProgress   = "OPERATION_IN_PROGRESS"
)

type AppError struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
	Status  int         `json:"-"`
}

func (e *AppError) Error() string {
	return e.Message
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
