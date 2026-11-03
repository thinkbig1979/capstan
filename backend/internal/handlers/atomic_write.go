package handlers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
// The rename creates a new inode owned by the process user, and needs write
// permission on the directory, not just on the file.
//
// A path that is a symlink is written through: the rename lands on the file
// the link resolves to and the link stays a link (agent-os-dwmz). Renaming
// onto the link itself replaced it with a plain file and left its target,
// often in a git checkout, unwritten. Containment is the caller's job
// (validateStackPath resolves links too); this helper has no roots to check.
func writeFileAtomic(path string, content []byte, mode os.FileMode) error {
	target, _, err := resolveWriteTarget(path)
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
// the link's own name to its parent), so creating its target is not safe.
func resolveWriteTarget(path string) (string, os.FileInfo, error) {
	name := filepath.Base(path)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
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
			return "", nil, fmt.Errorf("%s is a symlink to a missing file (%s); fix or remove the link, then save again", name, dest)
		}
		if err != nil {
			return "", nil, fmt.Errorf("%s is a symlink that cannot be followed (%w); fix or remove the link, then save again", name, err)
		}
		if info, err = os.Stat(resolved); err != nil {
			return "", nil, fmt.Errorf("check %s's link target: %w", name, err)
		}
		target = resolved
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("%s is not a regular file (%s); nothing written", name, info.Mode().Type())
	}
	return target, info, nil
}

// renameIntoPlace is writeFileAtomic for a path already resolved by
// resolveWriteTarget.
func renameIntoPlace(path string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

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
// keeps its permission bits (a stack edited from the host may be 0664); a new
// one gets composeFileMode. An existing file the process cannot open for
// writing is refused: a rename would replace it regardless of its own mode, and
// the in-place write this helper replaced honoured a read-only compose file, so
// the save still needs both file and directory write permission.
//
// Through a symlink, the mode and the write check are the target's: a link's
// own mode is always 0777.
func writeComposeFileAtomic(path string, content []byte) error {
	target, info, err := resolveWriteTarget(path)
	if err != nil {
		return err
	}
	mode := composeFileMode
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
	return renameIntoPlace(target, content, mode)
}

// writeEnvFileAtomic writes an env file atomically at exactly 0600, whatever
// mode an existing file had: env files hold secrets, so a looser mode left by
// older code is tightened rather than kept.
func writeEnvFileAtomic(envPath, content string) error {
	return writeFileAtomic(envPath, []byte(content), 0600)
}
