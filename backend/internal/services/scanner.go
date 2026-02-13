package services

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker-manager/backend/internal/config"
	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
)

type ScannerService struct {
	config *config.Config
	db     *database.DB
}

func NewScannerService(cfg *config.Config, db *database.DB) *ScannerService {
	return &ScannerService{
		config: cfg,
		db:     db,
	}
}

func (s *ScannerService) ScanAll() (hasGlobalEnv bool, err error) {
	slog.Info("Starting directory scan", "stacksDir", s.config.StacksDir)

	entries, err := os.ReadDir(s.config.StacksDir)
	if err != nil {
		return false, err
	}

	_, err = os.Stat(filepath.Join(s.config.StacksDir, "global.env"))
	hasGlobalEnv = !os.IsNotExist(err)

	if err := s.db.ClearDirectories(); err != nil {
		return hasGlobalEnv, err
	}

	if err := s.db.ClearStacks(); err != nil {
		return hasGlobalEnv, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		dirPath := filepath.Join(s.config.StacksDir, entry.Name())

		if err := s.ScanDirectory(dirPath); err != nil {
			slog.Warn("Failed to scan directory", "path", dirPath, "error", err)
			continue
		}
	}

	slog.Info("Directory scan complete")
	return hasGlobalEnv, nil
}

func (s *ScannerService) ScanDirectory(path string) error {
	_, err := os.ReadDir(path)
	if err != nil {
		return err
	}

	dirName := filepath.Base(path)
	isGitRepo := false
	gitRemote := ""
	gitBranch := ""

	gitPath := filepath.Join(path, ".git")
	if _, err := os.Stat(gitPath); err == nil {
		isGitRepo = true

		headPath := filepath.Join(gitPath, "HEAD")
		if content, err := os.ReadFile(headPath); err == nil {
			headContent := string(content)
			if strings.HasPrefix(headContent, "ref: refs/heads/") {
				gitBranch = strings.TrimPrefix(headContent, "ref: refs/heads/")
				gitBranch = strings.TrimSpace(gitBranch)
			}
		}
	}

	directory := models.Directory{
		Path:      path,
		Name:      dirName,
		IsGitRepo: isGitRepo,
		GitRemote: gitRemote,
		GitBranch: gitBranch,
	}

	if err := s.db.UpsertDirectory(directory); err != nil {
		return err
	}

	patterns := []string{
		"compose*.yaml",
		"compose*.yml",
		"docker-compose*.yaml",
		"docker-compose*.yml",
	}

	seenNames := make(map[string]bool)

	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(path, pattern))
		if err != nil {
			continue
		}

		for _, match := range matches {
			filename := filepath.Base(match)

			if strings.HasPrefix(filename, ".") {
				continue
			}

			if strings.HasSuffix(filename, ".override.yaml") || strings.HasSuffix(filename, ".override.yml") {
				continue
			}

			name := extractStackName(filename)

			if seenNames[name] && name == "default" {
				slog.Warn("Duplicate default stack, skipping", "directory", path, "file", filename)
				continue
			}

			seenNames[name] = true

			envFile := determineEnvFile(path, name)

			stackID := fmt.Sprintf("%s:%s", dirName, name)
			projectName := fmt.Sprintf("%s-%s", dirName, strings.ReplaceAll(name, ":", "-"))

			stack := models.Stack{
				ID:          stackID,
				Directory:   path,
				ComposeFile: filename,
				EnvFile:     envFile,
				ProjectName: projectName,
				Status:      "unknown",
				IsGitRepo:   isGitRepo,
				GitBranch:   gitBranch,
			}

			if err := s.db.UpsertStack(stack); err != nil {
				return err
			}

			slog.Info("Discovered stack", "id", stackID, "project", projectName)
		}
	}

	return nil
}

func extractStackName(filename string) string {
	name := filename

	name = strings.TrimPrefix(name, "compose.")
	name = strings.TrimPrefix(name, "docker-compose.")
	name = strings.TrimSuffix(name, ".yaml")
	name = strings.TrimSuffix(name, ".yml")

	if name == "" {
		return "default"
	}

	return name
}

func determineEnvFile(dirPath, stackName string) string {
	if stackName == "default" {
		envPath := filepath.Join(dirPath, ".env")
		if _, err := os.Stat(envPath); err == nil {
			return ".env"
		}
		return ""
	}

	envPath := filepath.Join(dirPath, fmt.Sprintf(".env.%s", stackName))
	if _, err := os.Stat(envPath); err == nil {
		return fmt.Sprintf(".env.%s", stackName)
	}

	envPath = filepath.Join(dirPath, ".env")
	if _, err := os.Stat(envPath); err == nil {
		return ".env"
	}

	return ""
}
