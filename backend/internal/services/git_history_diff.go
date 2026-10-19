package services

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/thinkbig1979/capstan/backend/internal/models"
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

// noLazyFetchEnv stops a partial clone fetching a missing object, which runs
// the repository's core.sshCommand or remote helper. Every child of the
// history view runs with it. git before 2.45 ignores it.
var noLazyFetchEnv = []string{"GIT_NO_LAZY_FETCH=1"}

// showEnv is the show's environment: exclude magic on, no lazy fetch.
var showEnv = append(append([]string{}, globPathspecEnv...), noLazyFetchEnv...)

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
func (s *GitService) historyDiff(dirPath string, cred gitCreds, oid string, parents []string) (*historyDiff, error) {
	top, bare, err := s.historyTop(dirPath, cred)
	if err != nil {
		return nil, err
	}
	out, _, err := s.gitRun(dirPath, cred, noLazyFetchEnv, append(append([]string{}, historyNameArgs...), oid)...)
	if err != nil {
		return nil, fmt.Errorf("failed to list changed files: %w", err)
	}
	groups, err := parseNameStatusZ(out)
	if err != nil {
		return nil, err
	}
	rule, err := s.historyEnvRule(dirPath, top, bare, cred, append([]string{oid}, parents...), false, "history diff")
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
	diff, _, err := s.gitRun(dirPath, cred, showEnv, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get diff: %w", err)
	}
	res.diff = strings.TrimSpace(diff)
	return res, nil
}

// historyTop is the repository's top level, symlinks resolved. A bare
// repository has no work tree, so its top is the folder itself and bare is
// true: nothing on disk describes its commits.
func (s *GitService) historyTop(dirPath string, cred gitCreds) (top string, bare bool, err error) {
	isBare, err := s.gitCommandWithCreds(dirPath, cred, "rev-parse", "--is-bare-repository")
	if err != nil {
		return "", false, fmt.Errorf("failed to tell whether the repository is bare: %w", err)
	}
	top = dirPath
	if isBare != "true" {
		if top, err = s.gitCommandWithCreds(dirPath, cred, "rev-parse", "--show-toplevel"); err != nil {
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

// treeEntry is one `ls-tree -r -l` record.
type treeEntry struct {
	mode string
	oid  string
	size int64 // -1 for an entry that is not a blob, or a size not listed
}

// composeNames are the file names compose looks for by default. A file with
// one of these names anywhere in a commit's tree is read as a compose file,
// as is every registered stack's ComposeFile name (D144): a stack removed
// since, or a compose file renamed since, still named its env files then.
var composeNames = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

// historyTree is one tree the rule reads (the commit, a parent, or a stage of
// the index).
type historyTree struct {
	name    string
	listing map[string]treeEntry
}

// historyRuleReader holds what one rule build reads, so a blob shared by the
// commit and its parents is read once.
type historyRuleReader struct {
	s       *GitService
	dirPath string
	cred    gitCreds
	top     string
	blobs   map[string]string
}

// catBlob is the only cat-file the history view runs: `blob`, so no textconv
// or filter, and never a lazy fetch.
func (s *GitService) catBlob(dirPath string, cred gitCreds, oid string) (string, error) {
	out, _, err := s.gitRun(dirPath, cred, noLazyFetchEnv, "-c", "core.fsmonitor=false", "cat-file", "blob", oid)
	return out, err
}

// blobSize is the size of blob oid, for a listing that does not carry it (the
// index): `cat-file -s`, never a lazy fetch.
func (h *historyRuleReader) blobSize(oid string) (int64, error) {
	if b, ok := h.blobs[oid]; ok {
		return int64(len(b)), nil
	}
	out, _, err := h.s.gitRun(h.dirPath, h.cred, noLazyFetchEnv, "-c", "core.fsmonitor=false", "cat-file", "-s", oid)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(out), 10, 64)
}

func (h *historyRuleReader) blob(oid string) (string, error) {
	if b, ok := h.blobs[oid]; ok {
		return b, nil
	}
	b, err := h.s.catBlob(h.dirPath, h.cred, oid)
	if err != nil {
		return "", err
	}
	h.blobs[oid] = b
	return b, nil
}

// resolve follows every symlink in p as the tree held it, component by
// component as the kernel does: a link to a folder inside the path and a
// link at its end, and a ".." after a link climbs from where the link led,
// not lexically (lnk/../secret with lnk -> deep/dir is deep/secret). Bounded
// like ELOOP. ok is false when the path leaves the repository or loops.
func (h *historyRuleReader) resolve(t historyTree, p string) (string, bool, error) {
	pending := strings.Split(p, "/")
	var done []string
	hops := 0
	for len(pending) > 0 {
		c := pending[0]
		pending = pending[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			if len(done) == 0 {
				return "", false, nil
			}
			done = done[:len(done)-1]
			continue
		}
		cur := strings.Join(append(done, c), "/")
		e, ok := t.listing[cur]
		if !ok || e.mode != "120000" {
			done = append(done, c)
			continue
		}
		if hops++; hops > maxLinkHops {
			return "", false, nil
		}
		dest, err := h.blob(e.oid)
		if err != nil {
			return "", false, fmt.Errorf("failed to read the link %s at %s: %w", cur, t.name, err)
		}
		if filepath.IsAbs(dest) {
			rel, ok, err := repoRel(h.top, dest)
			if err != nil {
				return "", false, err
			}
			if !ok {
				return "", false, nil
			}
			done = nil
			dest = rel
		}
		// A relative target continues from the link's folder (done).
		pending = append(strings.Split(dest, "/"), pending...)
	}
	return strings.Join(done, "/"), true, nil
}

// treeSource reads compose files from tree t. A git failure is recorded in
// gitErr, so the caller can tell it (refuse) from a compose file it can't
// read (hide all but compose files).
func (h *historyRuleReader) treeSource(t historyTree, gitErr *error) composeSource {
	return composeSource{
		read: func(p string) ([]byte, bool, error) {
			resolved, ok, err := h.resolve(t, p)
			if err != nil {
				*gitErr = err
				return nil, false, err
			}
			// A compose file whose link leaves the repository (or loops)
			// named something no tree here shows: unknown, not missing.
			if !ok {
				return nil, false, fmt.Errorf("compose file %s at %s is a link that leaves the repository or loops", p, t.name)
			}
			e, found := t.listing[resolved]
			if !found || e.mode == "160000" || e.mode == "120000" {
				return nil, false, nil
			}
			p = resolved
			if e.size < 0 {
				if e.size, err = h.blobSize(e.oid); err != nil {
					*gitErr = fmt.Errorf("failed to read the size of %s at %s: %w", p, t.name, err)
					return nil, false, *gitErr
				}
			}
			if e.size > maxComposeBytes {
				return nil, false, fmt.Errorf("compose file %s at %s is larger than %d bytes", p, t.name, maxComposeBytes)
			}
			b, err := h.blob(e.oid)
			if err != nil {
				*gitErr = fmt.Errorf("failed to read %s at %s: %w", p, t.name, err)
				return nil, false, *gitErr
			}
			return []byte(b), true, nil
		},
		place: func(dir, entry string) (string, bool, error) {
			return h.place(dir, entry)
		},
		dir: path.Dir,
	}
}

// place is an entry written in folder dir as a repository-relative path. An
// absolute entry is placed through the containment helper; ok is false only
// for a path outside the repository, which no commit of it holds.
func (h *historyRuleReader) place(dir, entry string) (string, bool, error) {
	if filepath.IsAbs(entry) {
		return repoRel(h.top, entry)
	}
	p, ok := repoJoin(dir, filepath.ToSlash(entry))
	return p, ok, nil
}

// historyEnvRule builds the rule from every stack inside top and from trees
// (the commit and its parents). With index it also reads the index as trees,
// and today's disk copy of every compose-named file the index tracks,
// registered or not (agent-os-7l76): the commit dialog commits the index. A
// git or database failure is an error: the caller refuses rather than show a
// diff it could not check. A nil database is one too. view names the caller
// in log lines.
func (s *GitService) historyEnvRule(dirPath, top string, bare bool, cred gitCreds, trees []string, index bool, view string) (historyEnvRule, error) {
	r := historyEnvRule{named: map[string]bool{}, targets: map[string]bool{}, compose: map[string]bool{}}
	if s.db == nil {
		return r, fmt.Errorf("cannot decide which files are env files: no database")
	}
	stacks, unplaced, err := s.repoStacks(top)
	if err != nil {
		return r, err
	}
	h := &historyRuleReader{s: s, dirPath: dirPath, cred: cred, top: top, blobs: map[string]string{}}
	unknown := func(msg string, args ...any) {
		slog.Warn("Hiding every file but the compose files in the "+view+": "+msg, args...)
		r.composeUnreadable = true
	}

	tl := make([]historyTree, 0, len(trees))
	for _, t := range trees {
		l, err := s.lsTree(dirPath, cred, t)
		if err != nil {
			return r, err
		}
		tl = append(tl, historyTree{name: t, listing: l})
	}
	var il []historyTree
	if index {
		if il, err = s.lsIndex(top, cred); err != nil {
			return r, err
		}
		tl = append(tl, il...)
	}

	names := map[string]bool{}
	for _, n := range composeNames {
		names[n] = true
	}
	// A stack that can't be placed may hold this repository's env files;
	// hiding all but compose files keeps this diff closed without failing
	// every history diff of every repository.
	for _, u := range unplaced {
		unknown("a stack folder could not be placed", "stack", u.stack.ProjectName, "error", u.err)
	}
	// diskNamed reads composeRel from today's disk.
	diskNamed := func(composeRel string) error {
		named, files, err := composeSecretFiles(diskComposeSource(top), filepath.Join(top, filepath.FromSlash(composeRel)))
		if err != nil {
			unknown("a compose file's env_file entries are unknown", "path", composeRel, "error", err)
			return nil
		}
		for i, abs := range append(named, files...) {
			rel, ok, err := repoRel(top, abs)
			if err != nil {
				return err
			}
			switch {
			case !ok:
			case i < len(named):
				r.named[rel] = true
			default:
				r.compose[rel] = true
			}
		}
		return nil
	}
	var composeRels []string
	for _, rs := range stacks {
		st, stackRel := rs.Stack, rs.rel
		if st.EnvFile != "" {
			p, ok, err := h.place(stackRel, st.EnvFile)
			if err != nil {
				return r, err
			}
			if ok {
				r.named[p] = true
			}
		}
		if st.ComposeFile == "" {
			continue
		}
		names[path.Base(filepath.ToSlash(st.ComposeFile))] = true
		composeRel, ok, err := h.place(stackRel, st.ComposeFile)
		if err != nil {
			return r, err
		}
		if !ok {
			continue
		}
		composeRels = append(composeRels, composeRel)
		r.compose[composeRel] = true
		if bare {
			continue
		}
		if err := diskNamed(composeRel); err != nil {
			return r, err
		}
	}
	// A tracked compose-named file edited on disk names what the next
	// commit's compose file may name, whether or not a stack is registered
	// for it. il[0] is the index's stage 0.
	if index && !bare {
		registered := map[string]bool{}
		for _, p := range composeRels {
			registered[p] = true
		}
		var rels []string
		for p, e := range il[0].listing {
			if names[path.Base(p)] && e.mode != "160000" && !registered[p] {
				rels = append(rels, p)
			}
		}
		sort.Strings(rels)
		for _, p := range rels {
			if err := diskNamed(p); err != nil {
				return r, err
			}
		}
	}

	// Every compose file as each tree held it: the registered ones and any
	// file with a compose name, wherever it sits.
	for _, t := range tl {
		roots := append([]string(nil), composeRels...)
		for p := range t.listing {
			if names[path.Base(p)] {
				roots = append(roots, p)
			}
		}
		sort.Strings(roots)
		for _, root := range roots {
			var gitErr error
			named, files, err := composeSecretFiles(h.treeSource(t, &gitErr), root)
			if gitErr != nil {
				return r, gitErr
			}
			if err != nil {
				unknown("a past compose file's env_file entries are unknown", "path", root, "commit", t.name, "error", err)
				continue
			}
			for _, f := range files {
				r.compose[f] = true
			}
			for _, n := range named {
				r.named[n] = true
			}
		}
	}

	// Symlinks as they were: an env-named or named path that went through a
	// link in a tree (at its end or in a folder above it) hides where it led,
	// whether or not the links exist today.
	for _, t := range tl {
		var cands []string
		for p := range r.named {
			cands = append(cands, p)
		}
		for p, e := range t.listing {
			if e.mode == "120000" && anyEnvComponent(p) {
				cands = append(cands, p)
			}
		}
		for _, p := range cands {
			dest, ok, err := h.resolve(t, p)
			if err != nil {
				return r, err
			}
			if ok && dest != p {
				r.targets[dest] = true
			}
		}
	}

	if !bare {
		candidates := make([]string, 0, len(r.named))
		for p := range r.named {
			candidates = append(candidates, filepath.Join(top, filepath.FromSlash(p)))
		}
		today, err := s.envLinkTargets(top, top, cred, candidates)
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

// lsIndex lists the index as lsTree lists a tree, run from top so every path
// is repository-relative. `ls-files -s` carries no sizes (ls-files --format's
// %(objectsize) needs a newer git: OBSERVED, git 2.39.5 answers "bad ls-files
// format"), so each entry's size is unknown and treeSource asks for it. A
// conflicted path has an entry per stage, so there is one listing per stage
// present: the stage-0 entries plus that stage's. The first is stage 0.
func (s *GitService) lsIndex(top string, cred gitCreds) ([]historyTree, error) {
	out, _, err := s.gitRun(top, cred, noLazyFetchEnv, "-c", "core.fsmonitor=false", "ls-files", "-s", "-z")
	if err != nil {
		return nil, fmt.Errorf("failed to list the index: %w", err)
	}
	stages := [4]map[string]treeEntry{{}}
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		meta, p, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			return nil, fmt.Errorf("unexpected git ls-files record %q", rec)
		}
		stage, err := strconv.Atoi(f[2])
		if err != nil {
			return nil, fmt.Errorf("unexpected git ls-files stage in %q: %w", rec, err)
		}
		if stage < 0 || stage > 3 {
			return nil, fmt.Errorf("unexpected git ls-files stage in %q", rec)
		}
		if stages[stage] == nil {
			stages[stage] = map[string]treeEntry{}
		}
		stages[stage][p] = treeEntry{mode: f[0], oid: f[1], size: -1}
	}
	tl := []historyTree{{name: "the index", listing: stages[0]}}
	for i := 1; i < len(stages); i++ {
		if stages[i] == nil {
			continue
		}
		l := maps.Clone(stages[0])
		maps.Copy(l, stages[i])
		tl = append(tl, historyTree{name: fmt.Sprintf("the index (stage %d)", i), listing: l})
	}
	return tl, nil
}

// repoStack is a registered stack whose folder lies inside the repository,
// with that folder as a repository-relative slash path.
type repoStack struct {
	models.Stack
	rel string
}

// unplacedStack is a stack whose folder could not be checked against the
// repository: it may lie inside it.
type unplacedStack struct {
	stack models.Stack
	err   error
}

// repoStacks is every registered stack whose folder lies inside top, plus the
// stacks it could not place. It never keys on the folder a request names: a
// file of one folder can be another stack's env file (agent-os-0zjs).
func (s *GitService) repoStacks(top string) ([]repoStack, []unplacedStack, error) {
	stacks, err := s.db.ListStacks()
	if err != nil {
		return nil, nil, fmt.Errorf("could not list the stacks to decide which files are env files: %w", err)
	}
	var in []repoStack
	var unplaced []unplacedStack
	for _, st := range stacks {
		rel, ok, err := repoRel(top, st.Directory)
		switch {
		case err != nil:
			unplaced = append(unplaced, unplacedStack{stack: st, err: err})
		case ok:
			in = append(in, repoStack{Stack: st, rel: rel})
		}
	}
	return in, unplaced, nil
}

// maxLinkHops bounds a chain of links, as the kernel's ELOOP does.
const maxLinkHops = 40

// lsTree lists every entry of tree-ish t by repository-relative path,
// recursively, with blob sizes. --full-tree: run from a stack folder below
// the top, ls-tree would list only that folder, relative to it (agent-os-odjs).
func (s *GitService) lsTree(dirPath string, cred gitCreds, t string) (map[string]treeEntry, error) {
	out, _, err := s.gitRun(dirPath, cred, noLazyFetchEnv, "-c", "core.fsmonitor=false", "ls-tree", "--full-tree", "-r", "-l", "-z", t)
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
		if !ok || len(f) != 4 {
			return nil, fmt.Errorf("unexpected git ls-tree record %q", rec)
		}
		size, err := strconv.ParseInt(f[3], 10, 64)
		if err != nil {
			size = -1 // "-": not a blob
		}
		l[p] = treeEntry{mode: f[0], oid: f[2], size: size}
	}
	return l, nil
}

// repoRel is abs, a path on disk, as a repository-relative slash path;
// false when it lies outside top. Symlinks are resolved in its deepest
// existing ancestor, so a file deleted from disk under a symlinked stacks root
// still maps (agent-os-odjs). A path it cannot check is an error, never
// "outside": the caller would then show a file it meant to hide.
func repoRel(top, abs string) (string, bool, error) {
	ok, err := pathutil.IsContained(top, abs)
	if err != nil {
		return "", false, fmt.Errorf("could not tell whether %s is inside the repository: %w", boundedPath(abs), boundedPathErr(err))
	}
	if !ok {
		return "", false, nil
	}
	resolved, err := pathutil.ResolveExisting(abs)
	if err != nil {
		return "", false, fmt.Errorf("could not resolve %s: %w", boundedPath(abs), boundedPathErr(err))
	}
	rel, err := filepath.Rel(top, resolved)
	if err != nil {
		return "", false, fmt.Errorf("could not place %s in the repository: %w", boundedPath(resolved), boundedPathErr(err))
	}
	// Contained, yet not local: top was passed with a symlink in it. It names
	// no file git could list under this name.
	if !filepath.IsLocal(rel) {
		return "", false, nil
	}
	return filepath.ToSlash(rel), true, nil
}

// maxErrPathBytes bounds a path quoted into an error. repoRel's abs can be a
// link target read from a committed blob, which the repository's author
// chose, and the error reaches a log line on every request (agent-os-odjs).
const maxErrPathBytes = 200

// boundedPath is p quoted for an error, cut to maxErrPathBytes.
func boundedPath(p string) string {
	if len(p) <= maxErrPathBytes {
		return strconv.Quote(p)
	}
	return fmt.Sprintf("%q... (%d bytes)", p[:maxErrPathBytes], len(p))
}

// boundedPathErr is err with the path of an *fs.PathError in it bounded as
// boundedPath does: the OS error (lstat <path>: file name too long) repeats
// the path whole. The cause stays in the chain.
func boundedPathErr(err error) error {
	var pe *fs.PathError
	if !errors.As(err, &pe) {
		return err
	}
	return fmt.Errorf("%s %s: %w", pe.Op, boundedPath(pe.Path), pe.Err)
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
