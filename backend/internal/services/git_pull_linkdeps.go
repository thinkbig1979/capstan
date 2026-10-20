package services

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// stackLinkDeps is every repository-relative path whose change alters what a
// stack reads through a symlink: each link crossed on the way to the stack's
// folder, compose file and env file, and the path each walk ends at
// (agent-os-juq9, agent-os-9ut3). git lists the link path when a link is
// retargeted and the target path when the target changes, never both, and the
// name match in relFilesChanged only knows the stack's own file names.
//
// A walk that ends outside the repository contributes no final path, only the
// links crossed inside it. That is deliberate: a pull cannot change a file
// outside the repository, so the target of such a link cannot have changed
// because of the pull, and the link itself, which is inside, still shows in
// the diff when it is retargeted. Treating the outside target as "unknown"
// would make every pull on that stack partial.
func stackLinkDeps(top string, stack models.Stack) ([]string, error) {
	var deps []string
	for _, name := range []string{"", stack.ComposeFile, stack.EnvFile} {
		if name == "" && len(deps) > 0 {
			continue
		}
		d, err := linkDeps(top, filepath.Join(stack.Directory, name))
		if err != nil {
			return nil, err
		}
		deps = append(deps, d...)
	}
	return deps, nil
}

// linkDeps walks the absolute path abs one component at a time, like the
// kernel does, and returns the repository-relative path of every symlink it
// crosses plus the path it ends at, when those lie inside top. A component
// that does not exist ends the walk there, so a dangling link yields the
// target git lists as deleted. A loop, or a component git cannot be asked
// about, is an error: the caller reports the stack, it is not skipped.
func linkDeps(top, abs string) ([]string, error) {
	var deps []string
	rest := splitPath(abs)
	resolved := string(filepath.Separator)
	hops := 0
	for len(rest) > 0 {
		c := rest[0]
		rest = rest[1:]
		switch c {
		case ".":
			continue
		case "..":
			resolved = filepath.Dir(resolved)
			continue
		}
		next := filepath.Join(resolved, c)
		fi, err := os.Lstat(next)
		if os.IsNotExist(err) {
			resolved = filepath.Join(append([]string{next}, rest...)...)
			break
		}
		if err != nil {
			return nil, fmt.Errorf("could not read %s: %w", boundedPath(next), boundedPathErr(err))
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			resolved = next
			continue
		}
		hops++
		if hops > maxLinkHops {
			return nil, fmt.Errorf("too many levels of symbolic links following %s", boundedPath(abs))
		}
		// resolved holds no links, so the link's own location is the placed
		// parent plus its name.
		if parent, ok, err := repoRel(top, resolved); err != nil {
			return nil, err
		} else if ok {
			deps = append(deps, path.Join(parent, c))
		}
		target, err := os.Readlink(next)
		if err != nil {
			return nil, fmt.Errorf("could not read the link %s: %w", boundedPath(next), boundedPathErr(err))
		}
		if filepath.IsAbs(target) {
			resolved = string(filepath.Separator)
		}
		rest = append(splitPath(target), rest...)
	}
	if rel, ok, err := repoRel(top, resolved); err != nil {
		return nil, err
	} else if ok {
		deps = append(deps, rel)
	}
	return deps, nil
}

func splitPath(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool { return r == filepath.Separator })
}

// anyChanged reports whether any of deps is among changedFiles.
func anyChanged(deps, changedFiles []string) bool {
	set := make(map[string]bool, len(deps))
	for _, d := range deps {
		set[d] = true
	}
	for _, f := range changedFiles {
		if set[f] {
			return true
		}
	}
	return false
}
