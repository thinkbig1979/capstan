package handlers

import (
	"net/http"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/pathutil"
)

// validateStackPath confirms path resolves inside one of the configured stacks
// directories. Containment is symlink-aware (pathutil.IsContained): a symlink
// under a stacks root that points elsewhere cannot be used to read or write
// host files outside the root (M2).
func validateStackPath(path string, cfg *config.Config) error {
	for _, stacksDir := range cfg.GetAllStacksDirs() {
		ok, err := pathutil.IsContained(stacksDir, path)
		if err != nil {
			continue
		}
		if ok {
			return nil
		}
	}

	return &models.AppError{
		Code:    models.ErrPathTraversal,
		Message: "Path is outside configured stacks directories",
		Status:  http.StatusBadRequest,
	}
}
