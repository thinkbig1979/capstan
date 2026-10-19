// Package pathutil provides symlink-aware filesystem containment checks used to
// confine user-influenced paths (stack directories, compose/env files, restore
// targets) to their configured roots. A purely lexical prefix check is not
// sufficient: a symlink inside the root that points outside it would let a
// lexically-inside path escape on read/write. IsContained resolves symlinks
// before comparing.
package pathutil

import (
	"os"
	"path/filepath"
	"strings"
)

// IsContained reports whether target is root itself or lies strictly inside
// root, after resolving symlinks on both sides.
//
// target need not exist yet (e.g. a restore destination or a new env file): the
// deepest existing ancestor is resolved via filepath.EvalSymlinks and the
// not-yet-created tail is appended, so a symlinked ancestor pointing outside
// root is still caught.
//
// The comparison appends a path separator to the resolved root so a sibling
// directory sharing a name prefix (e.g. /stacks-evil vs /stacks) is rejected.
func IsContained(root, target string) (bool, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false, err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false, err
	}

	realRoot, err := ResolveExisting(absRoot)
	if err != nil {
		return false, err
	}
	realTarget, err := ResolveExisting(absTarget)
	if err != nil {
		return false, err
	}

	if realTarget == realRoot {
		return true, nil
	}
	prefix := realRoot
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(realTarget, prefix), nil
}

// ResolveExisting resolves symlinks for the longest existing prefix of p and
// re-appends any trailing components that do not exist yet. It returns a cleaned
// absolute-ish path with all symlinks in the existing portion dereferenced, so a
// deleted file reached through a symlinked folder still maps to where it was.
// Any error but "does not exist" (ENOTDIR, EACCES) is returned.
func ResolveExisting(p string) (string, error) {
	p = filepath.Clean(p)

	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved, nil
	} else if !os.IsNotExist(err) {
		// A real error (permission, etc.) — surface it rather than guessing.
		return "", err
	}

	parent := filepath.Dir(p)
	if parent == p {
		// Reached the filesystem root and it still does not resolve; return as-is.
		return p, nil
	}

	resolvedParent, err := ResolveExisting(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(p)), nil
}
