package services

import (
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"

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

// GetStatus was a go-git call with a git-CLI fallback until agent-os-yo9e. The
// two implementations were MEASURED against a fifteen-state fixture corpus and
// were not equivalent in either direction, so "one is redundant" was false: each
// answered something the other got wrong. The CLI path won on the substance —
// it resolves the real @{upstream} instead of assuming origin/<same-name>, it
// still reports RemoteURL when no remote-tracking ref exists, and it can read a
// linked worktree, whose .git is a file that go-git cannot open at all. The one
// field only go-git served, TrackingBranch, moved into getStatusCLI first; the
// deletion followed. See git_parity_yo9e_test.go for the corpus.
//
// There is no fallback left to mask a failure, which is the point: an error
// from here is now the answer, not a reason to ask a second implementation.
func (s *GitService) GetStatus(dirPath string) (*models.GitStatusResult, error) {
	return s.getStatusCLI(dirPath)
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

	// TrackingBranch was served only by the deleted go-git path until
	// agent-os-yo9e, so this is a port — but not a transcription. go-git
	// hardcoded origin/<same-branch-name>, which is simply wrong for a branch
	// whose upstream has a different name; MEASURED, a branch `deploy` tracking
	// origin/main got ahead=0 from go-git and the true ahead=1 from this
	// function, because ahead/behind above already ask git for @{upstream}.
	// Asking the same question here makes TrackingBranch, Ahead and Behind
	// agree by construction instead of by coincidence.
	//
	// Exit codes only, never message text: git translates its errors.
	trackingBranch := ""
	// A detached HEAD has no upstream and no conventional tracking ref either.
	// The guard is not redundant with the @{upstream} probe below: MEASURED, a
	// clone carries refs/remotes/origin/HEAD as a symbolic ref that
	// `rev-parse --verify` resolves happily, so without this the fallback would
	// report the nonsense "origin/HEAD" for every detached checkout — a state
	// where go-git reported "".
	if branch != "HEAD" {
		if output, err := s.gitCommandWithCreds(dirPath, user, token,
			"rev-parse", "--abbrev-ref", "@{upstream}"); err == nil {
			trackingBranch = strings.TrimSpace(output)
		} else if _, refErr := s.gitCommandWithCreds(dirPath, user, token,
			"rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch); refErr == nil {
			// No upstream configured, but a remote-tracking ref of the
			// conventional name exists — the state left by a bare `git fetch`
			// with no branch.<name>.merge. go-git answered origin/<branch>
			// here, and dropping it would be an unannounced regression for
			// those repositories, so the field is preserved.
			//
			// agent-os-yo9e originally left Ahead/Behind at 0 here, on the
			// reasoning that counting against a ref the operator never
			// configured is a guess. agent-os-ct4e reverses that: the function
			// is already willing to NAME this ref as the tracking branch, so
			// refusing to count against it made getStatusCLI report "tracking
			// origin/main" and "0 behind" in one breath, where the 0 was
			// measured against nothing. go-git counted here, and dropping it
			// was an undeclared regression — GitStatus.tsx gates its chip on
			// `behind > 0`, so a stack that was genuinely behind rendered NO
			// chip rather than a zero, losing the dashboard's only "you need to
			// pull" signal.
			trackingBranch = "origin/" + branch
		}
	}

	// Counted against trackingBranch rather than @{upstream} so the three
	// fields agree BY CONSTRUCTION rather than by coincidence. Where an
	// upstream is configured, trackingBranch IS its abbrev-ref and this is the
	// same question; where only the conventional remote-tracking ref exists,
	// this is the ref the field above already names. Where neither exists
	// trackingBranch is "" and both counts stay 0, which is the honest answer.
	ahead := 0
	behind := 0
	if trackingBranch != "" {
		if output, err := s.gitCommandWithCreds(dirPath, user, token,
			"rev-list", "--left-right", "--count", trackingBranch+"...HEAD"); err == nil {
			parts := strings.Fields(output)
			if len(parts) == 2 {
				behind, _ = strconv.Atoi(parts[0])
				ahead, _ = strconv.Atoi(parts[1])
			}
		}
	}

	remoteURL, _ := s.gitCommandWithCreds(dirPath, user, token, "remote", "get-url", "origin")
	// redactToken has already run on this value, but it only removes the token
	// Capstan itself resolved, so a credential the operator embedded
	// independently survives it (agent-os-57xj). RemoteURL carries a json tag,
	// so redacting at the source keeps "the struct never holds a credential" an
	// invariant instead of a per-caller duty.
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
		Dirty:          dirty,
		DirtyCount:     dirtyCount,
		Ahead:          ahead,
		Behind:         behind,
		RemoteURL:      remoteURL,
		TrackingBranch: trackingBranch,
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

	if _, err = s.gitCommandWithCreds(dirPath, user, token, "pull", "--ff-only"); err != nil {
		// Two defects left here together (agent-os-fv2j). Every failure used
		// to become one 500 GIT_CONFLICT, and the up-to-date case was
		// recognised by matching git's own prose. pullFailure replaces the
		// first by asking git what went wrong; the second needed no
		// replacement at all, because an up-to-date pull never reaches this
		// branch. OBSERVED, git 2.47.3, on an up-to-date clone:
		//
		//	$ git pull --ff-only; echo $?
		//	Already up to date.
		//	0
		//
		// err is nil, so the fall-through below reads HEAD again, finds it
		// equal to previousCommit and returns the no-change result on its
		// own. The string match was a leftover from the go-git implementation
		// this replaced, where the no-op was signalled by a non-nil
		// NoErrAlreadyUpToDate sentinel rather than by success.
		return nil, s.pullFailure(dirPath, user, token, err)
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

// pullFailure classifies a failed `git pull --ff-only` in dirPath into the error
// its caller should surface (agent-os-fv2j).
//
// Before this, every failure of that one command returned 500 GIT_CONFLICT
// "Failed to pull". A rotated token, a DNS outage, a deleted remote, a branch
// tracking nothing and a genuinely diverged branch were one answer. Only the
// last is a conflict, and an operator reading GIT_CONFLICT goes looking for a
// divergence that is not there while the real cause — usually an expired token
// — sits in the log.
//
// Same discipline as gitFailure and hasUnbornHead: put the questions to git and
// read EXIT CODES, never its prose. Git translates its messages, and the fact
// that agent-os-vq3p pins the child to LC_ALL=C makes text matching possible
// rather than safe — it would then be correct only for as long as that pin
// survives, and any future call path that builds its own child env re-arms it
// silently.
//
// The exit status of `git pull` itself is not enough to split these. MEASURED,
// six failure shapes driven through the real command: a divergence exits 128
// while auth, DNS, a missing remote and a missing upstream ALL exit 1. So the
// classification is three follow-up probes, whose exit codes do separate them
// cleanly.
//
// The table below is MEASURED IN THREE ENVIRONMENTS and is IDENTICAL in all
// three, cell for cell:
//
//	git 2.47.3, developer host, host global git config in effect
//	git 2.47.3, developer host, GIT_CONFIG_GLOBAL=/dev/null
//	git 2.43.7, alpine:3.19 container (the version family CI runs)
//
// Naming the environment next to the numbers is deliberate. The first row is
// the only one that was originally measured, and it is the one CI does not
// share -- init.defaultBranch is ambient user config, and a measurement taken
// only under it can be true and still not generalise. Every probe below is
// driven against an explicitly named branch, so that setting does not
// participate; the other two rows are what establish that rather than assume
// it.
//
// The version spread matters because a classifier tuned to one git would be a
// version-dependent misclassification, worse than the flat GIT_CONFLICT it
// replaced -- wrong only sometimes. It also settles a specific false lead: a
// CI failure on this test was hypothesised to be a 2.43-vs-2.47 exit-code
// difference. There is no such difference. The cause was the test fixture, not
// the probes; see pullFixture.
//
//	                         ls-remote  rev-parse @{u}  is-ancestor HEAD @{u}
//	diverged, non-ff             0            0                1
//	remote path missing        128            0                0
//	no remote configured       128            1              128
//	DNS failure                128            0                0
//	auth failure               128            0                0
//	up to date                   0            0                0
//	reachable, no upstream       0            1              128
//
// Read in order, that is: can the remote be read at all; if so does this branch
// track anything; if so is HEAD merely behind it. The upstream probe is not
// redundant — the last row is a branch with no upstream, where `is-ancestor`
// exits 128 for want of a revision to resolve, and without that probe the
// failure would be reported as a divergence.
//
// Auth and network are deliberately not separated. Git does not distinguish
// them by exit code, only in the message text, and splitting them would mean
// exactly the prose matching this change removes. They share one answer:
// GIT_REMOTE_UNREACHABLE, 502, "the far end could not be read". Not 401 — that
// is the one status frontend/src/lib/api.ts branches on, and a git credential
// failure is not a Capstan session failure (agent-os-318).
//
// The unclassified cases return a plain wrapped error rather than inventing a
// diagnosis, which handleError turns into a 500 INTERNAL_ERROR and logs
// (agent-os-7z8c) — the same choice getStatusCLI makes for a HEAD that resolves
// but whose branch name will not read.
//
// ls-remote contacts the remote, so this costs a round trip. It runs only after
// a pull has already failed, so the happy path pays nothing, and
// GIT_TERMINAL_PROMPT=0 in the child env keeps it from blocking on a prompt.
func (s *GitService) pullFailure(dirPath, user, token string, err error) error {
	if _, probeErr := s.gitCommandWithCreds(dirPath, user, token, "ls-remote", "--quiet"); probeErr != nil {
		return models.NewAppErrorWithDetails(502, models.ErrGitRemoteUnreachable,
			"Could not read from the git remote", err.Error())
	}
	if _, upstreamErr := s.gitCommandWithCreds(dirPath, user, token,
		"rev-parse", "--verify", "--quiet", "@{upstream}"); upstreamErr != nil {
		return fmt.Errorf("git pull failed and the branch tracks no upstream: %w", err)
	}
	if _, ancestorErr := s.gitCommandWithCreds(dirPath, user, token,
		"merge-base", "--is-ancestor", "HEAD", "@{upstream}"); ancestorErr != nil {
		// EXIT 1 SPECIFICALLY, never "any non-zero". git documents 1 as "HEAD
		// is not an ancestor of the upstream" and reserves other non-zero
		// codes for errors -- an unresolvable revision exits 128, not 1.
		// Reading any failure as a divergence would repeat this bead's own
		// defect one level down: asserting a specific diagnosis from a generic
		// failure, which is the thing pullFailure exists to stop doing.
		if gitExitCode(ancestorErr) == 1 {
			return models.NewAppErrorWithDetails(409, models.ErrGitConflict,
				"Local branch has diverged from the remote and cannot be fast-forwarded", err.Error())
		}
		return fmt.Errorf("git pull failed and the divergence probe could not run: %w", err)
	}
	return fmt.Errorf("git pull failed: %w", err)
}

// gitExitCode returns the exit status of the git process behind err, or -1 when
// err did not come from a process that ran and exited.
//
// gitCommandWithCreds wraps CombinedOutput's error with %w, so the underlying
// *exec.ExitError survives the wrap. VERIFIED against that exact wrapping:
// errors.As returns true through it and recovers 128 from a failed
// `merge-base --is-ancestor`.
func gitExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
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
