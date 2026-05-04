package services

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
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
	allDirs := s.config.GetAllStacksDirs()
	slog.Info("Starting directory scan", "stacksDirs", allDirs)

	_, err = os.Stat(filepath.Join(s.config.DataDir, "global.env"))
	hasGlobalEnv = !os.IsNotExist(err)

	scanDepth := 1
	if s.db != nil {
		if depthStr, dbErr := s.db.GetSetting("scan_depth"); dbErr == nil && depthStr != "" {
			if v, parseErr := strconv.Atoi(depthStr); parseErr == nil && v >= 1 {
				scanDepth = v
			}
		}
	}

	for _, stacksDir := range allDirs {
		s.scanDirectoryRecursive(stacksDir, stacksDir, scanDepth, 1)
	}

	if err := s.pruneStaleStacks(); err != nil {
		slog.Warn("Failed to prune stale stacks", "error", err)
	}

	slog.Info("Directory scan complete", "scanDepth", scanDepth)
	return hasGlobalEnv, nil
}

func (s *ScannerService) scanDirectoryRecursive(path string, rootDir string, maxDepth int, currentDepth int) {
	entries, err := os.ReadDir(path)
	if err != nil {
		slog.Warn("Failed to read directory", "path", path, "error", err)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		dirPath := filepath.Join(path, entry.Name())

		if err := s.ScanDirectoryWithRoot(dirPath, rootDir); err != nil {
			slog.Warn("Failed to scan directory", "path", dirPath, "error", err)
			continue
		}

		if currentDepth < maxDepth {
			s.scanDirectoryRecursive(dirPath, rootDir, maxDepth, currentDepth+1)
		}
	}
}

func (s *ScannerService) pruneStaleStacks() error {
	allDirs := s.config.GetAllStacksDirs()

	scanDepth := 1
	if s.db != nil {
		if depthStr, err := s.db.GetSetting("scan_depth"); err == nil && depthStr != "" {
			if v, parseErr := strconv.Atoi(depthStr); parseErr == nil && v >= 1 {
				scanDepth = v
			}
		}
	}

	activeDirs := make(map[string]bool)
	for _, stacksDir := range allDirs {
		collectActiveDirs(stacksDir, scanDepth, 1, activeDirs)
	}

	directories, err := s.db.ListDirectories()
	if err != nil {
		return err
	}
	for _, dir := range directories {
		if !activeDirs[dir.Path] {
			s.db.DeleteDirectory(dir.Path)
		}
	}

	stacks, err := s.db.ListStacks()
	if err != nil {
		return err
	}

	for _, stack := range stacks {
		if !activeDirs[stack.Directory] {
			s.db.DeleteStack(stack.ID)
		}
	}

	return nil
}

func collectActiveDirs(path string, maxDepth int, currentDepth int, active map[string]bool) {
	active[path] = true
	entries, err := os.ReadDir(path)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dirPath := filepath.Join(path, entry.Name())
		active[dirPath] = true
		if currentDepth < maxDepth {
			collectActiveDirs(dirPath, maxDepth, currentDepth+1, active)
		}
	}
}

func (s *ScannerService) ScanDirectory(path string) error {
	return s.ScanDirectoryWithRoot(path, "")
}

func (s *ScannerService) ScanDirectoryWithRoot(path string, rootDir string) error {
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

	effectiveRoot := rootDir
	if effectiveRoot == "" {
		for _, stacksDir := range s.config.GetAllStacksDirs() {
			if strings.HasPrefix(path, stacksDir) {
				effectiveRoot = stacksDir
				break
			}
		}
	}

	relPath := ""
	if effectiveRoot != "" {
		rel, err := filepath.Rel(effectiveRoot, path)
		if err == nil {
			relPath = rel
		}
	}
	if relPath == "" {
		relPath = dirName
	}

	directory := models.Directory{
		Path:      path,
		Name:      dirName,
		RootDir:   effectiveRoot,
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

			rootPrefix := filepath.Base(effectiveRoot)
			stackPathID := strings.ReplaceAll(relPath, string(filepath.Separator), "-")
			stackID := fmt.Sprintf("%s~%s:%s", rootPrefix, stackPathID, name)

			var projectName string
			if name == "default" {
				projectName = dirName
			} else {
				projectName = fmt.Sprintf("%s-%s", dirName, strings.ReplaceAll(name, ":", "-"))
			}

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

			slog.Info("Discovered stack", "id", stackID, "project", projectName, "root", effectiveRoot)
		}
	}

	return nil
}

func extractStackName(filename string) string {
	name := filename

	name = strings.TrimSuffix(name, ".yaml")
	name = strings.TrimSuffix(name, ".yml")

	if name == "compose" || name == "docker-compose" {
		return "default"
	}

	name = strings.TrimPrefix(name, "compose.")
	name = strings.TrimPrefix(name, "docker-compose.")

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
