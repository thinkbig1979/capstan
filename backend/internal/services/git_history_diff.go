package services

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thinkbig1979/capstan/backend/internal/pathutil"
)

// The history diff (GET /git/diff/:hash) never shows an env file's contents
// (agent-os-2omy, D140), as the commit dialog never does (D136). Which files
// are env files is decided per commit (D143): the names the commit touched,
// on every side, against a rule built from every stack in the repository and
// from the repository as it was at that commit and at each parent. The
// hidden files are excluded in git's own argv, so their contents never reach
// this process, an error message or a log line.
//
// Accepted residuals, by decision:
//   - Content renamed or copied away from an env name (.env -> notes.txt,
//     cp .env backup.txt) shows in later commits, as the editor shows it
//     (D143). The commit that does the rename hides both sides; a rename the
//     name pass can't pair (an edited rename past diff.renameLimit) reads as
//     a delete and an add, so its new side shows.
//   - A stack outside the repository whose compose env_file: points into it
//     is not consulted; only stacks inside the repository are. An env-named
//     file is hidden whoever names it.

// historyNameArgs list every path a commit touched: --root for a root
// commit, -m for every parent of a merge (a combined diff lists nothing),
// -M to pair a rename's two sides, -z so no name comes back C-quoted. It
// runs no textconv or diff program.
var historyNameArgs = []string{
	"-c", "core.fsmonitor=false", "-c", "core.quotePath=false", "--no-optional-locks",
	"diff-tree", "-z", "-r", "--root", "-m", "-M", "--name-status", "--no-commit-id", "--no-ext-diff", "--no-textconv",
}

// historyShowArgs is `git show` of one commit's patch with what repository
// config could change pinned, as changeDiffArgs pins `git diff`. On top of
// that, --no-show-signature and log.showSignature=false stop gpg.program
// running (either alone does). --submodule=short is load-bearing here: in
// "diff" mode a submodule's files would print under paths the name pass
// never saw. The caller appends "--" and the pathspecs.
var historyShowArgs = []string{
	"-c", "core.fsmonitor=false", "-c", "core.quotePath=false", "--no-optional-locks",
	"-c", "diff.submodule=short", "-c", "log.showSignature=false",
	"show", "--format=", "--no-show-signature", "--no-ext-diff", "--no-textconv", "--no-color", "--submodule=short",
	"--src-prefix=a/", "--dst-prefix=b/", "--no-relative",
}

// excludePathspecMagic marks a hidden path: excluded, matched literally,
// relative to the repository top whatever the working directory. It needs
// GIT_LITERAL_PATHSPECS=0 (globPathspecEnv), which would otherwise turn the
// magic off and read it as a path that matches nothing beside a "whole
// repository" positive.
const excludePathspecMagic = ":(exclude,literal,top)"

// maxHiddenPathspecBytes bounds the excludes in argv. Past it the show is not
// run and every file is reported hidden, rather than failing on E2BIG.
const maxHiddenPathspecBytes = 256 << 10

// historyDiff is the patch of commit oid with every env file left out.
type historyDiff struct {
	diff   string
	files  []string
	hidden []string
}

// historyDiff runs from dirPath; every pathspec carries top magic, so the
// folder the request named can't narrow or widen anything. parents are oid's
// parents (none for a root commit).
func (s *GitService) historyDiff(dirPath, user, token, oid string, parents []string) (*historyDiff, error) {
	top, bare, err := s.historyTop(dirPath, user, token)
	if err != nil {
		return nil, err
	}
	out, _, err := s.gitRun(dirPath, user, token, nil, append(append([]string{}, historyNameArgs...), oid)...)
	if err != nil {
		return nil, fmt.Errorf("failed to list changed files: %w", err)
	}
	groups, err := parseNameStatusZ(out)
	if err != nil {
		return nil, err
	}
	rule, err := s.historyEnvRule(dirPath, top, bare, user, token, append([]string{oid}, parents...))
	if err != nil {
		return nil, err
	}

	all, hidden := map[string]bool{}, map[string]bool{}
	for _, g := range groups {
		hide := false
		for _, p := range g {
			all[p] = true
			hide = hide || rule.hides(p)
		}
		if hide {
			for _, p := range g {
				hidden[p] = true
			}
		}
	}
	res := &historyDiff{files: sortedKeys(all), hidden: sortedKeys(hidden)}

	pathspecs := []string{":(top)"}
	size := 0
	for _, p := range res.hidden {
		pathspecs = append(pathspecs, excludePathspecMagic+p)
		size += len(excludePathspecMagic) + len(p)
	}
	switch {
	case len(res.hidden) > 0 && len(res.hidden) == len(res.files):
		return res, nil
	case size > maxHiddenPathspecBytes:
		slog.Warn("Hiding the whole history diff: too many env files to exclude", "path", dirPath, "commit", oid, "hidden", len(res.hidden))
		res.hidden = res.files
		return res, nil
	}
	args := append(append(append([]string{}, historyShowArgs...), oid, "--"), pathspecs...)
	diff, _, err := s.gitRun(dirPath, user, token, globPathspecEnv, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get diff: %w", err)
	}
	res.diff = strings.TrimSpace(diff)
	return res, nil
}

// historyTop is the repository's top level, symlinks resolved. A bare
// repository has no work tree, so its top is the folder itself and bare is
// true: nothing on disk describes its commits.
func (s *GitService) historyTop(dirPath, user, token string) (top string, bare bool, err error) {
	isBare, err := s.gitCommandWithCreds(dirPath, user, token, "rev-parse", "--is-bare-repository")
	if err != nil {
		return "", false, fmt.Errorf("failed to tell whether the repository is bare: %w", err)
	}
	top = dirPath
	if isBare != "true" {
		if top, err = s.gitCommandWithCreds(dirPath, user, token, "rev-parse", "--show-toplevel"); err != nil {
			return "", false, fmt.Errorf("failed to find the repository top level: %w", err)
		}
	}
	resolved, err := filepath.EvalSymlinks(top)
	if err != nil {
		return "", false, fmt.Errorf("failed to resolve the repository top level: %w", err)
	}
	return resolved, isBare == "true", nil
}

// parseNameStatusZ reads `diff-tree -z --name-status`: a status record, then
// one path, or two for a rename or copy (status R or C), which come back as
// one group so both sides are decided together.
func parseNameStatusZ(out string) ([][]string, error) {
	recs := strings.Split(out, "\x00")
	var groups [][]string
	for i := 0; i < len(recs); i++ {
		status := recs[i]
		if status == "" {
			continue
		}
		n := 1
		if status[0] == 'R' || status[0] == 'C' {
			n = 2
		}
		if i+n >= len(recs) {
			return nil, fmt.Errorf("git diff-tree record %q has no path", status)
		}
		g := recs[i+1 : i+1+n]
		for _, p := range g {
			if p == "" {
				return nil, fmt.Errorf("git diff-tree record %q has an empty path", status)
			}
		}
		groups = append(groups, append([]string(nil), g...))
		i += n
	}
	return groups, nil
}

// historyEnvRule is envRule for a past commit, over repository-relative
// slash paths.
type historyEnvRule struct {
	// named: each stack's EnvFile and the env_file: entries of each stack's
	// compose file today and in every tree read.
	named map[string]bool
	// targets: where an env-named (or named) symlink points, in every tree
	// read and on today's disk.
	targets map[string]bool
	// composeUnreadable: some compose file's env_file: entries are unknown,
	// so everything but the stacks' compose files is hidden.
	composeUnreadable bool
	compose           map[string]bool
}

// hides: any component of the path is env-named (a file inside an env/ or
// .env/ folder too), or the path or a folder above it is named or a link
// target.
func (r historyEnvRule) hides(rel string) bool {
	parts := strings.Split(rel, "/")
	for i, c := range parts {
		prefix := strings.Join(parts[:i+1], "/")
		if isEnvName(c) || r.named[prefix] || r.targets[prefix] {
			return true
		}
	}
	return r.composeUnreadable && !r.compose[rel]
}

// treeEntry is one `ls-tree -r` record.
type treeEntry struct {
	mode string
	oid  string
}

// historyEnvRule builds the rule from every stack inside top and from trees
// (the commit and its parents). Any failure is an error: the caller refuses
// rather than show a diff it could not check. A nil database is one too.
func (s *GitService) historyEnvRule(dirPath, top string, bare bool, user, token string, trees []string) (historyEnvRule, error) {
	r := historyEnvRule{named: map[string]bool{}, targets: map[string]bool{}, compose: map[string]bool{}}
	if s.db == nil {
		return r, fmt.Errorf("cannot decide which files are env files: no database")
	}
	stacks, err := s.db.ListStacks()
	if err != nil {
		return r, fmt.Errorf("could not list the stacks to decide which files are env files: %w", err)
	}

	listings := make([]map[string]treeEntry, 0, len(trees))
	for _, t := range trees {
		l, err := s.lsTree(dirPath, user, token, t)
		if err != nil {
			return r, err
		}
		listings = append(listings, l)
	}

	addEntries := func(composeRel string, entries []string) {
		for _, e := range entries {
			if rel, ok := repoJoin(path.Dir(composeRel), e); ok {
				r.named[rel] = true
			}
		}
	}
	for _, st := range stacks {
		ok, err := pathutil.IsContained(top, st.Directory)
		if err != nil {
			return r, fmt.Errorf("could not tell whether stack %s is inside the repository: %w", st.ProjectName, err)
		}
		if !ok {
			continue
		}
		stackRel, ok, err := repoRel(top, st.Directory)
		if err != nil {
			return r, err
		}
		if !ok {
			continue
		}
		if st.EnvFile != "" {
			if rel, ok := repoJoin(stackRel, st.EnvFile); ok {
				r.named[rel] = true
			}
		}
		if st.ComposeFile == "" {
			continue
		}
		composeRel, ok := repoJoin(stackRel, st.ComposeFile)
		if !ok {
			continue
		}
		r.compose[composeRel] = true
		if !bare {
			entries, err := composeEnvFiles(filepath.Join(st.Directory, filepath.FromSlash(st.ComposeFile)))
			if err != nil {
				slog.Warn("Hiding every file but the compose files in the history diff: a compose file's env_file entries are unknown",
					"path", st.Directory, "error", err)
				r.composeUnreadable = true
			}
			addEntries(composeRel, entries)
		}
		for i, l := range listings {
			e, ok := l[composeRel]
			if !ok || e.mode == "160000" || e.mode == "040000" {
				continue
			}
			content, _, err := s.gitRun(dirPath, user, token, nil, "-c", "core.fsmonitor=false", "cat-file", "blob", e.oid)
			if err != nil {
				return r, fmt.Errorf("failed to read %s at %s: %w", composeRel, trees[i], err)
			}
			entries, err := parseComposeEnvFiles([]byte(content))
			if err != nil {
				slog.Warn("Hiding every file but the compose files in the history diff: a past compose file's env_file entries are unknown",
					"path", composeRel, "commit", trees[i], "error", err)
				r.composeUnreadable = true
			}
			addEntries(composeRel, entries)
		}
	}

	// Symlinks as they were: an env-named (or named) link in a tree hides
	// what it points at in that commit, whether or not the link exists today.
	for i, l := range listings {
		for p, e := range l {
			if e.mode != "120000" || (!r.named[p] && !anyEnvComponent(p)) {
				continue
			}
			if err := s.followTreeLink(dirPath, user, token, trees[i], top, l, p, r.targets); err != nil {
				return r, err
			}
		}
	}

	if !bare {
		candidates := make([]string, 0, len(r.named))
		for p := range r.named {
			candidates = append(candidates, filepath.Join(top, filepath.FromSlash(p)))
		}
		today, err := s.envLinkTargets(top, top, user, token, candidates)
		if err != nil {
			return r, err
		}
		for abs := range today {
			rel, ok, err := repoRel(top, abs)
			if err != nil {
				return r, err
			}
			if ok {
				r.targets[rel] = true
			}
		}
	}
	return r, nil
}

// maxLinkHops bounds a chain of links, as the kernel's ELOOP does.
const maxLinkHops = 40

// followTreeLink adds every path the link at p leads to in listing l to
// targets, resolved lexically inside the tree. A target outside the
// repository is no file of its commits and is dropped.
func (s *GitService) followTreeLink(dirPath, user, token, tree, top string, l map[string]treeEntry, p string, targets map[string]bool) error {
	for hop := 0; hop < maxLinkHops; hop++ {
		e, ok := l[p]
		if !ok || e.mode != "120000" {
			return nil
		}
		dest, _, err := s.gitRun(dirPath, user, token, nil, "-c", "core.fsmonitor=false", "cat-file", "blob", e.oid)
		if err != nil {
			return fmt.Errorf("failed to read the link %s at %s: %w", p, tree, err)
		}
		var next string
		if filepath.IsAbs(dest) {
			if next, ok, err = repoRel(top, dest); err != nil {
				return err
			}
		} else {
			next, ok = repoJoin(path.Dir(p), dest)
		}
		if !ok || targets[next] {
			return nil
		}
		targets[next] = true
		p = next
	}
	return nil
}

// lsTree lists every entry of tree-ish t by path, recursively.
func (s *GitService) lsTree(dirPath, user, token, t string) (map[string]treeEntry, error) {
	out, _, err := s.gitRun(dirPath, user, token, nil, "-c", "core.fsmonitor=false", "-c", "core.quotePath=false", "ls-tree", "-r", "-z", t)
	if err != nil {
		return nil, fmt.Errorf("failed to list the files of %s: %w", t, err)
	}
	l := map[string]treeEntry{}
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		meta, p, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			return nil, fmt.Errorf("unexpected git ls-tree record %q", rec)
		}
		l[p] = treeEntry{mode: f[0], oid: f[2]}
	}
	return l, nil
}

// repoRel is abs, a path on disk, as a repository-relative slash path;
// false when it lies outside top. Symlinks are resolved where they exist. A
// path it cannot check is an error, never "outside": the caller would then
// show a file it meant to hide.
func repoRel(top, abs string) (string, bool, error) {
	ok, err := pathutil.IsContained(top, abs)
	if err != nil {
		return "", false, fmt.Errorf("could not tell whether %s is inside the repository: %w", abs, err)
	}
	if !ok {
		return "", false, nil
	}
	resolved, err := filepath.EvalSymlinks(abs)
	switch {
	case err == nil:
		abs = resolved
	case !errors.Is(err, fs.ErrNotExist):
		return "", false, fmt.Errorf("could not resolve %s: %w", abs, err)
	}
	rel, err := filepath.Rel(top, abs)
	if err != nil {
		return "", false, fmt.Errorf("could not place %s in the repository: %w", abs, err)
	}
	// Contained, yet not local: a path that does not exist reached through a
	// symlinked folder. It names no file git could list under this name.
	if !filepath.IsLocal(rel) {
		return "", false, nil
	}
	return filepath.ToSlash(rel), true, nil
}

// repoJoin joins a repository-relative folder and a relative path as paths
// inside a git tree, which has no symlinks to resolve on disk; false when the
// result is absolute or leaves the tree.
func repoJoin(dir, p string) (string, bool) {
	j := path.Join(dir, filepath.ToSlash(p))
	if path.IsAbs(p) || j == "." || !filepath.IsLocal(j) {
		return "", false
	}
	return j, true
}

func anyEnvComponent(rel string) bool {
	for _, c := range strings.Split(rel, "/") {
		if isEnvName(c) {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
