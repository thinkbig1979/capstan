package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/pathutil"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// composeFileMode is the mode of a compose file that does not exist yet.
const composeFileMode os.FileMode = 0644

// writeFileAtomic writes content to path through a temp file in the target's
// own directory plus os.Rename, so a crash, OOM kill or ENOSPC leaves either the
// old file or the new one, never a truncated one (safe-defaults rule 7). Every
// write of a file under a stack directory goes through here; the one place in
// the handlers that calls os.CreateTemp and os.Rename.
//
// The temp file is chmod-ed to mode on its fd, so the final file lands at
// exactly mode (umask does not apply) and is never visible at a looser one.
// The file is fsynced before the rename, because a rename that reaches disk
// ahead of the data leaves a zero-length file after a power loss. The
// directory is not fsynced: that only decides whether the rename itself
// survives, and either outcome is a whole file.
//
// The rename creates a new inode owned by the process user (writeFileAtomic
// does not chown it; the compose and env helpers below keep the old owner), and
// needs write permission on the directory, not just on the file.
//
// A path that is a symlink is written through: the rename lands on the file
// the link resolves to and the link stays a link (agent-os-dwmz). Renaming
// onto the link itself replaced it with a plain file and left its target,
// often in a git checkout, unwritten. The file written must lie inside one of
// roots (the stacks directories, or DataDir for global.env), checked after the
// link is resolved; the caller's own check came earlier (agent-os-3b2d).
func writeFileAtomic(path string, content []byte, mode os.FileMode, roots []string) error {
	target, _, err := resolveWriteTarget(path, roots)
	if err != nil {
		return err
	}
	return renameIntoPlace(target, content, mode)
}

// resolveWriteTarget returns the file a write to path must replace: path
// itself, or the end of its symlink chain. info is that file's, or nil when it
// does not exist yet. A link that dangles or loops, and a target that is not a
// regular file, are refused: a dangling link passes validateStackPath even
// when it points outside every stacks root (pathutil.ResolveExisting appends
// the link's own name to its parent), so creating its target is not safe. So
// is a target outside every one of roots (checkWriteContained).
func resolveWriteTarget(path string, roots []string) (string, os.FileInfo, error) {
	name := filepath.Base(path)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := checkWriteContained(path, path, roots); err != nil {
			return "", nil, err
		}
		return path, nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("check %s: %w", name, err)
	}
	target := path
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(path)
		if errors.Is(err, os.ErrNotExist) {
			dest, _ := os.Readlink(path) //nolint:errcheck // only decorates the message; Lstat just saw a link here
			return "", nil, &writeRefusal{msg: fmt.Sprintf("%s is a symlink to a missing file (%s); fix or remove the link, then save again", name, dest)}
		}
		if err != nil {
			return "", nil, unfollowableLinkRefusal(name, err)
		}
		if info, err = os.Stat(resolved); err != nil {
			return "", nil, fmt.Errorf("check %s's link target: %w", name, err)
		}
		target = resolved
	}
	if !info.Mode().IsRegular() {
		return "", nil, &writeRefusal{msg: fmt.Sprintf("%s is not a regular file (%s); nothing written", name, info.Mode().Type())}
	}
	if err := checkWriteContained(path, target, roots); err != nil {
		return "", nil, err
	}
	return target, info, nil
}

// checkWriteContained refuses a write whose target is not inside any of roots
// (agent-os-3b2d, safe-defaults rule 6). The handlers check the path before
// they write (validateStackPath), but a link swapped after that check was
// followed here and written through, and global.env had no check at all. This
// narrows the window to the rename below; it does not close it. A root that
// cannot be checked counts as not containing the target, and with no root
// left the write is refused: fail closed.
func checkWriteContained(path, target string, roots []string) error {
	var checkErr error
	for _, root := range roots {
		ok, err := pathutil.IsContained(root, target)
		if err != nil {
			checkErr = errors.Join(checkErr, err)
			continue
		}
		if ok {
			return nil
		}
	}
	if checkErr != nil {
		return fmt.Errorf("check where %s leads: %w", filepath.Base(path), checkErr)
	}
	where := strings.Join(roots, ", ")
	if target != path {
		return &writeRefusal{msg: fmt.Sprintf("%s links to a file outside %s (%s); nothing written. Point the link inside it, or replace the link with the file", filepath.Base(path), where, target)}
	}
	return &writeRefusal{msg: fmt.Sprintf("%s is outside %s; nothing written", path, where)}
}

// writeRefusal is resolveWriteTarget refusing a path because of the user's
// own link or file (a dangling or looping link, a target that is not a regular
// file). Its text says what to do, so it is answered 409 with that text
// (agent-os-4hxa). Every other write error is a server fault and stays a 500
// whose cause is logged, not sent: it can name OS internals.
type writeRefusal struct{ msg string }

func (e *writeRefusal) Error() string { return e.msg }

// unfollowableLinkRefusal is the refusal for a link named name that
// EvalSymlinks could not follow (a loop) with err.
func unfollowableLinkRefusal(name string, err error) *writeRefusal {
	return &writeRefusal{msg: fmt.Sprintf("%s is a symlink that cannot be followed (%v); fix or remove the link, then save again", name, err)}
}

// writeFailureError is the AppError for a write helper's error: 409 with the
// refusal's text, or a 500 carrying code and message with err as its cause.
func writeFailureError(err error, code, message string) *models.AppError {
	var refusal *writeRefusal
	if errors.As(err, &refusal) {
		return models.NewAppError(http.StatusConflict, models.ErrValidation, refusal.msg)
	}
	return models.NewAppErrorWithCause(http.StatusInternalServerError, code, message, err)
}

// renderWriteFailure renders a write helper's error as a failed ActionResult:
// 409 with the refusal's text, or 500 with reason. note is appended to either
// (what else the handler undid, say).
func renderWriteFailure(c *gin.Context, err error, reason, note string) {
	var refusal *writeRefusal
	if errors.As(err, &refusal) {
		renderResultWithStatus(c, http.StatusConflict, truth.Failed(refusal.msg+note, err))
		return
	}
	renderResult(c, truth.Failed(reason+note, err))
}

// renameIntoPlace is writeFileAtomic for a path already resolved by
// resolveWriteTarget.
func renameIntoPlace(path string, content []byte, mode os.FileMode) error {
	return renameIntoPlaceOwned(path, content, mode, nil)
}

// fileOwner is the uid and gid a written file must keep. bestEffort says what
// a failed chown does: refuse the write (false, the symlinked env file,
// agent-os-1ltz) or log it and save as the process owner (true, a plain compose
// or env file, agent-os-9deh: Edwin's "best effort, needs root").
type fileOwner struct {
	uid, gid   int
	bestEffort bool
}

// ownerToKeep is the owner a replaced file must keep: info's uid and gid when
// they differ from the process's, nil when they match (no chown, so a
// non-root install saving its own files never needs one) or info is nil (a new
// file).
func ownerToKeep(info os.FileInfo, bestEffort bool) *fileOwner {
	if info == nil {
		return nil
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	uid, gid := processOwner()
	if int(st.Uid) == uid && int(st.Gid) == gid {
		return nil
	}
	return &fileOwner{uid: int(st.Uid), gid: int(st.Gid), bestEffort: bestEffort}
}

// processOwner and chownTempFile are seams for agent-os-1ltz's tests: an
// unprivileged test cannot own a file as another user, so it makes a target
// look foreign and records the chown instead. Production uses the real ones.
var (
	processOwner  = func() (uid, gid int) { return os.Geteuid(), os.Getegid() }
	chownTempFile = func(f *os.File, uid, gid int) error { return f.Chown(uid, gid) }
)

// renameIntoPlaceOwned is renameIntoPlace that, with a non-nil owner, gives
// the temp file that owner before the rename. A chown that fails refuses the
// write (409, nothing written): the rename would otherwise hand the file to
// the process user, the lock-out agent-os-1ltz is about.
func renameIntoPlaceOwned(path string, content []byte, mode os.FileMode, owner *fileOwner) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	if owner != nil {
		if err := chownTempFile(tmp, owner.uid, owner.gid); err != nil && owner.bestEffort {
			slog.Warn("Could not keep a file's owner; saving it as the user Capstan runs as",
				"file", path, "uid", owner.uid, "gid", owner.gid, "error", err)
		} else if err != nil {
			tmp.Close()
			return &writeRefusal{msg: fmt.Sprintf("%s belongs to uid %d, gid %d, and Capstan cannot write it as that owner (%v); nothing written. Save it from the host, or give it to the user Capstan runs as",
				base, owner.uid, owner.gid, errors.Unwrap(err))}
		}
	}
	// After the chown: a chown can clear mode bits.
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp file into place: %w", err)
	}
	return nil
}

// writeComposeFileAtomic writes a compose file atomically. An existing file
// keeps its permission bits (a stack edited from the host may be 0664) and,
// best effort, its owner and group (agent-os-9deh: a failed chown, as on a
// non-root install saving another user's file, is logged and the file ends up
// the process user's); a new one gets composeFileMode. An existing file the process cannot open for
// writing is refused: a rename would replace it regardless of its own mode, and
// the in-place write this helper replaced honoured a read-only compose file, so
// the save still needs both file and directory write permission.
//
// Through a symlink, the mode and the write check are the target's: a link's
// own mode is always 0777.
func writeComposeFileAtomic(path string, content []byte, roots []string) error {
	target, info, err := resolveWriteTarget(path, roots)
	if err != nil {
		return err
	}
	mode := composeFileMode
	owner := ownerToKeep(info, true)
	if info != nil {
		mode = info.Mode().Perm()
		//nolint:gosec // target is a compose path the caller validated against the stacks directories (validateStackPath, symlink-aware); opened write-only without O_TRUNC, closed at once, to test permission only
		f, openErr := os.OpenFile(target, os.O_WRONLY, 0)
		if openErr != nil {
			return fmt.Errorf("existing compose file is not writable: %w", openErr)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("close compose file after write check: %w", err)
		}
	}
	return renameIntoPlaceOwned(target, content, mode, owner)
}

// writeEnvFileAtomic writes an env file atomically at exactly 0600, whatever
// mode an existing file had: env files hold secrets, so a looser mode left by
// older code is tightened rather than kept. A plain file keeps its owner and
// group best effort, as writeComposeFileAtomic does (agent-os-9deh).
//
// Through a symlink it keeps the target's mode and owner instead
// (agent-os-1ltz, Edwin's ruling 2026-11-14): a link is how a stack shares an
// env file with the host, and a save must not turn that file 0600 and
// process-owned, locking its owner out. The target must still lie inside one
// of roots (checkWriteContained), so a link out of them is refused. The owner is set only when it differs from the
// process's, so a non-root install whose own files are linked is unaffected;
// when it cannot be kept, the save is refused (fail closed).
func writeEnvFileAtomic(envPath, content string, roots []string) error {
	target, info, err := resolveWriteTarget(envPath, roots)
	if err != nil {
		return err
	}
	mode := os.FileMode(0600)
	// A plain env file keeps its owner best effort (agent-os-9deh); through a
	// link the owner must be kept or the save is refused (agent-os-1ltz).
	owner := ownerToKeep(info, true)
	if target != envPath && info != nil {
		mode = info.Mode().Perm()
		owner = ownerToKeep(info, false)
	}
	return renameIntoPlaceOwned(target, []byte(content), mode, owner)
}
