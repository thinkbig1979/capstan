package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/pathutil"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// acquireStackLock takes stackID's operation lock for a handler, or answers
// 409 OPERATION_IN_PROGRESS (the lifecycle routes' code) and returns ok=false.
// The returned release is safe to call more than once. With no lock wired
// (test-built handlers) it takes nothing and release is a no-op.
func acquireStackLock(c *gin.Context, lock *services.OperationLock, stackID, kind string) (release func(), ok bool) {
	if lock == nil {
		return func() {}, true
	}
	token, err := lock.Acquire(stackID, kind)
	if err != nil {
		handleError(c, models.NewAppError(http.StatusConflict, models.ErrOperationInProgress, err.Error()))
		return nil, false
	}
	return sync.OnceFunc(func() { lock.Release(stackID, token) }), true
}

// stackLister is the one stacks-table read lockStacksSharingFiles needs.
type stackLister interface {
	ListStacks() ([]models.Stack, error)
}

// lockStacksSharingFiles is acquireStackLock for a save that writes paths: it
// also takes the lock of every other stack whose compose or env file is one of
// those files (agent-os-0j8a). A file is shared through a symlink in either
// direction, by same-folder stacks (a named stack falls back to .env, see
// determineEnvFile in services/scanner.go), or by a stack registered through a
// symlinked folder. Holding only the own stack's lock let the other stack's
// save run on the same file at once, and one of the two saves was lost.
//
// Acquire never waits, so the order the locks are taken in cannot deadlock. A
// busy sharer answers 409 naming it by project name. The sharers are found
// again once every lock is held, since a link in another stack's folder may
// have moved before its lock was taken; a different set answers 409 too. A
// fault while finding them answers 500: guessing would under-lock. Every
// refusal releases what was already taken; the release returned on success
// frees everything and is safe to call more than once.
func lockStacksSharingFiles(c *gin.Context, lock *services.OperationLock, db stackLister, stack *models.Stack, kind string, paths ...string) (release func(), ok bool) {
	if lock == nil {
		return func() {}, true
	}
	releaseOwn, ok := acquireStackLock(c, lock, stack.ID, kind)
	if !ok {
		return nil, false
	}
	sharers, err := stacksSharingFiles(db, stack.ID, paths)
	if err != nil {
		releaseOwn()
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR",
			"Failed to check which stacks share this stack's files", err))
		return nil, false
	}
	releaseSharers, busy, err := lockEach(lock, sharers, kind)
	if err != nil {
		releaseOwn()
		handleError(c, models.NewAppError(http.StatusConflict, models.ErrOperationInProgress,
			fmt.Sprintf("stack %s uses the same file: %v", busy.ProjectName, err)))
		return nil, false
	}
	releaseAll := sync.OnceFunc(func() {
		releaseSharers()
		releaseOwn()
	})
	again, err := stacksSharingFiles(db, stack.ID, paths)
	if err != nil {
		releaseAll()
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR",
			"Failed to check which stacks share this stack's files", err))
		return nil, false
	}
	if !slices.EqualFunc(sharers, again, func(a, b models.Stack) bool { return a.ID == b.ID }) {
		releaseAll()
		handleError(c, models.NewAppError(http.StatusConflict, models.ErrOperationInProgress,
			"The stacks sharing this stack's files changed while the save started; try again"))
		return nil, false
	}
	return releaseAll, true
}

// lockEach takes every stack's lock, or none: on the first busy stack it frees
// those already taken and returns that stack with the lock's error.
func lockEach(lock *services.OperationLock, stacks []models.Stack, kind string) (release func(), busy models.Stack, err error) {
	type held struct{ id, token string }
	var taken []held
	release = func() {
		for _, h := range taken {
			lock.Release(h.id, h.token)
		}
	}
	for _, st := range stacks {
		token, err := lock.Acquire(st.ID, kind)
		if err != nil {
			release()
			return nil, st, err
		}
		taken = append(taken, held{st.ID, token})
	}
	return release, models.Stack{}, nil
}

// sharedTarget is one file a save writes: its FileInfo when it exists, else
// the path it resolves to.
type sharedTarget struct {
	info     os.FileInfo
	resolved string
}

// stacksSharingFiles returns the stacks other than selfID whose compose or env
// file is one of paths, sorted by ID. An existing file matches by identity
// (os.SameFile, so any chain of links and a hard link count); one that does not
// exist yet matches a stack whose file resolves to the same path.
func stacksSharingFiles(db stackLister, selfID string, paths []string) ([]models.Stack, error) {
	targets := make([]sharedTarget, 0, len(paths))
	for _, p := range paths {
		info, err := os.Stat(p)
		switch {
		case err == nil:
			targets = append(targets, sharedTarget{info: info})
		case errors.Is(err, os.ErrNotExist):
			resolved, err := pathutil.ResolveExisting(p)
			if err != nil {
				return nil, err
			}
			targets = append(targets, sharedTarget{resolved: resolved})
		default:
			return nil, err
		}
	}

	stacks, err := db.ListStacks()
	if err != nil {
		return nil, err
	}
	var sharers []models.Stack
	for _, st := range stacks {
		if st.ID == selfID {
			continue
		}
		for _, name := range []string{st.ComposeFile, st.EnvFile} {
			if name == "" {
				continue
			}
			shared, err := isSharedTarget(filepath.Join(st.Directory, name), targets)
			if err != nil {
				return nil, fmt.Errorf("stack %s: %w", st.ProjectName, err)
			}
			if shared {
				sharers = append(sharers, st)
				break
			}
		}
	}
	slices.SortFunc(sharers, func(a, b models.Stack) int { return strings.Compare(a.ID, b.ID) })
	return sharers, nil
}

// isSharedTarget reports whether path is one of targets. A path that does not
// exist cannot be an existing target; any other error leaves the answer
// unknown and is returned.
func isSharedTarget(path string, targets []sharedTarget) (bool, error) {
	var info os.FileInfo
	for _, t := range targets {
		if t.info == nil {
			resolved, err := pathutil.ResolveExisting(path)
			if err != nil {
				return false, err
			}
			if resolved == t.resolved {
				return true, nil
			}
			continue
		}
		if info == nil {
			var err error
			info, err = os.Stat(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return false, err
			}
		}
		if os.SameFile(info, t.info) {
			return true, nil
		}
	}
	return false, nil
}

// acquireExclusiveLock is acquireStackLock for an operation that takes every
// stack's turn at once (a container prune): it answers 409 OPERATION_IN_PROGRESS
// naming the stack operation in the way, or the other exclusive holder, and
// returns ok=false. With no lock wired it takes nothing.
func acquireExclusiveLock(c *gin.Context, lock *services.OperationLock, kind string) (release func(), ok bool) {
	if lock == nil {
		return func() {}, true
	}
	token, err := lock.AcquireExclusive(kind)
	if err != nil {
		handleError(c, models.NewAppError(http.StatusConflict, models.ErrOperationInProgress, err.Error()))
		return nil, false
	}
	return sync.OnceFunc(func() { lock.ReleaseExclusive(token) }), true
}

// projectNameLookup is the one stacks-table read refuseSharedProjectName needs.
type projectNameLookup interface {
	GetStackByProjectName(projectName string) (*models.Stack, error)
}

// refuseSharedProjectName answers 409 AMBIGUOUS_STACK and returns true when
// another stack carries stack's compose project name: a compose command run
// with that `-p` would act on the other stack's containers too, under only
// this stack's lock (agent-os-z91e.38, owner decision D28). A lookup fault
// refuses with a 500 (fail closed). Called before the lock is taken and before
// anything is logged or persisted, so a refusal leaves no trace; the compose
// paths in DockerService refuse again for callers that are not handlers.
func refuseSharedProjectName(c *gin.Context, db projectNameLookup, stack *models.Stack) bool {
	_, err := db.GetStackByProjectName(stack.ProjectName)
	switch {
	case err == nil, errors.Is(err, errdefs.ErrNotFound):
		return false
	case errors.Is(err, errdefs.ErrAmbiguous):
		refuseAmbiguousStack(c, err)
	default:
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR",
			"Failed to check whether another stack shares this stack's compose project name", err))
	}
	return true
}
