package services

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// RedeployFailure records a stack redeploy that did not reach verified-running.
type RedeployFailure struct {
	StackID string `json:"stack"`
	Reason  string `json:"reason"`
}

// PullResult is the raw outcome of a git pull (no redeploy logic).
// The Redeploy path is handled separately via PullVerified on the DockerService.

// GitService is two pointer fields, immutable after construction, and built
// once at startup (main.go); it is reached concurrently from request handlers
// on gin's per-request goroutines. It deliberately carries no cache of
// resolved credentials or of what has already been logged: a shared map would
// need synchronization to avoid a data race, would suppress a credential
// error for the life of the process once logged once (an operator fixing a
// rotated key in-process via PUT /settings would never see the error clear),
// and would treat a transient DB error as permanent. Per-operation resolution
// (gitCmdWithCreds/gitCommandWithCreds) avoids all three by resolving fresh on
// every call to a public entry point instead of caching across calls.
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
		Branch:     branchName,
		Commit:     gitCommit,
		Dirty:      dirty,
		DirtyCount: dirtyCount,
		Ahead:      ahead,
		Behind:     behind,
		// Redacted here rather than at the handler: RemoteURL carries a json
		// tag, so any future marshal of GitStatusResult would reopen the leak
		// (agent-os-57xj). Redacting at the source makes "the struct never
		// holds a credential" an invariant instead of a per-caller duty.
		RemoteURL:      RedactURLUserinfo(remoteURL),
		TrackingBranch: trackingBranch,
	}, nil
}

func (s *GitService) getStatusCLI(dirPath string) (*models.GitStatusResult, error) {
	// Resolved once for the whole operation and threaded through every call
	// below via gitCommandWithCreds, instead of each of the nine calls
	// re-resolving (and re-logging) independently through gitCommand
	// (agent-os-9ha).
	user, token := s.httpsCredentials(dirPath)

	branch, err := s.gitCommandWithCreds(dirPath, user, token, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		// This guard used to answer EVERY failure of that command with a flat
		// 404 "Not a git repository", asserting a diagnosis it had not tested
		// (agent-os-xmtf). The condition that actually arrives here is the one
		// it was wrong about: MEASURED, go-git fails a repository with no
		// commits as "failed to get HEAD: reference not found", which is not a
		// *models.AppError, so GetStatus falls through to this function — while
		// a genuinely absent repository is already caught upstream by openRepo
		// and returned as-is by GetStatus, never reaching here.
		//
		// The two are fixed differently. An empty repository needs a commit or
		// a remote; an absent one needs the stack pointed somewhere else. The
		// old message sent the first operator looking for the second problem.
		//
		// Both questions are put to git as questions, never read out of its
		// prose, because git translates its messages.
		if repoErr := s.gitFailure(dirPath, err); repoErr != nil {
			return nil, repoErr
		}
		if s.hasUnbornHead(dirPath) {
			// ErrNotFound rather than a dedicated GIT_NO_COMMITS: adding a code
			// means editing internal/models/errors.go, and nothing branches on
			// git codes on the client side (the frontend renders the message),
			// so the honest message is the whole of the user-visible fix. The
			// 404 is unchanged from before, so only the diagnosis moves.
			return nil, models.NewAppError(404, models.ErrNotFound, "Repository has no commits yet")
		}
		// A repository, with a HEAD that resolves, whose branch name still could
		// not be read: unusual enough that naming it would be another guess.
		return nil, fmt.Errorf("failed to resolve HEAD: %w", err)
	}

	commitHash, err := s.gitCommandWithCreds(dirPath, user, token, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	subject, _ := s.gitCommandWithCreds(dirPath, user, token, "log", "-1", "--format=%s")
	author, _ := s.gitCommandWithCreds(dirPath, user, token, "log", "-1", "--format=%an")
	email, _ := s.gitCommandWithCreds(dirPath, user, token, "log", "-1", "--format=%ae")
	dateStr, _ := s.gitCommandWithCreds(dirPath, user, token, "log", "-1", "--format=%aI")

	shortHash := commitHash
	if len(shortHash) > 7 {
		shortHash = shortHash[:7]
	}

	dirty := false
	dirtyCount := 0
	if output, err := s.gitCommandWithCreds(dirPath, user, token, "status", "--porcelain"); err == nil {
		trimmed := strings.TrimSpace(output)
		dirty = trimmed != ""
		if dirty {
			dirtyCount = len(strings.Split(trimmed, "\n"))
		}
	}

	ahead := 0
	behind := 0
	if output, err := s.gitCommandWithCreds(dirPath, user, token, "rev-list", "--left-right", "--count", "@{upstream}...HEAD"); err == nil {
		parts := strings.Fields(output)
		if len(parts) == 2 {
			behind, _ = strconv.Atoi(parts[0])
			ahead, _ = strconv.Atoi(parts[1])
		}
	}

	remoteURL, _ := s.gitCommandWithCreds(dirPath, user, token, "remote", "get-url", "origin")
	// See the go-git path above. redactToken has already run on this value, but
	// it only removes the token Capstan itself resolved, so a credential the
	// operator embedded independently survives it (agent-os-57xj).
	remoteURL = RedactURLUserinfo(remoteURL)

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
	user, token := s.httpsCredentials(dirPath)
	return s.gitCommandWithCreds(dirPath, user, token, args...)
}

// gitCommandWithCreds is gitCommand with credential resolution factored out,
// mirroring gitCmdWithCreds: callers that issue several git invocations for
// one logical operation resolve once and pass the result to every call here.
func (s *GitService) gitCommandWithCreds(dirPath, user, token string, args ...string) (string, error) {
	cmd, _ := s.gitCmdWithCreds(dirPath, user, token, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w (%s)", args[0], err,
			redactToken(strings.TrimSpace(string(output)), token))
	}
	return redactToken(strings.TrimSpace(string(output)), token), nil
}

func (s *GitService) Pull(dirPath string) (*models.PullResult, error) {
	return s.pullCLI(dirPath)
}

func (s *GitService) pullCLI(dirPath string) (*models.PullResult, error) {
	slog.Debug("Pulling git changes (CLI)", "path", dirPath)

	// Resolved once for the whole pull and threaded through every call below
	// (agent-os-9ha) — see getStatusCLI for why.
	user, token := s.httpsCredentials(dirPath)

	dirtyOutput, err := s.gitCommandWithCreds(dirPath, user, token, "status", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("failed to check status: %w", err)
	}
	if strings.TrimSpace(dirtyOutput) != "" {
		return nil, models.NewAppError(400, models.ErrGitDirty, "Working directory has uncommitted changes")
	}

	previousCommit, err := s.gitCommandWithCreds(dirPath, user, token, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	_, err = s.gitCommandWithCreds(dirPath, user, token, "pull", "--ff-only")
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

	currentCommit, _ := s.gitCommandWithCreds(dirPath, user, token, "rev-parse", "HEAD")

	changedFiles := []string{}
	if previousCommit != currentCommit {
		diffOutput, err := s.gitCommandWithCreds(dirPath, user, token, "diff", "--name-only", previousCommit, currentCommit)
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

// gitFailure classifies a failed git invocation in dirPath.
//
// It returns the error the caller should surface, or nil when the failure is not
// attributable to the directory's repository state and the caller should wrap
// generically as before (agent-os-pawv).
//
// Before this, a stack directory that was not a git repository produced a typed
// 404 from GET /git and a generic 500 from /git/log, /git/diff and the file-log
// path — one condition, two answers. The 500 told an operator "the server is
// broken, file a bug"; the 404 told them "this is not a repository, point it
// elsewhere". Only the second was true.
//
// It asks git the question directly rather than reading the failed command's
// message, for two independent reasons. Git translates its output — this host
// carries 19 catalogs, and LANGUAGE=de turns "not a git repository" into
// "Kein Git-Repository" — so text matching silently stops firing on a
// non-English host (agent-os-vq3p). That translation has one precondition worth
// stating, because it is exactly what agent-os-vq3p's LC_ALL=C pin removes:
// glibc consults LANGUAGE only when the messages locale resolves to a GENERATED
// locale other than C/POSIX. It is not a precondition about catalogs, and on
// this host it is satisfied by default. MEASURED, git 2.47.3, LANG=en_US.UTF-8,
// `locale -a` = {C, C.utf8, en_US.utf8, nl_NL.utf8, POSIX}:
//
//	(unset LC_ALL; LANGUAGE=de)  -> "Schwerwiegend: Kein Git-Repository ..."  German
//	LC_ALL=en_US.utf8 LANGUAGE=de -> "Schwerwiegend: Kein Git-Repository ..."  German
//	LC_ALL=C          LANGUAGE=de -> "fatal: not a git repository ..."         English
//	LC_ALL=de_DE.utf8 LANGUAGE=de -> "fatal: not a git repository ..."         English
//
// The last arm reads English because de_DE.utf8 is not generated here, so
// setlocale falls back to C — not because the de catalog is missing; it is
// installed at /usr/share/locale/de/LC_MESSAGES/git.mo.
//
// And a failed first command does not imply a missing repository: getDiffCLI's
// first command is a log of a caller-supplied hash, which fails with
// "bad object" for a perfectly good repo.
//
// The probe must be the git CLI, not go-git. gitCmd sets cmd.Dir without
// GIT_CEILING_DIRECTORIES, so git walks up to a parent .git: a directory nested
// inside a repo, and a bare repo, both serve logs correctly today. go-git's
// PlainOpen reports neither as a repository, so a go-git probe would turn two
// working shapes into 404s. Using the same tool the endpoints use means the
// probe cannot disagree with them.
//
// Deliberately narrower than "the repository is usable": a repo with NO COMMITS
// is a repository, and answering GIT_NOT_REPO for it would turn one lying
// message into four. That case now has an honest answer of its own — see
// hasUnbornHead and its caller in getStatusCLI (agent-os-xmtf).
//
// Runs only after a command has already failed, so the happy path pays nothing.
// Credentials are not resolved for the probe — rev-parse contacts no remote, and
// resolving them would add a database read to an error path.
func (s *GitService) gitFailure(dirPath string, err error) error {
	if err == nil {
		return nil
	}
	if _, probeErr := s.gitCommandWithCreds(dirPath, "", "", "rev-parse", "--git-dir"); probeErr != nil {
		return models.NewAppError(404, models.ErrGitNotRepo, "Not a git repository")
	}
	return nil
}

// hasUnbornHead reports whether dirPath's repository has a HEAD pointing at a
// branch that does not exist yet — the state `git init` leaves behind until the
// first commit (agent-os-xmtf).
//
// Callers must have established that dirPath IS a repository first (gitFailure
// returning nil); this answers only "and does it have any commits".
//
// Two probes, exit codes only, no message text. `rev-parse --verify HEAD`
// separates "HEAD resolves to a commit" from "it does not"; `symbolic-ref
// --quiet HEAD` then separates an unborn branch from a HEAD that is broken for
// some other reason, because a symbolic ref to a branch that has no commits yet
// is still a perfectly readable symbolic ref. OBSERVED, git 2.47.3 on this host,
// exit statuses:
//
//	                    --git-dir  --verify HEAD  symbolic-ref --quiet HEAD
//	genuine non-repo    128        128            128
//	repo, no commits    0 (.git)   128            0 (refs/heads/master)
//	repo with commits   0          0              0
//
// Reading the messages instead would be wrong under any locale whose messages
// catalog is installed. The same probe run as
// `LC_ALL=en_US.utf8 LANGUAGE=de git rev-parse --git-dir` prints
// "Schwerwiegend: Kein Git-Repository ..."; 19 catalogs ship on this host.
// agent-os-vq3p pins the child to LC_ALL=C, which makes text matching possible
// but not safe — it would then be correct only for as long as that pin survives.
// Exit codes need no such pin.
//
// Credentials are not resolved: neither probe contacts a remote, and this runs
// only after a command has already failed.
func (s *GitService) hasUnbornHead(dirPath string) bool {
	if _, err := s.gitCommandWithCreds(dirPath, "", "", "rev-parse", "--verify", "HEAD"); err == nil {
		return false
	}
	_, err := s.gitCommandWithCreds(dirPath, "", "", "symbolic-ref", "--quiet", "HEAD")
	return err == nil
}

func (s *GitService) getLogCLI(dirPath string, limit, offset int) (*models.LogResult, error) {
	// Resolved once for the whole log request (agent-os-9ha) — see
	// getStatusCLI for why.
	user, token := s.httpsCredentials(dirPath)

	totalStr, err := s.gitCommandWithCreds(dirPath, user, token, "rev-list", "--count", "HEAD")
	if err != nil {
		if repoErr := s.gitFailure(dirPath, err); repoErr != nil {
			return nil, repoErr
		}
		return nil, fmt.Errorf("failed to count commits: %w", err)
	}
	total, _ := strconv.Atoi(totalStr)

	skip := offset
	fetchCount := limit
	logFormat := "%H%n%h%n%an%n%ae%n%s%n%aI%n---COMMIT_SEP---"
	output, err := s.gitCommandWithCreds(dirPath, user, token, "log", fmt.Sprintf("--skip=%d", skip), fmt.Sprintf("--max-count=%d", fetchCount), fmt.Sprintf("--format=%s", logFormat))
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
	// Resolved once for the whole diff request (agent-os-9ha) — see
	// getStatusCLI for why.
	user, token := s.httpsCredentials(dirPath)

	logOutput, err := s.gitCommandWithCreds(dirPath, user, token, "log", "-1", "--format=%H%n%h%n%an%n%ae%n%s%n%aI", commitHash)
	if err != nil {
		if repoErr := s.gitFailure(dirPath, err); repoErr != nil {
			return nil, repoErr
		}
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

	diffOutput, err := s.gitCommandWithCreds(dirPath, user, token, "show", "--format=", commitHash)
	if err != nil {
		return nil, fmt.Errorf("failed to get diff: %w", err)
	}

	filesOutput, _ := s.gitCommandWithCreds(dirPath, user, token, "diff-tree", "--no-commit-id", "--name-only", "-r", commitHash)
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
		if repoErr := s.gitFailure(dirPath, err); repoErr != nil {
			return nil, repoErr
		}
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
		// The callback never returns an error, so a non-nil return here is
		// an iteration/storage fault. It silently truncates this commit's
		// parent set for the BFS rather than aborting the walk, which can
		// turn a real merge base into a false "no merge base found" below —
		// worth knowing about even though there's nothing to retry inline.
		if err := parents.ForEach(func(parent *object.Commit) error {
			if !visited[parent.Hash] {
				visited[parent.Hash] = true
				queue = append(queue, parent)
			}
			return nil
		}); err != nil {
			slog.Warn("Merge-base parent traversal truncated by a storage error", "commit", current.Hash, "error", err)
		}
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
		// See findMergeBase above: a non-nil return here is a storage
		// fault that silently truncates the ancestor set this function
		// builds, which callers use to compute ahead/behind — worth
		// logging even though there's nothing to retry inline.
		if err := parents.ForEach(func(parent *object.Commit) error {
			if !ancestors[parent.Hash] {
				ancestors[parent.Hash] = true
				queue = append(queue, parent)
			}
			return nil
		}); err != nil {
			slog.Warn("Ancestor traversal truncated by a storage error", "commit", current.Hash, "error", err)
		}
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

		// See findMergeBase above: a non-nil return here is a storage
		// fault that silently truncates the walk, which would undercount
		// commits ahead/behind — worth logging even though there's
		// nothing to retry inline.
		if err := current.Parents().ForEach(func(parent *object.Commit) error {
			if !visited[parent.Hash] {
				visited[parent.Hash] = true
				queue = append(queue, parent)
			}
			return nil
		}); err != nil {
			slog.Warn("Commit count traversal truncated by a storage error", "commit", current.Hash, "error", err)
		}
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
