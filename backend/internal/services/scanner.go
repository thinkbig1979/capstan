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
// resolution is a separate concern (see resolveEffectiveRoot below, used by
// buildDirectoryRecord's effectiveRoot fallback for the rootDir="" case).
// Taking the root as a parameter keeps that resolution logic from flowing
// through ID minting, so a future change to how roots are resolved cannot
// silently change what StackID does with an already-resolved one.
//
// primaryRoot and extraRoots are config.StacksDir and config.ExtraStacksDirs.
// They are passed as separate parameters rather than as one GetAllStacksDirs
// slice because the primary root is privileged (see StackIDRootPrefix) and
// inferring which entry is primary from its position in a combined slice would
// make that guarantee depend on a caller's slice-building order.
//
// pathID is the stack's path relative to root with separators flattened to "-";
// name is the compose profile ("default" for compose.yaml).
func StackID(root, primaryRoot string, extraRoots []string, pathID, name string) string {
	return fmt.Sprintf("%s~%s:%s", StackIDRootPrefix(root, primaryRoot, extraRoots), pathID, name)
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
// joins it because it is already legal in this ID's charset (see
// middleware.ValidateStackID), is safe unescaped in a URL path, and sits before
// the final "~" that the frontend splits on to recover a display name.
//
// # The primary root is never suffixed
//
// Because the prefix depends on the SET of configured roots, adding a root can
// in principle change the ID of a stack that already exists. primaryRoot
// (config.StacksDir) is therefore exempt unconditionally: it keeps the bare
// basename even when an extra root collides with it, and the extra takes the
// suffix instead. Adding, removing or reordering extra roots consequently never
// re-IDs anything under the primary root, which is where single-root
// deployments — the overwhelming majority — keep everything.
//
// # Known residual: extra roots are not stable under config changes
//
// Two EXTRA roots that collide with each other, or an extra root that only
// starts colliding once a LATER extra root is added, still flip from a bare
// prefix to a suffixed one. Changing the set of configured roots can therefore
// change the ID of stacks under a non-primary root.
//
// That is worse than a rename, because nothing cleans up after it: the ID is
// not what pruning keys on. pruneStaleStacks removes rows by DIRECTORY
// existence, and the directory is still there, so the old row survives while
// the scan upserts a second row under the new ID — one stack, two rows, with
// all six TEXT columns above still referencing the old ID. Filed separately;
// deliberately not solved here, because solving it means either persisting the
// root->prefix mapping or a migration, both out of scope for the collision fix.
func StackIDRootPrefix(root, primaryRoot string, extraRoots []string) string {
	base := filepath.Base(root)
	canonical := canonicalRoot(root)

	if canonical == canonicalRoot(primaryRoot) {
		return base
	}

	if filepath.Base(primaryRoot) == base {
		return base + "." + rootFingerprint(canonical)
	}
	for _, other := range extraRoots {
		if filepath.Base(other) == base && canonicalRoot(other) != canonical {
			return base + "." + rootFingerprint(canonical)
		}
	}

	return base
}

func rootFingerprint(canonicalRoot string) string {
	sum := sha256.Sum256([]byte(canonicalRoot))
	return hex.EncodeToString(sum[:4])
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

	return s.pruneStaleIDStacks(directories, activeDirs)
}

// pruneStaleIDStacks removes rows left behind when a stack's computed ID
// changes while its directory stays put (agent-os-ufv). StackIDRootPrefix's
// "Known residual" comment above explains when this happens: two EXTRA roots
// that collide with each other, or an extra that starts colliding once a
// later extra is added. The directory-existence pass above does not catch
// this — the directory is still active — so the OLD row (minted under the
// previous ID) survives while ScanDirectoryWithRoot upserts a NEW row under
// the current ID: one stack, two rows.
//
// THE HAZARD this guards against: mistaking "the scanner didn't visit this
// directory on this pass" (temporarily unreadable compose file, a scan_depth
// change, a permission error skipping a subtree) for "this row's ID is
// stale". Recomputing an expected ID and deleting any row that fails to match
// it would delete live stacks the scan simply hadn't reached yet.
//
// The safe predicate: only delete a row when another row for the exact same
// (Directory, ComposeFile) pair already carries the ID the scanner would mint
// right now. That pairing is unique per logical stack — ComposeFile fixes
// extractStackName, and the directory's current root fixes the rest of
// StackID — so two rows sharing it can only come from an ID change, never
// from an unrelated multi-compose-file directory (agent-os-w8o), which has
// distinct ComposeFile values and therefore never shares the key. And a
// "currently correct" sibling row only exists if THIS scan (or a prior one)
// actually wrote it, which only happens for directories the scanner visited —
// so a not-yet-visited directory's lone row is left alone, never treated as
// stale on recomputation alone.
//
// ACCEPTED COST (agent-os-ufv, deliberately not solved here): the six
// unenforced TEXT columns holding a stack ID (action_log.stack_id,
// cached_updates.stack_id, update_history.stack_id, backup_run_items.stack_id,
// auto_update_policies.target_id, backup_policies.target_id — see
// StackIDRootPrefix's doc comment) still reference the deleted row's old ID
// and are orphaned, not carried across. The MigrateStackIDsToRootPrefixed
// re-ID-plus-carry-across approach was considered and not chosen.
func (s *ScannerService) pruneStaleIDStacks(directories []models.Directory, activeDirs map[string]bool) error {
	dirRoots := make(map[string]string, len(directories))
	for _, dir := range directories {
		if activeDirs[dir.Path] {
			dirRoots[dir.Path] = dir.RootDir
		}
	}

	stacks, err := s.db.ListStacks()
	if err != nil {
		return err
	}

	type dirFileKey struct {
		dir  string
		file string
	}
	byDirFile := make(map[dirFileKey][]models.Stack)
	for _, stack := range stacks {
		if !activeDirs[stack.Directory] {
			continue
		}
		key := dirFileKey{stack.Directory, stack.ComposeFile}
		byDirFile[key] = append(byDirFile[key], stack)
	}

	for key, group := range byDirFile {
		if len(group) < 2 {
			continue
		}

		effectiveRoot, ok := dirRoots[key.dir]
		if !ok || effectiveRoot == "" {
			continue
		}

		expectedID := s.expectedStackID(key.dir, effectiveRoot, key.file)

		hasExpected := false
		for _, st := range group {
			if st.ID == expectedID {
				hasExpected = true
				break
			}
		}
		if !hasExpected {
			// No row in this group is proven current yet — the scan may not
			// have visited this directory this pass. Leave every row alone.
			continue
		}

		for _, st := range group {
			if st.ID == expectedID {
				continue
			}
			if delErr := s.db.DeleteStack(st.ID); delErr != nil {
				slog.Warn("Failed to delete stale-ID stack row", "stackID", st.ID, "directory", st.Directory, "error", delErr)
			}
		}
	}

	return nil
}

// expectedStackID recomputes the ID the scanner would mint right now for a
// stack, given the directory's currently-recorded effective root and a
// compose filename. It mirrors the relPath/stackPathID derivation in
// ScanDirectoryWithRoot exactly (see below), factored out so
// pruneStaleIDStacks can ask "is this row still what the scanner would
// produce" without duplicating that logic or re-deriving root resolution.
func (s *ScannerService) expectedStackID(dirPath, effectiveRoot, composeFile string) string {
	relPath := ""
	if effectiveRoot != "" {
		if rel, err := filepath.Rel(effectiveRoot, dirPath); err == nil {
			relPath = rel
		}
	}
	if relPath == "" {
		relPath = filepath.Base(dirPath)
	}
	stackPathID := strings.ReplaceAll(relPath, string(filepath.Separator), "-")
	name := extractStackName(composeFile)
	return StackID(effectiveRoot, s.config.StacksDir, s.config.ExtraStacksDirs, stackPathID, name)
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

// resolveEffectiveRoot picks which configured root contains path, for the
// rootDir="" case: ScanDirectory (and through it, watcher.go's performRescan
// on every debounced file change) does not know which configured root it was
// called for and must work it out itself.
//
// Containment is tested with filepath.Rel plus a ".." check — the same idiom
// handlers/stack_crud.go's Create path-traversal guard uses — rather than a
// bare strings.HasPrefix, which has no path-separator boundary and so would
// match a root like ".../stacks" against an unrelated sibling directory like
// ".../stacks-extra" (agent-os-509).
//
// Multiple configured roots can legitimately contain the same path when one
// root is nested inside another (e.g. ".../stacks" and ".../stacks/team" are
// both configured). The LONGEST matching root wins, since it is always the
// most specific one regardless of the order roots appear in config — a first-
// match rule would make the outcome depend on GetAllStacksDirs' order, which
// is config-file order, not path specificity.
func resolveEffectiveRoot(path string, roots []string) string {
	best := ""
	for _, root := range roots {
		if root == "" || !rootContainsPath(root, path) {
			continue
		}
		if len(root) > len(best) {
			best = root
		}
	}
	return best
}

// rootContainsPath reports whether path is root itself or lies strictly
// beneath it, using filepath.Abs + filepath.Rel so relative and
// trailing-separator spellings of root compare correctly (mirrors
// handlers/stack_crud.go's Create guard).
func rootContainsPath(root, path string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	if absRoot == absPath {
		return true
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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
		effectiveRoot = resolveEffectiveRoot(path, s.config.GetAllStacksDirs())
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

	// seenNames tracks, per extracted stack name, the filename that already
	// claimed it — so a later file mapping to the same name can be skipped
	// with a warning that names both files (see the guard below).
	seenNames := make(map[string]string)

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

			// extractStackName strips both the "compose." and
			// "docker-compose." prefixes, so e.g. compose.api.yaml and
			// docker-compose.api.yml both yield "api". Without this guard the
			// second file's UpsertStack (INSERT OR REPLACE) would silently
			// overwrite the first file's row, leaving the other compose file
			// on disk but untracked: invisible in the UI, never brought
			// down, and left behind on delete (agent-os-4yy). This mirrors
			// the "default" handling that already existed here, generalized
			// to every stack name.
			//
			// The winner is whichever file the patterns loop above reaches
			// first, which is deterministic and NOT directory-read order:
			// the patterns slice fixes compose*.yaml > compose*.yml >
			// docker-compose*.yaml > docker-compose*.yml (Docker Compose's
			// own file precedence), and filepath.Glob sorts each pattern's
			// matches alphabetically. So the outcome is stable across
			// filesystems.
			if winner, seen := seenNames[name]; seen {
				slog.Warn("Duplicate stack name, skipping", "directory", path, "name", name, "kept", winner, "skipped", filename)
				continue
			}

			seenNames[name] = filename

			envFile := determineEnvFile(path, name)

			stackPathID := strings.ReplaceAll(relPath, string(filepath.Separator), "-")
			stackID := StackID(effectiveRoot, s.config.StacksDir, s.config.ExtraStacksDirs, stackPathID, name)

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
