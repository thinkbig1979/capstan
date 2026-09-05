package services

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestDefinitiveStatusError_TraversesWrap pins agent-os-5s9j.
//
// GetStatus classified getStatusGoGit's error with a bare
// `err.(*models.AppError)` type assertion, which does not traverse a `%w` wrap.
// handlers/respond.go fixed the identical form with errors.As and
// handlers/respond_test.go records that its own test was seen failing first
// against the assertion; this file is the same arm for the site that was left
// behind.
//
// SEEN FAILING FIRST, with the build green, by running this test against a
// git.go whose definitiveStatusError body is the assertion form. Reproduce
// (absolute paths are mandatory for -overlay, a relative key is silently
// ignored and yields a fake pass):
//
//	go test ./internal/services/ -run TestDefinitiveStatusError -overlay=<abs>/overlay.json
//
// The wrapped subtests failed on their assertions ("wrapped AppError was not
// recognised"), not on a build error; `go build ./...` under the same overlay
// exited 0.
//
// Both sides are asserted on the one instrument: the shapes that MUST be
// recognised and the shapes that MUST NOT be.
func TestDefinitiveStatusError_TraversesWrap(t *testing.T) {
	appErr := models.NewAppError(http.StatusNotFound, models.ErrGitNotRepo, "Not a git repository")

	cases := []struct {
		name       string
		err        error
		wantOK     bool
		wantStatus int
		why        string
	}{
		{
			name:       "wrapped once",
			err:        fmt.Errorf("open repository: %w", appErr),
			wantOK:     true,
			wantStatus: http.StatusNotFound,
			why:        "the bead: a type assertion misses this and the definitive error is discarded",
		},
		{
			name:       "wrapped twice",
			err:        fmt.Errorf("get status: %w", fmt.Errorf("open repository: %w", appErr)),
			wantOK:     true,
			wantStatus: http.StatusNotFound,
			why:        "errors.As walks the whole chain, not just one link",
		},
		{
			name:       "wrapped with errors.Join",
			err:        errors.Join(errors.New("probe failed"), appErr),
			wantOK:     true,
			wantStatus: http.StatusNotFound,
			why:        "a multi-error chain is still a chain",
		},
		{
			name:       "bare AppError",
			err:        appErr,
			wantOK:     true,
			wantStatus: http.StatusNotFound,
			why:        "CONTROL: the case that already worked must keep working",
		},
		{
			name:   "plain error",
			err:    errors.New("failed to get HEAD: reference not found"),
			wantOK: false,
			why:    "MUST NOT fire: this is the real go-git error that has to reach the CLI fallback",
		},
		{
			name:   "wrapped plain error",
			err:    fmt.Errorf("get status: %w", errors.New("boom")),
			wantOK: false,
			why:    "MUST NOT fire: errors.As must not match on wrapping alone",
		},
		{
			name:   "nil",
			err:    nil,
			wantOK: false,
			why:    "MUST NOT fire",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := definitiveStatusError(tc.err)
			if ok != tc.wantOK {
				t.Fatalf("definitiveStatusError(%v) ok = %v, want %v — %s", tc.err, ok, tc.wantOK, tc.why)
			}
			if !tc.wantOK {
				if got != nil {
					t.Fatalf("not recognised but returned %#v, want nil", got)
				}
				return
			}
			if got.Status != tc.wantStatus {
				t.Errorf("status = %d, want %d — the typed status must survive the wrap", got.Status, tc.wantStatus)
			}
			if got.Code != models.ErrGitNotRepo {
				t.Errorf("code = %q, want %q", got.Code, models.ErrGitNotRepo)
			}
		})
	}
}

// TestGetStatus_DefinitiveErrorSurvives is the end-to-end arm, and it is
// recorded here that it does NOT discriminate the branch.
//
// MEASURED, not assumed: run under an overlay whose GetStatus has the
// definitive-error branch DELETED outright, this test and the older
// TestGetStatus_NonGitRepoReturns404 both still pass (build exit 0, test exit
// 0), because getStatusCLI's gitFailure probe mints the same 404
// GIT_NOT_REPO. That coincidence is the whole reason agent-os-5s9j is latent,
// and it is why the wrapped arm above has to be tested at the seam instead.
// This arm is a regression guard on the reachable path, not evidence about the
// fix.
func TestGetStatus_DefinitiveErrorSurvives(t *testing.T) {
	svc := NewGitService(&config.Config{}, nil)

	_, err := svc.GetStatus(t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a non-git directory, got nil")
	}

	var appErr *models.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *models.AppError, got %T: %v", err, err)
	}
	if appErr.Status != http.StatusNotFound || appErr.Code != models.ErrGitNotRepo {
		t.Errorf("got %d/%s, want 404/%s", appErr.Status, appErr.Code, models.ErrGitNotRepo)
	}
}
