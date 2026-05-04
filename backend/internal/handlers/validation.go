package handlers

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/docker-manager/backend/internal/config"
	"github.com/docker-manager/backend/internal/models"
)

func validateStackPath(path string, cfg *config.Config) error {
	cleanPath := filepath.Clean(path)

	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return err
	}

	for _, stacksDir := range cfg.GetAllStacksDirs() {
		absStacksDir, err := filepath.Abs(stacksDir)
		if err != nil {
			continue
		}

		rel, err := filepath.Rel(absStacksDir, absPath)
		if err != nil {
			continue
		}

		if !strings.HasPrefix(rel, "..") && !strings.HasPrefix(rel, "/") {
			return nil
		}
	}

	return &models.AppError{
		Code:    models.ErrPathTraversal,
		Message: "Path is outside configured stacks directories",
		Status:  http.StatusBadRequest,
	}
}
