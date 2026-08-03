package models

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
