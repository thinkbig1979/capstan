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
			return nil, nil, fmt.Errorf("more than %d compose files reached from %s", maxComposeFiles, root)
		}
		content, found, err := src.read(p)
		if err != nil {
			return nil, nil, err
		}
		if !found {
			continue
		}
		files = append(files, p)
		entries, err := parseComposeSecretEntries(content)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", p, err)
		}
		for _, e := range entries {
			q, ok, err := src.place(src.dir(p), e.path)
			if err != nil {
				return nil, nil, err
			}
			switch {
			case !ok && e.compose:
				return nil, nil, fmt.Errorf("%s: the file at line %d is outside the repository, so what it names is unknown", p, e.line)
			case !ok:
			case e.compose:
				queue = append(queue, q)
			default:
				named = append(named, q)
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
// that does not decode makes the whole file unreadable.
func parseComposeSecretEntries(content []byte) ([]composeEntry, error) {
	dec := yaml.NewDecoder(bytes.NewReader(content))
	var out []composeEntry
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, errComposeYAML
		}
		entries, err := composeDocumentEntries(&doc)
		if err != nil {
			return nil, err
		}
		out = append(out, entries...)
	}
}

// composeDocumentEntries reads one YAML document of a compose file.
func composeDocumentEntries(node *yaml.Node) ([]composeEntry, error) {
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
		return nil, errComposeYAML
	}
	var out []composeEntry
	add := func(n *yaml.Node, what string, compose bool) error {
		if n.Kind != yaml.ScalarNode || n.Value == "" || strings.Contains(n.Value, "$") || strings.Contains(n.Value, "://") {
			return fmt.Errorf("%s entry at line %d is not a fixed path", what, n.Line)
		}
		out = append(out, composeEntry{path: n.Value, line: n.Line, compose: compose})
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
						return err
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
		return fmt.Errorf("%s at line %d is neither a path nor a list", what, n.Line)
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
				return nil, errComposeYAML
			}
			if ext.File.Kind != 0 {
				if err := add(&ext.File, "extends file", true); err != nil {
					return nil, err
				}
			}
		default:
			return nil, fmt.Errorf("extends at line %d is neither a service nor a mapping", svc.Extends.Line)
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
				return nil, errComposeYAML
			}
			// It moves where the included file's own relative paths point.
			if inc.ProjectDirectory.Kind != 0 {
				return nil, fmt.Errorf("include at line %d sets project_directory", item.Line)
			}
			if err := paths(inc.Path, "include", true, nil); err != nil {
				return nil, err
			}
			if err := paths(inc.EnvFile, "include env_file", false, nil); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("include at line %d is not a list", doc.Include.Line)
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
			// Not a regular file is unreadable, and is never opened: opening a
			// FIFO for reading blocks until a writer appears. O_NONBLOCK and
			// the Stat below cover one swapped in between.
			info, err := os.Stat(p)
			if errors.Is(err, fs.ErrNotExist) {
				return nil, false, nil
			}
			if err != nil {
				return nil, false, err
			}
			if !info.Mode().IsRegular() {
				return nil, false, fmt.Errorf("compose file %s is not a regular file", p)
			}
			//nolint:gosec,forbidigo // a read-only open of a compose file inside the repository top (checked above); OpenFile only for O_NONBLOCK, which os.Open can't set
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
				return nil, false, fmt.Errorf("compose file %s is not a regular file of at most %d bytes", p, maxComposeBytes)
			}
			content, err := io.ReadAll(io.LimitReader(f, maxComposeBytes+1))
			return content, true, err
		},
		place: func(dir, entry string) (string, bool, error) {
			return diskPath(dir, entry), true, nil
		},
		dir: filepath.Dir,
	}
}

// diskPath is entry, written in a file in folder dir, as an absolute path:
// an absolute entry as it is, since joining it under dir names nothing.
func diskPath(dir, entry string) string {
	if filepath.IsAbs(entry) {
		return filepath.Clean(entry)
	}
	return filepath.Join(dir, filepath.FromSlash(entry))
}
