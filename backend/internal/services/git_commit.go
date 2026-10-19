package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/pathutil"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// The user-initiated commit and push (agent-os-k81p.2). Nothing here runs on
// its own: a file save never touches git, and both writes need the folder's
// push switch on.
//
// One repository can hold several stack folders (the scanner makes one
// directories row per folder and walks up to an ancestor .git), so:
//   - the change list holds only files inside the requesting folder;
//   - the lock covers every stack inside the repository top level;
//   - the switch, credentials and auth type come from the requesting folder;
//   - a push sends the whole branch, which can hold commits made elsewhere.
//
// Writes run git from the top level, so gitCmdWithCreds sets
// safe.directory to it. The first child (show-toplevel) has to run from the
// folder itself, so an ancestor repository owned by another user still fails
// there, as GetStatus does.

// maxCommitMessageBytes bounds the message, which travels as one argv entry.
const maxCommitMessageBytes = 64 << 10

// CommitRequest is what POST /git/commit asks for.
type CommitRequest struct {
	Message string
	// Files are repository-root-relative paths, as GetChanges lists them.
	Files      []string
	ConfirmEnv bool
	Push       bool
}

// GitWriteResult says what a commit or push request actually did, so the
// handler writes one action-log row per commit and one per push that ran.
type GitWriteResult struct {
	// Commit is the hash of the commit this request made; "" when none.
	Commit string
	Files  int
	// PushRan is true once the push child was started, whatever it answered.
	PushRan bool
	// Remote is the remote's NAME. Its URL can carry a token, so it is never
	// recorded.
	Remote string
	// Branch is the remote ref pushed to.
	Branch string
}

// pushTarget is where a push goes: branch.<b>.remote and branch.<b>.merge.
type pushTarget struct {
	remote   string
	mergeRef string
}

// GetChanges lists the tracked files with uncommitted changes inside dirPath,
// plus what decides whether a commit or push can run. A read error of the
// switch or the author settings is an error, never false.
func (s *GitService) GetChanges(dirPath string) (*models.GitChangesResult, error) {
	pushEnabled, err := s.pushSwitch(dirPath)
	if err != nil {
		return nil, err
	}
	name, email, err := s.authorIdentity()
	if err != nil {
		return nil, err
	}

	status, err := s.GetStatus(dirPath)
	if err != nil {
		return nil, err
	}
	if status.IsBare {
		return nil, models.NewAppError(http.StatusConflict, models.ErrGitBareRepo, "A bare repository has no work tree to commit from")
	}

	cred := s.gitCredentials(dirPath)
	top, err := s.repoTopLevel(dirPath, cred)
	if err != nil {
		return nil, err
	}
	changes, err := s.listChanges(dirPath, top, cred)
	if err != nil {
		return nil, err
	}
	branch, detached, err := s.headBranch(top, cred)
	if err != nil {
		return nil, err
	}
	// Exit 128 is both "no upstream" and a fault, so this is a hint for the
	// form only; commit and push ask again under the lock and refuse.
	_, upErr := s.gitCommandWithCreds(top, cred, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}") //geterrors:ignore a failing @{upstream} IS the "no upstream" answer, and git exits 128 for it and for a fault alike; commit and push re-check under the lock

	return &models.GitChangesResult{
		Branch:             branch,
		Detached:           detached,
		TrackingBranch:     status.TrackingBranch,
		HasUpstream:        upErr == nil,
		Ahead:              status.Ahead,
		Behind:             status.Behind,
		PushEnabled:        pushEnabled,
		IdentityConfigured: name != "" && email != "",
		Changes:            changes,
	}, nil
}

// GetChangeDiff is the uncommitted diff, against HEAD, of one file GetChanges
// lists for dirPath. An env file answers Hidden with no diff, and no diff child
// runs for it. Whether it is one comes from the change list (which also
// covers a rename's original path and env symlinks), never from the
// requested path.
func (s *GitService) GetChangeDiff(dirPath, path string) (*models.GitChangeDiff, error) {
	status, err := s.GetStatus(dirPath)
	if err != nil {
		return nil, err
	}
	if status.IsBare {
		return nil, models.NewAppError(http.StatusConflict, models.ErrGitBareRepo, "A bare repository has no work tree to diff")
	}
	cred := s.gitCredentials(dirPath)
	top, err := s.repoTopLevel(dirPath, cred)
	if err != nil {
		return nil, err
	}
	changes, err := s.listChanges(dirPath, top, cred)
	if err != nil {
		return nil, err
	}
	var change *models.GitChange
	for i := range changes {
		if changes[i].Path == path {
			change = &changes[i]
			break
		}
	}
	if change == nil {
		return nil, models.NewAppError(http.StatusBadRequest, models.ErrValidation,
			fmt.Sprintf("%q is not a changed tracked file in this stack's folder", path))
	}
	if change.IsEnv {
		return &models.GitChangeDiff{Path: path, Hidden: true}, nil
	}

	pathspecs := []string{change.Path}
	if change.OrigPath != "" {
		pathspecs = append(pathspecs, change.OrigPath)
	}
	diff, _, err := s.gitRun(top, cred, []string{"GIT_LITERAL_PATHSPECS=1"}, append(changeDiffArgs, pathspecs...)...)
	if err != nil {
		return nil, fmt.Errorf("failed to read the diff: %w", err)
	}
	return &models.GitChangeDiff{Path: path, Diff: diff}, nil
}

// changeDiffArgs is `git diff HEAD` with everything repository config could
// change about it pinned, ending in "--". --no-ext-diff and --no-textconv stop
// diff.external, GIT_EXTERNAL_DIFF, diff driver commands and textconv from
// running code; --no-color, the explicit prefixes and --no-relative undo
// color.ui=always, diff.noprefix, diff.mnemonicPrefix and diff.relative, and
// core.quotePath=false keeps non-ASCII names unquoted, each of which would
// break the frontend's parseDiff. --submodule=short (and diff.submodule=short,
// which the flag outranks) keeps a submodule to its commit line: "diff" mode
// runs a git diff inside the submodule, which reads the submodule's own
// diff.external and textconv and shows its files' contents. Clean and process
// filters still run when git reads the work tree: that is a residual risk, as
// it is for status.
var changeDiffArgs = []string{
	"-c", "core.fsmonitor=false", "-c", "core.quotePath=false", "--no-optional-locks",
	"-c", "diff.submodule=short",
	"diff", "HEAD", "--no-ext-diff", "--no-textconv", "--no-color", "--submodule=short",
	"--src-prefix=a/", "--dst-prefix=b/", "--no-relative", "--",
}

// CommitVerified commits exactly the requested files and, when req.Push, pushes
// the branch under the same lock.
//
// Order: request shape, then the database-only read of the switch before any
// git child; then the lock; then every other read and every precondition, the
// push's included (its URL and, over SSH, its key), before the commit. So a refusal leaves nothing half done.
// A push that fails after the commit keeps the commit and answers partial.
func (s *GitService) CommitVerified(dirPath string, req CommitRequest) (truth.ActionResult, *GitWriteResult) {
	message := strings.TrimSpace(req.Message)
	switch {
	case message == "":
		return refused(models.NewAppError(http.StatusBadRequest, models.ErrValidation, "A commit message is required"))
	case strings.ContainsRune(message, 0):
		return refused(models.NewAppError(http.StatusBadRequest, models.ErrValidation, "The commit message must not contain a NUL character"))
	case len(message) > maxCommitMessageBytes:
		return refused(models.NewAppError(http.StatusBadRequest, models.ErrValidation, "The commit message must be at most 64 KiB"))
	case len(req.Files) == 0:
		return refused(models.NewAppError(http.StatusBadRequest, models.ErrValidation, "Choose at least one file to commit"))
	}

	if s.opLock == nil {
		return truth.Failed("operation lock is not configured; refusing a git write", nil), nil
	}
	if ar, ok := s.writePrecheck(dirPath); !ok {
		return ar, nil
	}

	cred := s.gitCredentials(dirPath)
	top, err := s.repoTopLevel(dirPath, cred)
	if err != nil {
		return truth.Failed("could not find the repository", err), nil
	}
	release, err := s.lockRepo(top, OpKindGitCommit)
	if err != nil {
		return repoLockFailure("could not lock the repository's stacks", "commit", err), nil
	}
	defer release()

	name, email, err := s.authorIdentity()
	if err != nil {
		return truth.Failed("could not read the commit author settings", err), nil
	}
	if name == "" || email == "" {
		return refused(models.NewAppError(http.StatusBadRequest, models.ErrGitIdentityMissing,
			"Set the commit author name and email in Settings > Git first"))
	}
	branch, err := s.requireBranch(top, cred)
	if err != nil {
		return truth.Failed("could not read the current branch", err), nil
	}
	if err := s.refuseOperationInProgress(top, cred); err != nil {
		return truth.Failed("could not check for a merge in progress", err), nil
	}
	changes, err := s.listChanges(dirPath, top, cred)
	if err != nil {
		return truth.Failed("could not list the changed files", err), nil
	}
	paths, err := selectCommitPaths(dirPath, top, changes, req)
	if err != nil {
		return truth.Failed("could not check the requested files", err), nil
	}
	var target pushTarget
	if req.Push {
		if target, err = s.pushPreconditions(top, branch, cred); err != nil {
			return truth.Failed("could not check the push target", err), nil
		}
	}

	hash, err := s.commitPaths(top, cred, name, email, message, paths)
	if err != nil {
		return truth.Failed("git commit failed", err), nil
	}
	w := &GitWriteResult{Commit: hash, Files: len(paths)}
	if !req.Push {
		return truth.Success("committed",
			truth.KV("commit", hash),
			truth.KV("files", len(paths)),
		), w
	}

	w.PushRan, w.Remote, w.Branch = true, target.remote, target.mergeRef
	rng, _, err := s.push(top, cred, target)
	if err != nil {
		code, reason := "INTERNAL_ERROR", "git push failed"
		var appErr *models.AppError
		if errors.As(err, &appErr) {
			code, reason = appErr.Code, appErr.Message
		}
		slog.Warn("Commit kept after its push failed", "path", dirPath, "commit", hash, "error", err)
		return truth.Partial("committed, but the push failed: "+reason,
			truth.KV("commit", hash),
			truth.KV("files", len(paths)),
			truth.KV("remote", target.remote),
			truth.KV("branch", target.mergeRef),
			truth.KV("pushCode", code),
			truth.KV("pushReason", reason),
		), w
	}
	return truth.Success("committed and pushed "+branch+" to "+target.remote+pushScopeNote,
		truth.KV("commit", hash),
		truth.KV("files", len(paths)),
		truth.KV("remote", target.remote),
		truth.KV("branch", target.mergeRef),
		truth.KV("range", rng),
	), w
}

// PushVerified pushes the current branch to its upstream: never a force, never
// a new upstream, never tags or submodules.
func (s *GitService) PushVerified(dirPath string) (truth.ActionResult, *GitWriteResult) {
	if s.opLock == nil {
		return truth.Failed("operation lock is not configured; refusing a git write", nil), nil
	}
	if ar, ok := s.writePrecheck(dirPath); !ok {
		return ar, nil
	}

	cred := s.gitCredentials(dirPath)
	top, err := s.repoTopLevel(dirPath, cred)
	if err != nil {
		return truth.Failed("could not find the repository", err), nil
	}
	release, err := s.lockRepo(top, OpKindGitPush)
	if err != nil {
		return repoLockFailure("could not lock the repository's stacks", "push", err), nil
	}
	defer release()

	branch, err := s.requireBranch(top, cred)
	if err != nil {
		return truth.Failed("could not read the current branch", err), nil
	}
	target, err := s.pushPreconditions(top, branch, cred)
	if err != nil {
		return truth.Failed("could not check the push target", err), nil
	}

	w := &GitWriteResult{PushRan: true, Remote: target.remote, Branch: target.mergeRef}
	rng, upToDate, err := s.push(top, cred, target)
	switch {
	case err != nil:
		return truth.Failed("git push failed", err), w
	case upToDate:
		return truth.NoChange("already up to date",
			truth.KV("remote", target.remote),
			truth.KV("branch", target.mergeRef),
		), w
	}
	return truth.Success("pushed "+branch+" to "+target.remote+pushScopeNote,
		truth.KV("remote", target.remote),
		truth.KV("branch", target.mergeRef),
		truth.KV("range", rng),
	), w
}

// pushScopeNote is appended to a push's success reason: the push sends the
// branch, not only what Capstan committed (agent-os-k81p, D134).
const pushScopeNote = " (a push sends every commit on the branch the remote lacks, including ones made outside Capstan)"

// refused is a classified refusal that wrote nothing.
func refused(err *models.AppError) (truth.ActionResult, *GitWriteResult) {
	return truth.Failed(err.Message, err), nil
}

// writePrecheck runs the database-only gate of a git write, before any git
// child: the push switch. An ssh push is checked later, against the push
// URL, by pushURLRefusal (agent-os-z0gl).
func (s *GitService) writePrecheck(dirPath string) (truth.ActionResult, bool) {
	enabled, err := s.pushSwitch(dirPath)
	if err != nil {
		return truth.Failed("could not read the push switch", err), false
	}
	if !enabled {
		return truth.Failed("push is not enabled for this directory", models.NewAppError(http.StatusForbidden,
			models.ErrGitPushDisabled, "Commit and push are not enabled for this directory")), false
	}
	return truth.ActionResult{}, true
}

// pushSwitch reads dirPath's push switch. No row means off; any other read
// error is returned, never read as off or on.
func (s *GitService) pushSwitch(dirPath string) (bool, error) {
	dir, err := s.db.GetDirectory(dirPath)
	switch {
	case errors.Is(err, errdefs.ErrNotFound):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("could not read the push switch for this directory: %w", err)
	}
	return dir.GitPushEnabled, nil
}

// authorIdentity reads the commit author settings, trimmed. An unset key is
// ""; any other read error is returned. Each read names its key constant
// directly, so the settings-key census (database/settings_classification_test.go)
// can resolve it.
func (s *GitService) authorIdentity() (name, email string, err error) {
	v, err := s.db.GetSetting(models.SettingGitAuthorName)
	if name, err = settingOrUnset(models.SettingGitAuthorName, v, err); err != nil {
		return "", "", err
	}
	v, err = s.db.GetSetting(models.SettingGitAuthorEmail)
	if email, err = settingOrUnset(models.SettingGitAuthorEmail, v, err); err != nil {
		return "", "", err
	}
	return name, email, nil
}

// settingOrUnset maps a GetSetting answer for key: unset is "", any other
// error is wrapped with the key's name.
func settingOrUnset(key, v string, err error) (string, error) {
	switch {
	case errors.Is(err, errdefs.ErrNotFound):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("could not read the %s setting: %w", key, err)
	}
	return strings.TrimSpace(v), nil
}

// repoTopLevel is the work tree root of the repository holding dirPath.
func (s *GitService) repoTopLevel(dirPath string, cred gitCreds) (string, error) {
	out, err := s.gitCommandWithCreds(dirPath, cred, "rev-parse", "--show-toplevel")
	if err == nil {
		return filepath.Clean(out), nil
	}
	if repoErr := s.gitFailure(dirPath, err); repoErr != nil {
		return "", repoErr
	}
	isBare, bareErr := s.gitCommandWithCreds(dirPath, cred, "rev-parse", "--is-bare-repository")
	if bareErr != nil {
		return "", fmt.Errorf("failed to find the repository top level (%w) or tell whether it is bare: %w", err, bareErr)
	}
	if isBare == "true" {
		return "", models.NewAppError(http.StatusConflict, models.ErrGitBareRepo, "A bare repository has no work tree to commit from")
	}
	return "", fmt.Errorf("failed to find the repository top level: %w", err)
}

// RepoStackPathError is a registered stack whose folder could not be resolved,
// so whether it lies inside the repository cannot be told and the git
// operation is refused (agent-os-l4h6). It carries the stack so the refusal
// can name it: the cause alone would reach only the server log.
type RepoStackPathError struct {
	Stack models.Stack
	Err   error
}

func (e *RepoStackPathError) Error() string {
	return fmt.Sprintf("could not tell whether stack %s (%s) is inside the repository: %v", e.Stack.ProjectName, e.Stack.Directory, e.Err)
}

func (e *RepoStackPathError) Unwrap() error { return e.Err }

// repoLockFailure answers a lockRepoStacks failure. A RepoStackPathError names
// the blocking stack and its folder, with the same detail keys as the delete
// refusal (handlers renderStackPathError); anything else keeps fallback.
// action is what the lock was for: "pull", "commit" or "push".
func repoLockFailure(fallback, action string, err error) truth.ActionResult {
	var pathErr *RepoStackPathError
	if !errors.As(err, &pathErr) {
		return truth.Failed(fallback, err)
	}
	return truth.Failed(
		fmt.Sprintf("could not resolve the folder of stack %s (%s), so the repository's stacks cannot be locked for this %s; fix that folder or rescan, then retry",
			pathErr.Stack.ProjectName, pathErr.Stack.Directory, action),
		err,
		truth.KV("blockingStackId", pathErr.Stack.ID),
		truth.KV("blockingDirectory", pathErr.Stack.Directory),
	)
}

// lockRepo takes the lock of every stack whose folder is inside top: a
// commit or push in one folder changes what every stack of the repository
// sees. An unanswerable containment check refuses rather than skipping the
// stack, which would under-lock.
func (s *GitService) lockRepo(top, kind string) (func(), error) {
	_, release, err := s.lockRepoStacks(top, kind)
	return release, err
}

// lockRepoStacks is lockRepo, also returning the stacks it locked: a caller
// acting on some of them takes them from this list, never from a second read,
// which could name a stack registered after the lock was taken (agent-os-88xz).
func (s *GitService) lockRepoStacks(top, kind string) ([]models.Stack, func(), error) {
	stacks, err := s.db.ListStacks()
	if err != nil {
		return nil, nil, fmt.Errorf("could not list the stacks to lock: %w", err)
	}
	var inRepo []models.Stack
	for _, st := range stacks {
		ok, err := pathutil.IsContained(top, st.Directory)
		if err != nil {
			return nil, nil, &RepoStackPathError{Stack: st, Err: err}
		}
		if ok {
			inRepo = append(inRepo, st)
		}
	}
	release, err := s.lockStacks(inRepo, kind)
	if err != nil {
		return nil, nil, models.NewAppError(http.StatusConflict, models.ErrOperationInProgress, err.Error())
	}
	return inRepo, release, nil
}

// headBranch is the branch HEAD names, or detached=true when it names none.
// The full ref is read and refs/heads/ stripped: --short answers "heads/main"
// when a tag is also named "main", which names no branch config.
func (s *GitService) headBranch(top string, cred gitCreds) (branch string, detached bool, err error) {
	out, err := s.gitCommandWithCreds(top, cred, "symbolic-ref", "-q", "HEAD")
	if err != nil {
		// Exit 1 is git's documented "HEAD is not a symbolic ref".
		if gitExitCode(err) == 1 {
			return "", true, nil
		}
		return "", false, fmt.Errorf("failed to read the current branch: %w", err)
	}
	branch, ok := strings.CutPrefix(out, "refs/heads/")
	if !ok || branch == "" {
		return "", false, fmt.Errorf("HEAD names %q, which is not a branch", out)
	}
	return branch, false, nil
}

// requireBranch is headBranch with a detached HEAD refused: a commit there is
// reachable from HEAD alone, and a push has no branch to send.
func (s *GitService) requireBranch(top string, cred gitCreds) (string, error) {
	branch, detached, err := s.headBranch(top, cred)
	if err != nil {
		return "", err
	}
	if detached {
		return "", models.NewAppError(http.StatusConflict, models.ErrGitDetachedHead,
			"HEAD is detached; check out a branch before committing or pushing")
	}
	return branch, nil
}

// refuseOperationInProgress refuses during a merge, cherry-pick or revert, by
// exit code (0 present, 1 absent). A rebase leaves HEAD detached, which
// requireBranch has already refused.
func (s *GitService) refuseOperationInProgress(top string, cred gitCreds) error {
	for _, ref := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"} {
		_, err := s.gitCommandWithCreds(top, cred, "rev-parse", "-q", "--verify", ref)
		switch {
		case err == nil:
			return models.NewAppError(http.StatusConflict, models.ErrGitOperationInProgress,
				"A merge, cherry-pick or revert is in progress in this repository; finish or abort it first")
		case gitExitCode(err) != 1:
			return fmt.Errorf("failed to check for %s: %w", ref, err)
		}
	}
	return nil
}

// listChanges reads the tracked changes under dirPath. Porcelain v2 with -z:
// paths are repository-root-relative and NUL-terminated, never C-quoted, and
// every record starts with a type letter, so nothing at its start is
// whitespace. --no-optional-locks keeps status off .git/index.lock, which a
// concurrent commit needs; fsmonitor is off because repository config must
// not run code here either.
func (s *GitService) listChanges(dirPath, top string, cred gitCreds) ([]models.GitChange, error) {
	out, _, err := s.gitRun(dirPath, cred, nil,
		"-c", "core.fsmonitor=false", "--no-optional-locks", "status", "--porcelain=v2", "-z", "--untracked-files=no", "--", ".")
	if err != nil {
		return nil, fmt.Errorf("failed to read status: %w", err)
	}
	entries, err := parsePorcelainV2(out)
	if err != nil {
		return nil, err
	}

	rule, err := s.envRule(dirPath, top, cred)
	if err != nil {
		return nil, err
	}

	changes := []models.GitChange{}
	for _, e := range entries {
		if e.unmerged {
			return nil, models.NewAppError(http.StatusConflict, models.ErrGitOperationInProgress,
				"This folder has files with unresolved conflicts; resolve them first")
		}
		abs := filepath.Join(top, filepath.FromSlash(e.path))
		ok, err := inFolder(dirPath, abs)
		if err != nil {
			return nil, fmt.Errorf("failed to check that %q is inside the stack folder: %w", e.path, err)
		}
		if !ok {
			continue
		}
		isEnv := rule.matches(e.path)
		if e.origPath != "" {
			isEnv = isEnv || rule.matches(e.origPath)
		}
		changes = append(changes, models.GitChange{
			Path:     e.path,
			OrigPath: e.origPath,
			Status:   e.xy,
			IsEnv:    isEnv,
		})
	}
	return changes, nil
}

type porcelainEntry struct {
	xy       string
	path     string
	origPath string
	unmerged bool
}

// parsePorcelainV2 parses `status --porcelain=v2 -z`. A path may hold spaces,
// so each record is split a bounded number of times: type 1 has eight fields
// before the path, type 2 nine (the last is the rename score) and its original
// path follows as the next NUL record, type u ten. Untracked and ignored
// records are dropped.
func parsePorcelainV2(out string) ([]porcelainEntry, error) {
	recs := strings.Split(out, "\x00")
	var entries []porcelainEntry
	for i := 0; i < len(recs); i++ {
		r := recs[i]
		if r == "" {
			continue
		}
		var want int
		switch r[0] {
		case '1':
			want = 9
		case '2':
			want = 10
		case 'u':
			want = 11
		case '?', '!', '#':
			continue
		default:
			return nil, fmt.Errorf("unexpected git status record %q", r)
		}
		f := strings.SplitN(r, " ", want)
		if len(f) != want {
			return nil, fmt.Errorf("git status record has %d fields, want %d: %q", len(f), want, r)
		}
		e := porcelainEntry{xy: f[1], path: f[want-1], unmerged: r[0] == 'u'}
		if r[0] == '2' {
			i++
			if i >= len(recs) || recs[i] == "" {
				return nil, fmt.Errorf("git status rename record has no original path: %q", r)
			}
			e.origPath = recs[i]
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// inFolder reports whether abs lies inside dirPath. The PARENT is resolved,
// not the file: git commits a symlink as a link, so a tracked symlink pointing
// outside the folder is still a file of the folder.
func inFolder(dirPath, abs string) (bool, error) {
	if base := filepath.Base(abs); base == "." || base == ".." || base == string(filepath.Separator) {
		return false, nil
	}
	return pathutil.IsContained(dirPath, filepath.Dir(abs))
}

// envRule decides which changed files are env files. Since agent-os-k81p.3 it
// decides what the dialog SHOWS (GET /git/changes/diff hides an env file) as
// well as what needs confirmEnv, so it errs toward flagging.
type envRule struct {
	// named holds the env files named by path: each stack's EnvFile and every
	// file its compose file names as secret (composeSecretFiles), absolute.
	named map[string]bool
	// targets holds where env files really live: named, every env-named
	// tracked path of the repository and every env-named untracked path of
	// the folder, with symlinks resolved. An env-named link
	// (.env -> secrets.txt) leaves its target an innocent name.
	targets map[string]bool
	// composeUnreadable: a stack's compose file could not be read or parsed,
	// or an env_file: entry could not be resolved, so the env files it names
	// are unknown. Every change but the compose files is then flagged.
	composeUnreadable bool
	compose           map[string]bool
	// git is the history view's rule over HEAD (agent-os-odjs) and the index
	// (agent-os-7l76): a file a compose file named at HEAD or names in the
	// index stays an env file when today's compose file is deleted, renamed
	// or no longer names it, and a compose file staged for a stack not yet
	// registered names its files too. nil when it could not be built, and
	// composeUnreadable is then set.
	git *historyEnvRule
	top string
}

// matches takes rel, a repository-root-relative slash path as git status
// lists it.
func (r envRule) matches(rel string) bool {
	abs := filepath.Join(r.top, filepath.FromSlash(rel))
	switch {
	case isEnvName(filepath.Base(abs)), r.named[abs], resolvesInto(abs, r.targets),
		r.git != nil && r.git.hides(rel):
		return true
	case r.composeUnreadable:
		return !r.compose[abs]
	}
	return false
}

// isEnvName: the basename, case-insensitively, is "env" or ".env", starts with
// ".env" (.envrc, .env-prod, .env_local, .env.prod), ends with ".env"
// (prod.env) or contains ".env." (app.env.local).
func isEnvName(base string) bool {
	b := strings.ToLower(base)
	return b == "env" || strings.HasPrefix(b, ".env") || strings.HasSuffix(b, ".env") || strings.Contains(b, ".env.")
}

// envNamePathspecs find env-named paths with ls-files. They are glob magic,
// so globPathspecEnv turns off any inherited GIT_*_PATHSPECS that would read
// them literally. isEnvName re-checks every hit.
var envNamePathspecs = []string{
	":(glob,icase)**/env", ":(glob,icase)**/.env*", ":(glob,icase)**/*.env", ":(glob,icase)**/*.env.*",
}

var globPathspecEnv = []string{
	"GIT_LITERAL_PATHSPECS=0", "GIT_GLOB_PATHSPECS=0", "GIT_NOGLOB_PATHSPECS=0", "GIT_ICASE_PATHSPECS=0",
}

// envRule is built from every stack inside top, each stack's files read from
// its own folder: a file in the requested folder can be named by a stack
// registered above it, below it or beside it (agent-os-0zjs).
func (s *GitService) envRule(dirPath, top string, cred gitCreds) (envRule, error) {
	stacks, unplaced, err := s.repoStacks(top)
	if err != nil {
		return envRule{}, err
	}
	r := envRule{named: map[string]bool{}, targets: map[string]bool{}, compose: map[string]bool{}, top: top}
	// A stack that can't be placed may name env files here, as in the
	// history view.
	for _, u := range unplaced {
		slog.Warn("Treating every changed file of the folder as an env file: a stack folder could not be placed",
			"stack", u.stack.ProjectName, "error", u.err)
		r.composeUnreadable = true
	}
	src := diskComposeSource(top)
	for _, st := range stacks {
		stackDir := filepath.Join(top, filepath.FromSlash(st.rel))
		if st.EnvFile != "" {
			r.named[diskPath(stackDir, st.EnvFile)] = true
		}
		if st.ComposeFile == "" {
			continue
		}
		composePath := diskPath(stackDir, st.ComposeFile)
		r.compose[composePath] = true
		// Only the stack's own compose file is exempt when composeUnreadable
		// hides the rest: a file reached through include: or extends: stays
		// hidden, as it was before the parser followed them.
		named, _, err := composeSecretFiles(src, composePath)
		if err != nil {
			slog.Warn("Treating every changed file of the folder as an env file: the compose file's env_file entries are unknown",
				"path", composePath, "error", err)
			r.composeUnreadable = true
			continue
		}
		for _, p := range named {
			r.named[p] = true
		}
	}

	candidates := make([]string, 0, len(r.named))
	for p := range r.named {
		candidates = append(candidates, p)
	}
	targets, err := s.envLinkTargets(top, dirPath, cred, candidates)
	if err != nil {
		return envRule{}, err
	}
	r.targets = targets

	// The union with HEAD and the index. A fault reading either (a missing
	// or corrupt object) hides every change but the compose files rather
	// than failing the dialog. An unborn
	// branch (exit 1) has no HEAD to name anything, but its index can.
	var trees []string
	_, err = s.gitCommandWithCreds(top, cred, "rev-parse", "-q", "--verify", "HEAD^{commit}")
	switch {
	case err == nil:
		trees = []string{"HEAD"}
	case gitExitCode(err) != 1:
		slog.Warn("Treating every changed file of the folder as an env file: HEAD could not be read",
			"path", top, "error", err)
		r.composeUnreadable = true
		return r, nil
	}
	git, err := s.historyEnvRule(dirPath, top, false, cred, trees, true, "commit dialog")
	if err != nil {
		slog.Warn("Treating every changed file of the folder as an env file: the env files named at HEAD or in the index are unknown",
			"path", top, "error", err)
		r.composeUnreadable = true
		return r, nil
	}
	r.git = &git
	return r, nil
}

// envLinkTargets resolves, on today's disk, where env files really live: the
// candidates (absolute), every env-named tracked path of the repository and
// every env-named untracked path under untrackedDir, with symlinks resolved.
func (s *GitService) envLinkTargets(top, untrackedDir string, cred gitCreds, candidates []string) (map[string]bool, error) {
	// Tracked: the whole repository, so a link in a sibling stack folder
	// (app/.env -> ../other/prod.secrets) flags its target here too.
	tracked, _, err := s.gitRun(top, cred, globPathspecEnv,
		append([]string{"-c", "core.fsmonitor=false", "--no-optional-locks", "ls-files", "-z", "--full-name", "--"}, envNamePathspecs...)...)
	if err != nil {
		return nil, fmt.Errorf("failed to list the tracked env files: %w", err)
	}
	// Untracked, ignored ones included: a .env is usually gitignored. Not
	// --exclude-standard, which would drop exactly that file.
	untracked, _, err := s.gitRun(untrackedDir, cred, globPathspecEnv,
		append([]string{"-c", "core.fsmonitor=false", "--no-optional-locks", "ls-files", "-z", "--full-name", "--others", "--"}, envNamePathspecs...)...)
	if err != nil {
		return nil, fmt.Errorf("failed to list the untracked env files: %w", err)
	}
	for _, rel := range strings.Split(tracked+"\x00"+untracked, "\x00") {
		if rel != "" && isEnvName(filepath.Base(rel)) {
			candidates = append(candidates, filepath.Join(top, filepath.FromSlash(rel)))
		}
	}
	// Resolved by the deepest existing ancestor, so a deleted file named
	// through a symlinked stacks root still maps to where it was
	// (agent-os-odjs). A dangling link maps to itself.
	targets := map[string]bool{}
	for _, p := range candidates {
		resolved, err := pathutil.ResolveExisting(p)
		if err != nil {
			// Its lexical spelling is still in named.
			continue
		}
		targets[resolved] = true
	}
	return targets, nil
}

// resolvesInto reports whether abs, resolved as envLinkTargets resolves its
// candidates, is in targets. A path that can't be resolved is (fail closed).
func resolvesInto(abs string, targets map[string]bool) bool {
	resolved, err := pathutil.ResolveExisting(abs)
	if err != nil {
		return true
	}
	return targets[resolved]
}

// selectCommitPaths checks every requested file against the change list just
// read under the lock, and returns the pathspecs to commit: a rename brings its
// original path, or HEAD would keep a copy and the index a dangling delete.
func selectCommitPaths(dirPath, top string, changes []models.GitChange, req CommitRequest) ([]string, error) {
	byPath := make(map[string]models.GitChange, len(changes))
	for _, c := range changes {
		byPath[c.Path] = c
	}
	seen := map[string]bool{}
	var paths []string
	envNamed := false
	add := func(p string) error {
		ok, err := inFolder(dirPath, filepath.Join(top, filepath.FromSlash(p)))
		if err != nil {
			return fmt.Errorf("failed to check that %q is inside the stack folder: %w", p, err)
		}
		if !ok {
			return models.NewAppError(http.StatusBadRequest, models.ErrValidation,
				fmt.Sprintf("%q is outside this stack's folder", p))
		}
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
		return nil
	}
	for _, f := range req.Files {
		c, ok := byPath[f]
		if !ok {
			return nil, models.NewAppError(http.StatusBadRequest, models.ErrValidation,
				fmt.Sprintf("%q is not a changed tracked file in this stack's folder", f))
		}
		if err := add(f); err != nil {
			return nil, err
		}
		if c.OrigPath != "" {
			if err := add(c.OrigPath); err != nil {
				return nil, err
			}
		}
		envNamed = envNamed || c.IsEnv
	}
	if envNamed && !req.ConfirmEnv {
		return nil, models.NewAppError(http.StatusBadRequest, models.ErrGitEnvConfirmRequired,
			"The selection includes an env file, which can hold secrets; confirm to commit it")
	}
	return paths, nil
}

// commitPaths commits paths (repository-root-relative) from top and returns
// the new HEAD. Hooks, fsmonitor and signing are off: the repository's config
// is not trusted to run code. Pathspecs are literal, so a file named ":!x"
// is that file and not pathspec magic. The author comes from the settings
// through the environment, which beats both config and inherited
// GIT_AUTHOR_*/GIT_COMMITTER_* values.
func (s *GitService) commitPaths(top string, cred gitCreds, name, email, message string, paths []string) (string, error) {
	args := []string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.fsmonitor=false",
		"-c", "commit.gpgSign=false",
		"commit", "--only", "-m", message, "--",
	}
	env := []string{
		"GIT_LITERAL_PATHSPECS=1",
		"GIT_GLOB_PATHSPECS=0", "GIT_NOGLOB_PATHSPECS=0", "GIT_ICASE_PATHSPECS=0",
		"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email,
		"GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + email,
	}
	_, stderr, err := s.gitRun(top, cred, env, append(args, paths...)...)
	if err != nil {
		// LC_ALL=C is pinned on the child, so this text is git's English.
		if gitExitCode(err) == 128 && strings.Contains(stderr, "index.lock': File exists") {
			return "", models.NewAppError(http.StatusConflict, models.ErrGitOperationInProgress,
				"Another git process is using this repository; try again")
		}
		return "", err
	}
	hash, err := s.gitCommandWithCreds(top, cred, "rev-parse", "HEAD")
	if err != nil {
		// The commit landed; only the read of its hash failed.
		slog.Error("Committed but could not read the new HEAD", "path", top, "error", err)
		return "unknown", nil
	}
	return hash, nil
}

// pushPreconditions refuses every push it can predict will be wrong, using
// read-only git children: no upstream, an upstream in this same repository
// (remote "."), or a push URL outside the allow-list. It returns the remote
// and the ref to push HEAD to.
func (s *GitService) pushPreconditions(top, branch string, cred gitCreds) (pushTarget, error) {
	noUpstream := models.NewAppError(http.StatusConflict, models.ErrGitNoUpstream,
		"Branch "+branch+" tracks no upstream branch; Capstan does not set one")
	if _, err := s.gitCommandWithCreds(top, cred, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err != nil {
		if gitExitCode(err) < 0 {
			return pushTarget{}, fmt.Errorf("failed to read the upstream: %w", err)
		}
		return pushTarget{}, noUpstream
	}
	// Every value, not `config --get`: that answers the LAST value while
	// @{upstream} resolves the FIRST, so a second branch.<b>.merge would push
	// HEAD to a branch the upstream does not name.
	remotes, err := s.gitConfigValues(top, cred, "branch."+branch+".remote")
	if err != nil {
		return pushTarget{}, err
	}
	merges, err := s.gitConfigValues(top, cred, "branch."+branch+".merge")
	if err != nil {
		return pushTarget{}, err
	}
	if len(remotes) == 0 || len(merges) == 0 {
		return pushTarget{}, noUpstream
	}
	if len(remotes) > 1 || len(merges) > 1 {
		return pushTarget{}, models.NewAppError(http.StatusConflict, models.ErrGitNoUpstream,
			"Branch "+branch+" has more than one upstream configured; Capstan pushes to exactly one")
	}
	remote, mergeRef := remotes[0], merges[0]
	if remote == "" || mergeRef == "" {
		return pushTarget{}, noUpstream
	}
	// A remote NAMED by a URL (a hand-written config subsection) would carry
	// that URL, and any userinfo in it, into the reason, the details and the
	// action log. Refused before anything echoes the name.
	if strings.Contains(remote, "://") || strings.Contains(remote, "@") {
		return pushTarget{}, models.NewAppError(http.StatusConflict, models.ErrGitPushURLUnsupported,
			"Branch "+branch+" tracks a remote named by a URL; Capstan pushes only to a named remote")
	}
	if remote == "." {
		// Pushing to "." moves another LOCAL branch and reports success.
		return pushTarget{}, models.NewAppError(http.StatusConflict, models.ErrGitNoUpstream,
			"Branch "+branch+" tracks a local branch, not a remote; nothing would be pushed anywhere")
	}

	// --push --all applies pushurl and pushInsteadOf, which can differ from
	// the fetch URL. The URLs are never echoed: one can carry a token.
	urls, err := s.gitCommandWithCreds(top, cred, "remote", "get-url", "--push", "--all", remote)
	if err != nil {
		if gitExitCode(err) < 0 {
			return pushTarget{}, fmt.Errorf("failed to read the push URL: %w", err)
		}
		return pushTarget{}, models.NewAppError(http.StatusConflict, models.ErrGitNoUpstream,
			"Branch "+branch+" tracks "+remote+", which is not a configured remote")
	}
	pushURLs := strings.Split(urls, "\n")
	if len(pushURLs) != 1 {
		// git pushes to each URL in turn, so one ref line could report the
		// first while a later one failed or went somewhere else.
		return pushTarget{}, models.NewAppError(http.StatusConflict, models.ErrGitPushURLUnsupported,
			"Remote "+remote+" has more than one push URL; Capstan pushes to exactly one URL")
	}
	if refusal := pushURLRefusal(remote, strings.TrimSpace(pushURLs[0]), cred); refusal != nil {
		return pushTarget{}, refusal
	}
	return pushTarget{remote: remote, mergeRef: mergeRef}, nil
}

// gitConfigValues reads every value of a config key; unset (exit 1) is none.
func (s *GitService) gitConfigValues(top string, cred gitCreds, key string) ([]string, error) {
	v, err := s.gitCommandWithCreds(top, cred, "config", "--get-all", key)
	if err != nil {
		if gitExitCode(err) == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read %s: %w", key, err)
	}
	return strings.Split(v, "\n"), nil
}

var (
	urlSchemeRe       = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.-]*)://`)
	transportHelperRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+.-]*::`)
)

// pushURLRefusal allow-lists the push URL: https://, http://, file://, a
// local path, or one git runs over SSH (isSSHRemoteURL) when cred carries a
// usable key. An SSH URL with no key applying is GIT_SSH_UNSUPPORTED: it
// would use whatever default key the container has. A key that applies but
// fails sshReady is GIT_SSH_KEY_UNUSABLE (agent-os-z0gl). git://, other
// schemes and <transport>:: helpers are refused.
func pushURLRefusal(remote, u string, cred gitCreds) error {
	unsupported := models.NewAppError(http.StatusConflict, models.ErrGitPushURLUnsupported,
		"Remote "+remote+" uses a transport Capstan does not push over; use https, http, file, ssh or a local path")
	if u == "" || transportHelperRe.MatchString(u) {
		return unsupported
	}
	if isSSHRemoteURL(u) {
		if cred.sshKey == "" && cred.sshErr == nil {
			return models.NewAppError(http.StatusConflict, models.ErrGitSSHUnsupported,
				"Remote "+remote+" pushes over SSH, but no SSH key applies to this directory; set its git auth to SSH with a key path, set the global SSH key in Settings > Git, or use an https remote")
		}
		return cred.sshReady()
	}
	if m := urlSchemeRe.FindStringSubmatch(u); m != nil {
		switch strings.ToLower(m[1]) {
		case "https", "http", "file":
			return nil
		default:
			return unsupported
		}
	}
	return nil
}

// push runs the push and classifies its answer from the --porcelain ref line,
// never from git's hint prose. It returns the pushed range on success, or
// upToDate when the remote already had it.
func (s *GitService) push(top string, cred gitCreds, t pushTarget) (rng string, upToDate bool, err error) {
	args := []string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.fsmonitor=false",
		"-c", "push.followTags=false",
		"-c", "push.recurseSubmodules=no",
		"-c", "push.gpgSign=false",
		// For a local or file:// remote git runs this program, and repository
		// config must not choose it. The flag, not `-c remote.<r>.receivepack`:
		// git keeps the FIRST receivepack value it reads, which is the
		// repository's (OBSERVED, git 2.47.3, TestPushIgnoresRepoReceivePack).
		"push", "--porcelain", "--receive-pack=git-receive-pack", t.remote, "HEAD:" + t.mergeRef,
	}
	stdout, _, runErr := s.gitRun(top, cred, nil, args...)
	flag, summary, found := porcelainRefLine(stdout)
	if runErr != nil {
		// The classified answers below carry a fixed message, and auth and
		// network failures share one, so git's own text (redacted by
		// gitRun) is logged here: it is the only place they differ.
		slog.Warn("git push failed", "path", top, "remote", t.remote, "error", runErr.Error())
	}

	if runErr == nil {
		switch {
		case found && flag == "=":
			return "", true, nil
		case found && (flag == " " || flag == "*"):
			return summary, false, nil
		}
		return "", false, fmt.Errorf("git push exited 0 with an unexpected answer %q", strings.TrimSpace(stdout))
	}
	if gitExitCode(runErr) < 0 {
		return "", false, runErr
	}
	if !found {
		// No ref line: git never reached the point of updating a ref. Auth
		// and network failures look the same here and share one answer; not
		// 401, which the frontend reads as a lost Capstan session.
		return "", false, models.NewAppErrorWithDetails(http.StatusBadGateway, models.ErrGitRemoteUnreachable,
			"Could not push to the git remote: it was unreachable or refused the credentials", runErr.Error())
	}
	if flag == "!" {
		if strings.Contains(summary, "(fetch first)") || strings.Contains(summary, "(non-fast-forward)") {
			return "", false, models.NewAppError(http.StatusConflict, models.ErrGitRemoteAhead,
				"The remote has commits this branch does not have; pull first")
		}
		return "", false, models.NewAppErrorWithDetails(http.StatusConflict, models.ErrGitPushRejected,
			"The remote refused the push: "+summary, runErr.Error())
	}
	return "", false, fmt.Errorf("git push failed: %w", runErr)
}

// porcelainRefLine finds the ref line of `push --porcelain` output:
// "<flag>\t<from>:<to>\t<summary>". One ref is pushed, so the first is it.
func porcelainRefLine(stdout string) (flag, summary string, found bool) {
	for _, line := range strings.Split(stdout, "\n") {
		if len(line) < 2 || line[1] != '\t' {
			continue
		}
		f := strings.SplitN(line, "\t", 3)
		if len(f) != 3 {
			continue
		}
		return f[0], f[2], true
	}
	return "", "", false
}

// gitRun is gitCommandWithCreds for the callers that need more: extra child
// environment, and stdout and stderr untrimmed and kept on failure (a push's
// porcelain line is on stdout whether it succeeded or not). Same child, same
// deadline, same redaction of both streams. Cmd.Env keeps the last value of a
// duplicate key, so env overrides what gitCmdWithCreds forwarded.
func (s *GitService) gitRun(dirPath string, cred gitCreds, env []string, args ...string) (stdout, stderr string, err error) {
	ctx, cancel := withCommandDeadline(context.Background(), s.gitTimeout())
	defer cancel()
	cmd, _ := s.gitCmdWithCreds(ctx, dirPath, cred, args...)
	cmd.Env = append(cmd.Env, env...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	runErr := timeoutError(ctx, cmd.Run(), "command")
	stdout = redactToken(out.String(), cred.token)
	stderr = redactToken(errOut.String(), cred.token)
	if runErr != nil {
		return stdout, stderr, fmt.Errorf("git %s: %w (%s)", gitSubcommand(args), runErr, gitDiagnostic(stdout, stderr))
	}
	return stdout, stderr, nil
}

// gitSubcommand names the subcommand in args, past any leading options and
// `-c key=value` pairs, for an error message.
func gitSubcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-c":
			i++
		case strings.HasPrefix(args[i], "-"):
		default:
			return args[i]
		}
	}
	return "command"
}
