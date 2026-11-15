package services

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"

	"github.com/thinkbig1979/capstan/backend/internal/pathutil"
)

// Which files a compose file names as holding secrets, for the commit dialog
// (envRule) and the history view (historyEnvRule): every service's env_file:,
// every top-level secrets: file: (D146), and the same in each file reached
// through include: (whose own env_file: counts too) and a service's
// extends: file: (D145). The two views read compose files from different
// places, today's disk and a past commit's tree, so where a file comes from
// is a composeSource.

// composeSource reads compose files from one place and resolves the paths
// written in them.
type composeSource struct {
	// read returns a compose file's content; found is false when it does not
	// exist.
	read func(p string) (content []byte, found bool, err error)
	// place turns an entry written in a file in folder dir into a path of
	// this source. ok is false when it names nothing this source can hold
	// (outside the repository).
	place func(dir, entry string) (p string, ok bool, err error)
	dir   func(p string) string
}

const (
	// maxComposeFiles bounds one walk of include: and extends: links.
	maxComposeFiles = 64
	// maxComposeBytes bounds one compose file read.
	maxComposeBytes = 1 << 20
)

// composeSecretFiles returns the secret files named from the compose file at
// root and every compose file it reached (root included, when it exists). An
// entry it cannot resolve to a fixed path (interpolated, remote, another
// shape), an include: with project_directory, or an include:/extends: file
// outside the source is an error, so the caller fails toward hiding. A
// missing file names nothing.
func composeSecretFiles(src composeSource, root string) (named, files []string, err error) {
	found, files, err := walkComposeSecretFiles(src, root, func(err error) error { return err })
	if err != nil {
		return nil, nil, err
	}
	for _, f := range found {
		named = append(named, f.path)
	}
	return named, files, nil
}

// namedSecretFile is a file a compose file names as holding secrets: an
// env_file (KEY=value lines), or a top-level secrets: file: (one raw value).
type namedSecretFile struct {
	path   string
	secret bool
}

// composeSecretFilesEach is composeSecretFiles for a caller that masks with
// what it can read (the stack's secret list, agent-os-rbmi round-2 review
// F1): an entry it cannot resolve, or a compose file it cannot read, is
// returned in skipped and only that entry is left out. Every other entry,
// and every compose file reached, still counts. Past maxComposeFiles the
// walk stops with what it found.
func composeSecretFilesEach(src composeSource, root string) (named []namedSecretFile, files []string, skipped []error) {
	named, files, err := walkComposeSecretFiles(src, root, func(err error) error {
		skipped = append(skipped, err)
		return nil
	})
	if err != nil {
		// bad above never ends the walk; kept so a change to it can't drop
		// an error unseen.
		skipped = append(skipped, err)
	}
	return named, files, skipped
}

// walkComposeSecretFiles is the walk both callers share. Every problem goes
// to bad: when it returns an error the walk ends with it; when it returns
// nil only the entry, or the file, at fault is left out.
func walkComposeSecretFiles(src composeSource, root string, bad func(error) error) (named []namedSecretFile, files []string, err error) {
	queue := []string{root}
	seen := map[string]bool{}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if seen[p] {
			continue
		}
		seen[p] = true
		if len(seen) > maxComposeFiles {
			if err := bad(fmt.Errorf("more than %d compose files reached from %s", maxComposeFiles, root)); err != nil {
				return nil, nil, err
			}
			break
		}
		content, found, err := src.read(p)
		if err != nil {
			if err := bad(err); err != nil {
				return nil, nil, err
			}
			continue
		}
		if !found {
			continue
		}
		files = append(files, p)
		entries, err := parseComposeSecretEntries(content, func(err error) error { return bad(fmt.Errorf("%s: %w", p, err)) })
		if err != nil {
			return nil, nil, err
		}
		for _, e := range entries {
			q, ok, err := src.place(src.dir(p), e.path)
			if err != nil {
				if err := bad(err); err != nil {
					return nil, nil, err
				}
				continue
			}
			switch {
			case !ok && e.compose:
				if err := bad(fmt.Errorf("%s: the file at line %d is outside the repository, so what it names is unknown", p, e.line)); err != nil {
					return nil, nil, err
				}
			case !ok:
			case e.compose:
				queue = append(queue, q)
			default:
				named = append(named, namedSecretFile{path: q, secret: e.secret})
			}
		}
	}
	return named, files, nil
}

// composeEntry is one path written in a compose file: a secret file, or
// (compose) another compose file to read.
type composeEntry struct {
	path    string
	line    int
	compose bool
	// secret is a top-level secrets: file:, which holds one raw value.
	secret bool
}

// errComposeYAML replaces every YAML decoding error. yaml.v3 quotes the
// value it could not decode, and an include: or extends: can point at an env
// file, so its text would put a secret in an error and the log line that
// carries it. Errors here name a path and a line, never a value.
var errComposeYAML = errors.New("not YAML this rule can read")

// parseComposeSecretEntries reads the paths one compose file writes, relative
// to its folder or absolute: env_file: as a string, a list of strings or a
// list of {path: ...}; secrets: <name>: file:; include: as a list of strings
// or of {path: string|list, env_file: string|list}; extends: {file: ...}.
//
// Every YAML document of the file counts: compose merges them. A document
// that does not decode makes the whole file unreadable. Every entry it cannot
// read goes to bad (see walkComposeSecretFiles): an error from bad ends the
// parse with it, nil leaves out only that entry, or the rest of the file
// when it is the YAML itself that does not decode.
func parseComposeSecretEntries(content []byte, bad func(error) error) ([]composeEntry, error) {
	dec := yaml.NewDecoder(bytes.NewReader(content))
	var out []composeEntry
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, bad(errComposeYAML)
		}
		entries, err := composeDocumentEntries(&doc, bad)
		if err != nil {
			return nil, err
		}
		out = append(out, entries...)
	}
}

// composeDocumentEntries reads one YAML document of a compose file, each
// entry it cannot read sent to bad as parseComposeSecretEntries says.
func composeDocumentEntries(node *yaml.Node, bad func(error) error) ([]composeEntry, error) {
	var doc struct {
		Include  yaml.Node `yaml:"include"`
		Services map[string]struct {
			EnvFile yaml.Node `yaml:"env_file"`
			Extends yaml.Node `yaml:"extends"`
		} `yaml:"services"`
		Secrets map[string]struct {
			File yaml.Node `yaml:"file"`
		} `yaml:"secrets"`
	}
	if err := node.Decode(&doc); err != nil {
		return nil, bad(errComposeYAML)
	}
	var out []composeEntry
	add := func(n *yaml.Node, what string, compose bool) error {
		if n.Kind != yaml.ScalarNode || n.Value == "" || strings.Contains(n.Value, "$") || strings.Contains(n.Value, "://") {
			return bad(fmt.Errorf("%s entry at line %d is not a fixed path", what, n.Line))
		}
		out = append(out, composeEntry{path: n.Value, line: n.Line, compose: compose, secret: what == "secrets file"})
		return nil
	}
	// paths reads a string or a list of strings; long, when set, decodes a
	// list item that is a mapping into the node to read.
	paths := func(n yaml.Node, what string, compose bool, long func(item *yaml.Node) (*yaml.Node, error)) error {
		switch n.Kind {
		case 0:
			return nil
		case yaml.ScalarNode:
			return add(&n, what, compose)
		case yaml.SequenceNode:
			for _, item := range n.Content {
				if item.Kind == yaml.MappingNode && long != nil {
					var err error
					if item, err = long(item); err != nil {
						if err := bad(err); err != nil {
							return err
						}
						continue
					}
					if item == nil {
						continue
					}
				}
				if err := add(item, what, compose); err != nil {
					return err
				}
			}
			return nil
		}
		return bad(fmt.Errorf("%s at line %d is neither a path nor a list", what, n.Line))
	}

	for _, svc := range doc.Services {
		err := paths(svc.EnvFile, "env_file", false, func(item *yaml.Node) (*yaml.Node, error) {
			var l struct {
				Path yaml.Node `yaml:"path"`
			}
			if err := item.Decode(&l); err != nil {
				return nil, errComposeYAML
			}
			return &l.Path, nil
		})
		if err != nil {
			return nil, err
		}
		switch svc.Extends.Kind {
		case 0, yaml.ScalarNode:
			// A service of the same file: no other file to read.
		case yaml.MappingNode:
			var ext struct {
				File yaml.Node `yaml:"file"`
			}
			if err := svc.Extends.Decode(&ext); err != nil {
				if err := bad(errComposeYAML); err != nil {
					return nil, err
				}
			} else if ext.File.Kind != 0 {
				if err := add(&ext.File, "extends file", true); err != nil {
					return nil, err
				}
			}
		default:
			if err := bad(fmt.Errorf("extends at line %d is neither a service nor a mapping", svc.Extends.Line)); err != nil {
				return nil, err
			}
		}
	}
	for _, sec := range doc.Secrets {
		if sec.File.Kind != 0 {
			if err := add(&sec.File, "secrets file", false); err != nil {
				return nil, err
			}
		}
	}
	switch doc.Include.Kind {
	case 0:
	case yaml.SequenceNode:
		for _, item := range doc.Include.Content {
			if item.Kind != yaml.MappingNode {
				if err := add(item, "include", true); err != nil {
					return nil, err
				}
				continue
			}
			var inc struct {
				Path             yaml.Node `yaml:"path"`
				EnvFile          yaml.Node `yaml:"env_file"`
				ProjectDirectory yaml.Node `yaml:"project_directory"`
			}
			if err := item.Decode(&inc); err != nil {
				if err := bad(errComposeYAML); err != nil {
					return nil, err
				}
				continue
			}
			// It moves where the included file's own relative paths point.
			if inc.ProjectDirectory.Kind != 0 {
				if err := bad(fmt.Errorf("include at line %d sets project_directory", item.Line)); err != nil {
					return nil, err
				}
				continue
			}
			if err := paths(inc.Path, "include", true, nil); err != nil {
				return nil, err
			}
			if err := paths(inc.EnvFile, "include env_file", false, nil); err != nil {
				return nil, err
			}
		}
	default:
		if err := bad(fmt.Errorf("include at line %d is not a list", doc.Include.Line)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// diskComposeSource reads compose files on today's disk, inside top only: an
// include: or extends: file elsewhere is an error, so a compose file can't
// make Capstan read any file the server can. Paths are absolute.
func diskComposeSource(top string) composeSource {
	return composeSource{
		read: func(p string) ([]byte, bool, error) {
			ok, err := pathutil.IsContained(top, p)
			if err != nil {
				return nil, false, err
			}
			if !ok {
				return nil, false, fmt.Errorf("compose file %s is outside the repository", p)
			}
			return readRegularFileCapped(p, "compose file")
		},
		place: func(dir, entry string) (string, bool, error) {
			return diskPath(dir, entry), true, nil
		},
		dir: filepath.Dir,
	}
}

// readRegularFileCapped reads the file at p, which the caller has already
// checked is inside the place it may read, only when it is a regular file of
// at most maxComposeBytes. found is false when it does not exist; what names
// the file in an error, which carries its path and never its content.
//
// Not a regular file is unreadable, and is never opened: opening a FIFO for
// reading blocks until a writer appears. O_NONBLOCK and the second Stat cover
// one swapped in between.
func readRegularFileCapped(p, what string) (content []byte, found bool, err error) {
	info, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s %s is not a regular file", what, p)
	}
	//nolint:gosec,forbidigo // a read-only open of a file the caller checked is inside its place; OpenFile only for O_NONBLOCK, which os.Open can't set
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	info, err = f.Stat()
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxComposeBytes {
		return nil, false, fmt.Errorf("%s %s is not a regular file of at most %d bytes", what, p, maxComposeBytes)
	}
	content, err = io.ReadAll(io.LimitReader(f, maxComposeBytes+1))
	return content, true, err
}

// diskPath is entry, written in a file in folder dir, as an absolute path:
// an absolute entry as it is, since joining it under dir names nothing.
func diskPath(dir, entry string) string {
	if filepath.IsAbs(entry) {
		return filepath.Clean(entry)
	}
	return filepath.Join(dir, filepath.FromSlash(entry))
}
