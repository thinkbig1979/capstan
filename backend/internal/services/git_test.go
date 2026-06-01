package services

import (
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestGetStatus_NonGitRepoReturns404 reproduces the bug where a stack directory
// that is not a git repository made GET /api/v1/git return 500: getStatusGoGit
// produced the correct typed 404, but GetStatus fell back to the CLI path, which
// returned a generic error that HandleError mapped to 500. GetStatus must surface
// a typed *models.AppError (404, GIT_NOT_REPO) so the frontend can treat it as
// "no git repo" instead of a server error.
func TestGetStatus_NonGitRepoReturns404(t *testing.T) {
	s := &GitService{}

	_, err := s.GetStatus(t.TempDir()) // fresh empty dir — not a git repo
	if err == nil {
		t.Fatal("expected an error for a non-git directory, got nil")
	}

	appErr, ok := err.(*models.AppError)
	if !ok {
		t.Fatalf("expected *models.AppError, got %T: %v", err, err)
	}
	if appErr.Status != 404 {
		t.Errorf("expected status 404, got %d", appErr.Status)
	}
	if appErr.Code != models.ErrGitNotRepo {
		t.Errorf("expected code %q, got %q", models.ErrGitNotRepo, appErr.Code)
	}
}
