package services

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// StackID is the single producer of stack IDs. Both the scanner (discovering a
// compose file on disk) and POST /api/v1/stacks (creating one) must mint the
// same ID for the same stack, or a create is immediately shadowed by the next
// scan. They agreed by coincidence of two independent format strings until
// agent-os-elo; they agree by construction now.
//
// root MUST already be resolved to one of the configured stacks roots. This
// helper deliberately does NOT work out which root a path belongs to: that
// resolution is done with an unanchored strings.HasPrefix (scanner.go's
// effectiveRoot fallback), so /srv/stacks matches paths under /srv/stacks-extra
// — tracked as agent-os-509. Taking the root as a parameter keeps that defect
// from flowing through ID minting.
//
// pathID is the stack's path relative to root with separators flattened to "-";
// name is the compose profile ("default" for compose.yaml).
func StackID(root string, allRoots []string, pathID, name string) string {
	return fmt.Sprintf("%s~%s:%s", StackIDRootPrefix(root, allRoots), pathID, name)
}

// StackIDRootPrefix returns the root component of a stack ID.
//
// Normally that is just the root's basename, which is what every stack ID in
// the field already carries. Two configured roots CAN share a basename
// (/a/stacks and /b/stacks), and then the basename alone collides: UpsertStack
// is an INSERT OR REPLACE, so the second stack silently repoints the first's
// row at its own directory and the first becomes unreachable by ID.
//
// The disambiguator is therefore applied to colliding roots ONLY, and every
// other root keeps its prefix byte-for-byte. Re-IDing unconditionally would be
// far more expensive than it looks: stack IDs are copied into six plain TEXT
// columns with no foreign key to keep them honest (action_log.stack_id,
// cached_updates.stack_id, update_history.stack_id, backup_run_items.stack_id,
// auto_update_policies.target_id, backup_policies.target_id) and into frontend
// URLs users bookmark. Collision-only touches none of that, and needs no
// migration.
//
// The suffix is a fingerprint of the root's own path rather than its position
// in the config, so reordering EXTRA_STACKS_DIRS does not re-ID anything. "."
// joins it because it is already legal in this ID's charset, is safe unescaped
// in a URL path, and sits before the final "~" that the frontend splits on to
// recover a display name.
func StackIDRootPrefix(root string, allRoots []string) string {
	base := filepath.Base(root)
	canonical := canonicalRoot(root)

	for _, other := range allRoots {
		if filepath.Base(other) != base || canonicalRoot(other) == canonical {
			continue
		}
		sum := sha256.Sum256([]byte(canonical))
		return base + "." + hex.EncodeToString(sum[:4])
	}

	return base
}

// canonicalRoot puts a root in a comparable form. It intentionally stops at
// filepath.Abs: resolving symlinks would let a root compare equal to a
// configured root it is not spelled the same as, and the callers' own
// validation (handlers.isValidStacksDir) matches on Abs too.
func canonicalRoot(root string) string {
	if abs, err := filepath.Abs(root); err == nil {
		return abs
	}
	return filepath.Clean(root)
}

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
			// The next scan re-evaluates and retries this delete, so a
			// single failure isn't fatal to the sweep — but a delete that
			// keeps failing drifts the DB from disk indefinitely with
			// nothing to notice it, so surface it.
			if delErr := s.db.DeleteDirectory(dir.Path); delErr != nil {
				slog.Warn("Failed to delete stale directory row", "path", dir.Path, "error", delErr)
			}
		}
	}

	stacks, err := s.db.ListStacks()
	if err != nil {
		return err
	}

	for _, stack := range stacks {
		if !activeDirs[stack.Directory] {
			// See the DeleteDirectory comment above: retried next scan, but
			// a persistent failure should not go unnoticed.
			if delErr := s.db.DeleteStack(stack.ID); delErr != nil {
				slog.Warn("Failed to delete stale stack row", "stackID", stack.ID, "error", delErr)
			}
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

// directoryRecord is what RegisterDirectory and ScanDirectoryWithRoot both
// derive from a path on disk: the models.Directory row plus the git fields
// ScanDirectoryWithRoot also needs when it builds any stack it discovers
// underneath. Extracted so the two callers share one source of truth instead
// of two copies of the same git/root-path probing drifting apart.
type directoryRecord struct {
	directory models.Directory
	isGitRepo bool
	gitBranch string
}

func (s *ScannerService) buildDirectoryRecord(path string, rootDir string) directoryRecord {
	dirName := filepath.Base(path)
	isGitRepo := false
	gitRemote := ""
	gitBranch := ""

	gitPath := filepath.Join(path, ".git")
	if _, err := os.Stat(gitPath); err == nil {
		isGitRepo = true

		headPath := filepath.Join(gitPath, "HEAD")
		//nolint:gosec // path is reached only by recursing from the configured stacks directories (ScanAll -> scanDirectoryRecursive), never external input
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

	return directoryRecord{
		directory: models.Directory{
			Path:      path,
			Name:      dirName,
			RootDir:   effectiveRoot,
			IsGitRepo: isGitRepo,
			GitRemote: gitRemote,
			GitBranch: gitBranch,
		},
		isGitRepo: isGitRepo,
		gitBranch: gitBranch,
	}
}

// RegisterDirectory ensures a directories row exists for path without scanning
// it for compose files or touching the stacks table. It exists so a caller that
// is about to insert a stacks row referencing this path (stacks.directory has
// an FK to directories.path, ON DELETE CASCADE — see migrations.go) can
// satisfy that FK first, without paying for a full ScanDirectoryWithRoot pass
// (agent-os-jcu: POST /api/v1/stacks was inserting the stack row for a
// brand-new directory before any row for that directory existed, which
// 500'd under pool-wide foreign_keys enforcement).
//
// created reports whether this call inserted the row, so the caller can undo
// its own registration via UnregisterDirectory without touching a row that was
// already there. That distinction matters because UnregisterDirectory cascades:
// see its doc comment.
func (s *ScannerService) RegisterDirectory(path string, rootDir string) (created bool, err error) {
	if _, err := os.ReadDir(path); err != nil {
		return false, err
	}
	rec := s.buildDirectoryRecord(path, rootDir)
	return s.db.InsertDirectoryIfAbsent(rec.directory)
}

// UnregisterDirectory removes the directories row for path. It is the
// rollback counterpart to RegisterDirectory in ONE case only: where that call
// reported created == true. If a caller registers the directory to satisfy the
// stacks FK, inserts it itself, and then fails to insert the stack row, this
// undoes the registration so a partial Create does not leave a directory row
// with no stack behind it.
//
// It is NOT a general undo, and calling it for a row this caller did not insert
// is a data-loss bug. stacks.directory has ON DELETE CASCADE onto
// directories.path, so removing the row also removes EVERY stacks row whose
// directory equals path — and one directory legitimately holds several stacks,
// one per compose file (agent-os-w8o).
func (s *ScannerService) UnregisterDirectory(path string) error {
	return s.db.DeleteDirectory(path)
}

func (s *ScannerService) ScanDirectoryWithRoot(path string, rootDir string) error {
	_, err := os.ReadDir(path)
	if err != nil {
		return err
	}

	rec := s.buildDirectoryRecord(path, rootDir)
	dirName := rec.directory.Name
	isGitRepo := rec.isGitRepo
	gitBranch := rec.gitBranch
	effectiveRoot := rec.directory.RootDir

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

	if err := s.db.UpsertDirectory(rec.directory); err != nil {
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

			stackPathID := strings.ReplaceAll(relPath, string(filepath.Separator), "-")
			stackID := StackID(effectiveRoot, s.config.GetAllStacksDirs(), stackPathID, name)

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
