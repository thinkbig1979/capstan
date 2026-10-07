package handlers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// composeFileMode is the mode of a compose file that does not exist yet.
const composeFileMode os.FileMode = 0644

// writeFileAtomic writes content to path through a temp file in path's own
// directory plus os.Rename, so a crash, OOM kill or ENOSPC leaves either the
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
func writeFileAtomic(path string, content []byte, mode os.FileMode) error {
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
func writeComposeFileAtomic(path string, content []byte) error {
	mode := composeFileMode
	info, err := os.Stat(path)
	switch {
	case err == nil:
		mode = info.Mode().Perm()
		//nolint:gosec // path is a compose path the caller validated against the stacks directories (validateStackPath); opened write-only without O_TRUNC, closed at once, to test permission only
		f, openErr := os.OpenFile(path, os.O_WRONLY, 0)
		if openErr != nil {
			return fmt.Errorf("existing compose file is not writable: %w", openErr)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("close compose file after write check: %w", err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("stat existing compose file: %w", err)
	}
	return writeFileAtomic(path, content, mode)
}

// writeEnvFileAtomic writes an env file atomically at exactly 0600, whatever
// mode an existing file had: env files hold secrets, so a looser mode left by
// older code is tightened rather than kept.
func writeEnvFileAtomic(envPath, content string) error {
	return writeFileAtomic(envPath, []byte(content), 0600)
}
