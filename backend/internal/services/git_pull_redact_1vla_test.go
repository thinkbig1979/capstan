package services

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-1vla: renderGitResult (handlers/git.go) finds the AppError with
// errors.As and renders its Message and Details, not Error(). redactErr
// masked only an AppError passed to it directly, so one wrapped by
// fmt.Errorf("...: %w") reached the response with the token in it. No pull
// route builds such an AppError with the token today (the one wrapped
// AppError on the route, objectMissing's, carries a constant message); this
// closes the gap at the display boundary rather than at each site.
func TestRedactErrMasksWrappedAppError_1vla(t *testing.T) {
	const tok = "tok-1vla-secret"
	cause := errors.New("cause-1vla")
	cases := map[string]*models.AppError{
		"token in message": {Code: "C", Status: 409, Message: "pull of " + tok + " refused", Cause: cause},
		"token in details": {Code: "C", Status: 409, Message: "pull refused", Details: "remote " + tok, Cause: cause},
	}
	for name, ae := range cases {
		wrapped := fmt.Errorf("failed to detect a bare repository: %w", ae)
		// Through the pull's display boundary, as PullVerified's defer does.
		ar, _ := redactPullResult(truth.Failed("git pull failed", wrapped), nil, tok)

		var got *models.AppError
		if !errors.As(ar.Err, &got) {
			t.Fatalf("%s: errors.As found no AppError in %v", name, ar.Err)
		}
		shown := fmt.Sprintf("%s %v %s", got.Message, got.Details, ar.Err.Error())
		if strings.Contains(shown, tok) {
			t.Errorf("%s: the rendered AppError still holds the token: message=%q details=%v error=%q", name, got.Message, got.Details, ar.Err.Error())
		}
		if got.Code != "C" || got.Status != 409 {
			t.Errorf("%s: code/status changed: %q %d", name, got.Code, got.Status)
		}
		// The rest of the chain is kept: gitExitCode and errors.Is still see it.
		if !errors.Is(ar.Err, cause) {
			t.Errorf("%s: the cause left the chain", name)
		}
		// The caller's AppError is not written.
		if !strings.Contains(ae.Message+fmt.Sprint(ae.Details), tok) {
			t.Errorf("%s: redactErr wrote the caller's AppError", name)
		}
	}
}
