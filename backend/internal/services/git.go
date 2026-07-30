package services

import (
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/filesystem"
)

// RedeployFailure records a stack redeploy that did not reach verified-running.
type RedeployFailure struct {
	StackID string `json:"stack"`
	Reason  string `json:"reason"`
}

// PullResult is the raw outcome of a git pull (no redeploy logic).
// The Redeploy path is handled separately via PullVerified on the DockerService.

type GitService struct {
	config *config.Config
	db     *database.DB
}

func NewGitService(cfg *config.Config, db *database.DB) *GitService {
	return &GitService{
		config: cfg,
		db:     db,
	}
}

func (s *GitService) GetStatus(dirPath string) (*models.GitStatusResult, error) {
	result, err := s.getStatusGoGit(dirPath)
	if err != nil {
		// A typed AppError (e.g. the not-a-git-repo 404 from openRepo) is
		// definitive — returning it as-is keeps GET /git a clean 404 instead of
		// masking it behind the CLI fallback, whose generic error becomes a 500.
		if appErr, ok := err.(*models.AppError); ok {
			return nil, appErr
		}
		slog.Debug("go-git failed, falling back to CLI", "path", dirPath, "error", err)
		return s.getStatusCLI(dirPath)
	}
	return result, nil
}

func (s *GitService) getStatusGoGit(dirPath string) (result *models.GitStatusResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			// Warn, not Debug. A panic in go-git is a bug in this code or in
			// the library, not a routine fallback condition — logging it at
			// Debug is how agent-os-r1a stayed invisible in production for as
			// long as it did. The CLI fallback still keeps the endpoint
			// answering; it just no longer does so silently.
			slog.Warn("go-git panicked, falling back to the git CLI",
				"path", dirPath, "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("go-git panic: %v", r)
		}
	}()

	repo, err := s.openRepo(dirPath)
	if err != nil {
		return nil, err
	}

	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	branchName := head.Name().Short()
	commitHash := head.Hash()

	commit, err := repo.CommitObject(commitHash)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	gitCommit := s.mapCommit(commit)

	worktree, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	status, err := worktree.Status()
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}

	dirty := !status.IsClean()
	dirtyCount := 0
	for _, s := range status {
		if s.Staging != git.Unmodified || s.Worktree != git.Unmodified {
			dirtyCount++
		}
	}

	ahead, behind, trackingBranch, remoteURL, err := s.getDivergence(repo, head)
	if err != nil {
		slog.Warn("Failed to get ahead/behind status", "error", err)
		ahead = 0
		behind = 0
		trackingBranch = ""
		remoteURL = ""
	}

	return &models.GitStatusResult{
		Branch:         branchName,
		Commit:         gitCommit,
		Dirty:          dirty,
		DirtyCount:     dirtyCount,
		Ahead:          ahead,
		Behind:         behind,
		RemoteURL:      remoteURL,
		TrackingBranch: trackingBranch,
	}, nil
}

func (s *GitService) getStatusCLI(dirPath string) (*models.GitStatusResult, error) {
	branch, err := s.gitCommand(dirPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		// Typed 404 (not generic) so the handler returns "not a git repo" rather
		// than a 500 when go-git fell back to here for a non-repo directory.
		return nil, models.NewAppError(404, models.ErrGitNotRepo, "Not a git repository")
	}

	commitHash, err := s.gitCommand(dirPath, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	subject, _ := s.gitCommand(dirPath, "log", "-1", "--format=%s")
	author, _ := s.gitCommand(dirPath, "log", "-1", "--format=%an")
	email, _ := s.gitCommand(dirPath, "log", "-1", "--format=%ae")
	dateStr, _ := s.gitCommand(dirPath, "log", "-1", "--format=%aI")

	shortHash := commitHash
	if len(shortHash) > 7 {
		shortHash = shortHash[:7]
	}

	dirty := false
	dirtyCount := 0
	if output, err := s.gitCommand(dirPath, "status", "--porcelain"); err == nil {
		trimmed := strings.TrimSpace(output)
		dirty = trimmed != ""
		if dirty {
			dirtyCount = len(strings.Split(trimmed, "\n"))
		}
	}

	ahead := 0
	behind := 0
	if output, err := s.gitCommand(dirPath, "rev-list", "--left-right", "--count", "@{upstream}...HEAD"); err == nil {
		parts := strings.Fields(output)
		if len(parts) == 2 {
			behind, _ = strconv.Atoi(parts[0])
			ahead, _ = strconv.Atoi(parts[1])
		}
	}

	remoteURL, _ := s.gitCommand(dirPath, "remote", "get-url", "origin")

	return &models.GitStatusResult{
		Branch: branch,
		Commit: &models.GitCommit{
			Hash:    commitHash,
			Short:   shortHash,
			Author:  author,
			Email:   email,
			Message: subject,
			Date:    dateStr,
		},
		Dirty:      dirty,
		DirtyCount: dirtyCount,
		Ahead:      ahead,
		Behind:     behind,
		RemoteURL:  remoteURL,
	}, nil
}

func (s *GitService) gitCommand(dirPath string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-c", "safe.directory=" + dirPath}, args...)...)
	cmd.Dir = dirPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w (%s)", args[0], err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func (s *GitService) Pull(dirPath string) (*models.PullResult, error) {
	return s.pullCLI(dirPath)
}

func (s *GitService) pullCLI(dirPath string) (*models.PullResult, error) {
	slog.Debug("Pulling git changes (CLI)", "path", dirPath)

	dirtyOutput, err := s.gitCommand(dirPath, "status", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("failed to check status: %w", err)
	}
	if strings.TrimSpace(dirtyOutput) != "" {
		return nil, models.NewAppError(400, models.ErrGitDirty, "Working directory has uncommitted changes")
	}

	previousCommit, err := s.gitCommand(dirPath, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	_, err = s.gitCommand(dirPath, "pull", "--ff-only")
	if err != nil {
		if strings.Contains(err.Error(), "Already up to date") || strings.Contains(err.Error(), "Already up-to-date") {
			return &models.PullResult{
				PreviousCommit: previousCommit,
				CurrentCommit:  previousCommit,
				ChangedFiles:   []string{},
			}, nil
		}
		return nil, models.NewAppErrorWithDetails(500, models.ErrGitConflict, "Failed to pull", err.Error())
	}

	currentCommit, _ := s.gitCommand(dirPath, "rev-parse", "HEAD")

	changedFiles := []string{}
	if previousCommit != currentCommit {
		diffOutput, err := s.gitCommand(dirPath, "diff", "--name-only", previousCommit, currentCommit)
		if err == nil && diffOutput != "" {
			changedFiles = strings.Split(diffOutput, "\n")
		}
	}

	return &models.PullResult{
		PreviousCommit: previousCommit,
		CurrentCommit:  currentCommit,
		ChangedFiles:   changedFiles,
	}, nil
}

// PullVerified performs a git pull and, if redeploy is requested, redeploys every
// affected stack using the verified lifecycle (RestartVerified / StartVerified).
// It returns a truth.ActionResult that never reports success when a redeploy failed:
//   - no new commits (HEAD unchanged) → no_change
//   - HEAD advanced, all redeploys verified-success → success
//   - HEAD advanced, ≥1 redeploy failed → partial (details.failedRedeploys)
//   - pull itself failed → failed
//
// docker may be nil; in that case redeploy is skipped even when requested.
func (s *GitService) PullVerified(dirPath string, redeploy bool, docker *DockerService) (truth.ActionResult, *models.PullResult) {
	pullResult, err := s.pullCLI(dirPath)
	if err != nil {
		return truth.Failed("git pull failed", err), nil
	}

	headAdvanced := pullResult.PreviousCommit != pullResult.CurrentCommit

	if !headAdvanced {
		return truth.NoChange("already up to date",
			truth.KV("commit", pullResult.CurrentCommit),
		), pullResult
	}

	// HEAD advanced; skip redeploy if not requested or docker unavailable.
	if !redeploy || docker == nil || len(pullResult.ChangedFiles) == 0 {
		return truth.Success("pulled new commits",
			truth.KV("previousCommit", pullResult.PreviousCommit),
			truth.KV("currentCommit", pullResult.CurrentCommit),
			truth.KV("changedFiles", pullResult.ChangedFiles),
		), pullResult
	}

	// Determine which stacks are affected by the changed files.
	stacks, err := s.db.ListStacksByDirectory(dirPath)
	if err != nil {
		// Can list stacks — treat as partial: pull succeeded but redeploy untried.
		return truth.Partial("pulled new commits but could not list stacks for redeploy",
			truth.KV("previousCommit", pullResult.PreviousCommit),
			truth.KV("currentCommit", pullResult.CurrentCommit),
			truth.KV("listError", err.Error()),
		), pullResult
	}

	var failures []RedeployFailure
	var redeployed []string

	for _, stack := range stacks {
		if !stackFilesChanged(stack, pullResult.ChangedFiles) {
			continue
		}
		slog.Info("Redeploying stack after git pull", "stackID", stack.ID)
		ar, _ := docker.RestartVerified(stack)
		if ar.Outcome == truth.OutcomeSuccess || ar.Outcome == truth.OutcomeNoChange {
			redeployed = append(redeployed, stack.ID)
		} else {
			failures = append(failures, RedeployFailure{
				StackID: stack.ID,
				Reason:  ar.Reason,
			})
		}
	}

	if len(failures) > 0 {
		return truth.Partial("pulled new commits but some stacks failed to redeploy",
			truth.KV("previousCommit", pullResult.PreviousCommit),
			truth.KV("currentCommit", pullResult.CurrentCommit),
			truth.KV("redeployedStacks", redeployed),
			truth.KV("failedRedeploys", failures),
		), pullResult
	}

	return truth.Success("pulled and redeployed",
		truth.KV("previousCommit", pullResult.PreviousCommit),
		truth.KV("currentCommit", pullResult.CurrentCommit),
		truth.KV("changedFiles", pullResult.ChangedFiles),
		truth.KV("redeployedStacks", redeployed),
	), pullResult
}

// stackFilesChanged reports whether any changed file matches the stack's compose or env file.
func stackFilesChanged(stack models.Stack, changedFiles []string) bool {
	for _, f := range changedFiles {
		if f == stack.ComposeFile || f == stack.EnvFile {
			return true
		}
	}
	return false
}

func (s *GitService) GetLog(dirPath string, limit, offset int) (*models.LogResult, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	return s.getLogCLI(dirPath, limit, offset)
}

func (s *GitService) getLogCLI(dirPath string, limit, offset int) (*models.LogResult, error) {
	totalStr, err := s.gitCommand(dirPath, "rev-list", "--count", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("failed to count commits: %w", err)
	}
	total, _ := strconv.Atoi(totalStr)

	skip := offset
	fetchCount := limit
	logFormat := "%H%n%h%n%an%n%ae%n%s%n%aI%n---COMMIT_SEP---"
	output, err := s.gitCommand(dirPath, "log", fmt.Sprintf("--skip=%d", skip), fmt.Sprintf("--max-count=%d", fetchCount), fmt.Sprintf("--format=%s", logFormat))
	if err != nil {
		return nil, fmt.Errorf("failed to get log: %w", err)
	}

	commits := []models.GitCommit{}
	entries := strings.Split(output, "---COMMIT_SEP---")
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		lines := strings.SplitN(entry, "\n", 6)
		if len(lines) < 6 {
			continue
		}
		commits = append(commits, models.GitCommit{
			Hash:    strings.TrimSpace(lines[0]),
			Short:   strings.TrimSpace(lines[1]),
			Author:  strings.TrimSpace(lines[2]),
			Email:   strings.TrimSpace(lines[3]),
			Message: strings.TrimSpace(lines[4]),
			Date:    strings.TrimSpace(lines[5]),
		})
	}

	hasMore := offset+len(commits) < total

	return &models.LogResult{
		Commits: commits,
		Total:   total,
		HasMore: hasMore,
	}, nil
}

func (s *GitService) GetDiff(dirPath string, commitHash string) (*models.DiffResult, error) {
	return s.getDiffCLI(dirPath, commitHash)
}

func (s *GitService) getDiffCLI(dirPath string, commitHash string) (*models.DiffResult, error) {
	logOutput, err := s.gitCommand(dirPath, "log", "-1", "--format=%H%n%h%n%an%n%ae%n%s%n%aI", commitHash)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	lines := strings.SplitN(logOutput, "\n", 6)
	if len(lines) < 6 {
		return nil, fmt.Errorf("unexpected log format")
	}
	commit := &models.GitCommit{
		Hash:    strings.TrimSpace(lines[0]),
		Short:   strings.TrimSpace(lines[1]),
		Author:  strings.TrimSpace(lines[2]),
		Email:   strings.TrimSpace(lines[3]),
		Message: strings.TrimSpace(lines[4]),
		Date:    strings.TrimSpace(lines[5]),
	}

	diffOutput, err := s.gitCommand(dirPath, "show", "--format=", commitHash)
	if err != nil {
		return nil, fmt.Errorf("failed to get diff: %w", err)
	}

	filesOutput, _ := s.gitCommand(dirPath, "diff-tree", "--no-commit-id", "--name-only", "-r", commitHash)
	var files []string
	if filesOutput != "" {
		files = strings.Split(filesOutput, "\n")
	}

	return &models.DiffResult{
		Commit: commit,
		Diff:   diffOutput,
		Files:  files,
	}, nil
}

func (s *GitService) GetLogForFile(dirPath, filePath string, limit int) (*models.LogResult, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	logFormat := "%H%n%h%n%an%n%ae%n%s%n%aI%n---COMMIT_SEP---"
	output, err := s.gitCommand(dirPath, "log", fmt.Sprintf("--max-count=%d", limit), fmt.Sprintf("--format=%s", logFormat), "--", filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get log: %w", err)
	}

	commits := []models.GitCommit{}
	entries := strings.Split(output, "---COMMIT_SEP---")
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		lines := strings.SplitN(entry, "\n", 6)
		if len(lines) < 6 {
			continue
		}
		commits = append(commits, models.GitCommit{
			Hash:    strings.TrimSpace(lines[0]),
			Short:   strings.TrimSpace(lines[1]),
			Author:  strings.TrimSpace(lines[2]),
			Email:   strings.TrimSpace(lines[3]),
			Message: strings.TrimSpace(lines[4]),
			Date:    strings.TrimSpace(lines[5]),
		})
	}

	return &models.LogResult{
		Commits: commits,
		Total:   len(commits),
		HasMore: false,
	}, nil
}

func (s *GitService) openRepo(dirPath string) (*git.Repository, error) {
	dotGit := osfs.New(filepath.Join(dirPath, ".git"))
	// The cache is NOT optional. filesystem.ObjectStorage dereferences it the
	// moment an object has to be read out of a packfile, so passing nil here
	// panicked on every repository produced by `git clone` — which is every
	// real stack. Repositories built commit-by-commit store loose objects and
	// never reach the packfile reader, which is why it went unnoticed.
	stor := filesystem.NewStorage(dotGit, cache.NewObjectLRUDefault())
	worktree := osfs.New(dirPath)
	repo, err := git.Open(stor, worktree)
	if err != nil {
		if err == git.ErrRepositoryNotExists {
			return nil, models.NewAppError(404, models.ErrGitNotRepo, "Not a git repository")
		}
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}
	return repo, nil
}

func (s *GitService) getDivergence(repo *git.Repository, head *plumbing.Reference) (ahead, behind int, trackingBranch, remoteURL string, err error) {
	remoteName := "origin"
	branchName := head.Name().Short()
	trackingBranch = fmt.Sprintf("%s/%s", remoteName, branchName)

	remoteRef, err := repo.Storer.Reference(plumbing.ReferenceName(fmt.Sprintf("refs/remotes/%s/%s", remoteName, branchName)))
	if err != nil {
		if err == plumbing.ErrReferenceNotFound {
			return 0, 0, "", "", nil
		}
		return 0, 0, "", "", fmt.Errorf("failed to get remote ref: %w", err)
	}

	cfg, err := repo.Config()
	if err != nil {
		return 0, 0, "", "", fmt.Errorf("failed to get config: %w", err)
	}

	remoteURL = ""
	for name, remote := range cfg.Remotes {
		if name == remoteName && len(remote.URLs) > 0 {
			remoteURL = remote.URLs[0]
			break
		}
	}

	localCommit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return 0, 0, "", "", fmt.Errorf("failed to get local commit: %w", err)
	}

	remoteCommit, err := repo.CommitObject(remoteRef.Hash())
	if err != nil {
		return 0, 0, "", "", fmt.Errorf("failed to get remote commit: %w", err)
	}

	mergeBase, err := s.findMergeBase(repo, localCommit, remoteCommit)
	if err != nil {
		return 0, 0, "", "", fmt.Errorf("failed to find merge base: %w", err)
	}

	ahead = s.countCommits(repo, mergeBase.Hash, localCommit.Hash)
	behind = s.countCommits(repo, mergeBase.Hash, remoteCommit.Hash)

	return ahead, behind, trackingBranch, remoteURL, nil
}

func (s *GitService) findMergeBase(repo *git.Repository, commit1, commit2 *object.Commit) (*object.Commit, error) {
	ancestors1 := s.collectAncestors(repo, commit1)

	queue := []*object.Commit{commit2}
	visited := map[plumbing.Hash]bool{commit2.Hash: true}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if ancestors1[current.Hash] {
			return current, nil
		}

		parents := current.Parents()
		parents.ForEach(func(parent *object.Commit) error {
			if !visited[parent.Hash] {
				visited[parent.Hash] = true
				queue = append(queue, parent)
			}
			return nil
		})
	}

	return nil, fmt.Errorf("no merge base found")
}

func (s *GitService) collectAncestors(repo *git.Repository, commit *object.Commit) map[plumbing.Hash]bool {
	ancestors := map[plumbing.Hash]bool{}
	ancestors[commit.Hash] = true

	queue := []*object.Commit{commit}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		parents := current.Parents()
		parents.ForEach(func(parent *object.Commit) error {
			if !ancestors[parent.Hash] {
				ancestors[parent.Hash] = true
				queue = append(queue, parent)
			}
			return nil
		})
	}

	return ancestors
}

// countCommits returns the number of commits reachable from target but not from
// base — the same quantity as `git rev-list --count base..target`, which is what
// git's own ahead/behind reports.
//
// It walks ALL parents. The previous implementation followed first parents only
// and stopped when it hit base, which undercounts any history containing merge
// commits: a merge that brought in two commits counted as 1 instead of 3. That
// was invisible while agent-os-r1a kept this whole path unreachable, and it
// would have become a live regression the moment the path was switched back on,
// because getStatusCLI (`git rev-list --left-right --count`) counts correctly.
//
// The old loop also had a termination hazard: base is found by BFS over all
// parents, so it need not lie on target's first-parent chain. When it did not,
// the walk ran to the root commit and returned the entire history length.
func (s *GitService) countCommits(repo *git.Repository, base, target plumbing.Hash) int {
	baseCommit, err := repo.CommitObject(base)
	if err != nil {
		return 0
	}
	reachableFromBase := s.collectAncestors(repo, baseCommit)

	targetCommit, err := repo.CommitObject(target)
	if err != nil {
		return 0
	}

	count := 0
	visited := map[plumbing.Hash]bool{targetCommit.Hash: true}
	queue := []*object.Commit{targetCommit}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if reachableFromBase[current.Hash] {
			// Everything above this commit is shared history; do not descend.
			continue
		}
		count++

		current.Parents().ForEach(func(parent *object.Commit) error {
			if !visited[parent.Hash] {
				visited[parent.Hash] = true
				queue = append(queue, parent)
			}
			return nil
		})
	}

	return count
}

func (s *GitService) mapCommit(commit *object.Commit) *models.GitCommit {
	return &models.GitCommit{
		Hash:    commit.Hash.String(),
		Short:   commit.Hash.String()[:7],
		Author:  commit.Author.Name,
		Email:   commit.Author.Email,
		Message: strings.Split(commit.Message, "\n")[0],
		Date:    commit.Author.When.Format(time.RFC3339),
	}
}
