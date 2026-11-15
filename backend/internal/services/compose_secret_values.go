package services

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/pathutil"
)

// The stack's secret list (StackSecretValues) used to read only global.env,
// the project .env and the stack's EnvFile, so a secret written in the
// compose file itself, or in a service's env_file:, was hidden by the locked
// compose view and shown verbatim wherever the list masks by value: container
// logs and inspect (agent-os-rbmi review F1). These add both.

// composeSecretSources returns the values the stack's compose files write
// under a sensitive name, and the env files their services name (env_file:,
// also through include: and extends:, as composeSecretFiles reads them),
// keeping only those inside the stack's folder. A file it will not or cannot
// read is skipped with a warning that names its path, never a value: its
// values cannot be masked, which is the recorded residual.
func composeSecretSources(stack models.Stack) (values, envFiles []string) {
	if stack.Directory == "" || stack.ComposeFile == "" {
		return nil, nil
	}
	src := diskComposeSource(stack.Directory)
	root := stackRelativePath(stack.Directory, stack.ComposeFile)
	named, composeFiles, err := composeSecretFiles(src, root)
	if err != nil {
		// composeSecretFiles' errors name a path and a line, never a value.
		slog.Warn("Masking without the env_file values of a stack's compose file: its entries could not be read",
			"stack", stack.ProjectName, "error", err)
		named, composeFiles = nil, []string{root}
	}
	for _, p := range composeFiles {
		content, found, err := src.read(p)
		if err != nil {
			slog.Warn("Masking without the values of a compose file that could not be read", "stack", stack.ProjectName, "file", p, "error", err)
			continue
		}
		if found {
			values = append(values, composeYAMLSecretValues(content)...)
		}
	}
	for _, p := range named {
		inside, err := pathutil.IsContained(stack.Directory, p)
		if err != nil {
			slog.Warn("Masking without the values of an env_file whose place could not be checked: it is not read",
				"stack", stack.ProjectName, "file", p, "error", err)
			continue
		}
		if !inside {
			slog.Warn("Masking without the values of an env_file outside the stack's folder: it is not read",
				"stack", stack.ProjectName, "file", p)
			continue
		}
		envFiles = append(envFiles, p)
	}
	return values, envFiles
}

// readServiceEnvFile reads one env file a service names. A missing one holds
// no value to mask; any other failure is logged, path only, and skipped.
func readServiceEnvFile(stack models.Stack, p string) ([]byte, bool) {
	//nolint:gosec // G304: p is an env_file entry of the stack's own compose file, checked inside the stack's folder by composeSecretSources
	data, err := os.ReadFile(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, false
	case err != nil:
		slog.Warn("Masking without the values of an env_file that could not be read", "stack", stack.ProjectName, "file", p, "error", err)
		return nil, false
	}
	return data, true
}

// composeYAMLSecretValues returns what the locked compose view's YAML pass
// masks in content, found by the same rule (walkSensitiveYAML): every scalar
// under a sensitive key (a reference like ${X} is not a value), the value of
// a sensitive `KEY=value` item, and the argument of a sensitive long flag.
// Every YAML document counts, as compose merges them. A file that does not
// parse is read line by line instead, as the view masks it.
func composeYAMLSecretValues(content []byte) []string {
	var out []string
	aliased := map[*yaml.Node]bool{}
	var collect func(v *yaml.Node)
	collect = func(v *yaml.Node) {
		switch v.Kind {
		case yaml.ScalarNode:
			if v.Value != "" && !isSecretReference(v.Value) {
				out = append(out, v.Value)
			}
		case yaml.MappingNode:
			for i := 1; i < len(v.Content); i += 2 {
				collect(v.Content[i])
			}
		case yaml.SequenceNode:
			for _, c := range v.Content {
				collect(c)
			}
		case yaml.AliasNode:
			// Once per anchor: an alias inside its own anchor would recurse.
			if v.Alias != nil && !aliased[v.Alias] {
				aliased[v.Alias] = true
				collect(v.Alias)
			}
		}
	}
	onValue := func(v, _ *yaml.Node) { collect(v) }
	onItem := func(_ *yaml.Node, val string) {
		out = append(out, val, strings.TrimSpace(val))
	}

	dec := yaml.NewDecoder(bytes.NewReader(content))
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			return append(out, sensitiveEnvValues(string(content))...)
		}
		walkSensitiveYAML(&doc, onValue, onItem)
	}
}
