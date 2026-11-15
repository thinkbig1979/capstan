package services

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"

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
// under a sensitive name, and the files their services name (env_file:, also
// through include: and extends:, and the top-level secrets: file:, as
// composeSecretFiles reads them), keeping only those inside the stack's
// folder. An entry it cannot resolve, or a file it will not read, is left out
// alone, the rest still counts (agent-os-rbmi round-2 review F1), and is
// warned about once (warnSecretSourceOnce) by path, never by value: its
// values cannot be masked, which is the recorded residual.
//
// refs are the variables a sensitive value names only by reference
// (composeYAMLSecrets), for the caller to resolve.
func composeSecretSources(stack models.Stack) (values, refs []string, files []namedSecretFile) {
	if stack.Directory == "" || stack.ComposeFile == "" {
		return nil, nil, nil
	}
	src := diskComposeSource(stack.Directory)
	root := stackRelativePath(stack.Directory, stack.ComposeFile)
	named, composeFiles, skipped := composeSecretFilesEach(src, root)
	for _, err := range skipped {
		// composeSecretFiles' errors name a path and a line, never a value.
		warnSecretSourceOnce(stack, err.Error(), "Masking without what one entry of a stack's compose file names: it could not be read",
			"error", err)
	}
	for _, p := range composeFiles {
		content, found, err := src.read(p)
		if err != nil {
			warnSecretSourceOnce(stack, p, "Masking without the values of a compose file that could not be read", "file", p, "error", err)
			continue
		}
		if found {
			v, r := composeYAMLSecrets(content)
			values, refs = append(values, v...), append(refs, r...)
		}
	}
	for _, f := range named {
		inside, err := pathutil.IsContained(stack.Directory, f.path)
		if err != nil {
			warnSecretSourceOnce(stack, f.path, "Masking without the values of an env_file whose place could not be checked: it is not read",
				"file", f.path, "error", err)
			continue
		}
		if !inside {
			warnSecretSourceOnce(stack, f.path, "Masking without the values of an env_file outside the stack's folder: it is not read",
				"file", f.path)
			continue
		}
		files = append(files, f)
	}
	return values, refs, files
}

// referenceName is the variable a reference (isSecretReference) names.
func referenceName(ref string) string {
	n := strings.TrimPrefix(strings.TrimSpace(ref), "$")
	n = strings.TrimSuffix(strings.TrimPrefix(n, "{"), "}")
	if i := strings.IndexAny(n, ":?"); i >= 0 {
		n = n[:i]
	}
	return n
}

// readServiceEnvFile reads one file a service names, through the compose
// files' own guard (readRegularFileCapped): a FIFO, a device or a file over
// the cap is never read (agent-os-rbmi round-2 review F3). A missing one
// holds no value to mask; any other failure is warned about once, path only,
// and skipped.
func readServiceEnvFile(stack models.Stack, p string) ([]byte, bool) {
	data, found, err := readRegularFileCapped(p, "env_file")
	if err != nil {
		warnSecretSourceOnce(stack, p, "Masking without the values of an env_file that could not be read", "file", p, "error", err)
		return nil, false
	}
	return data, found
}

// warnedSecretSources is every (stack, source) warnSecretSourceOnce has
// warned about. The list is rebuilt every 5 s per open log stream, so a
// warning per rebuild was 12 a minute per stream (agent-os-rbmi round-2
// review F4). It holds one key per distinct skipped path of a stack.
var warnedSecretSources sync.Map

// warnSecretSourceOnce logs msg at Warn the first time it is called for this
// stack and source, and at Debug after that.
func warnSecretSourceOnce(stack models.Stack, source, msg string, args ...any) {
	args = append([]any{"stack", stack.ProjectName}, args...)
	if _, seen := warnedSecretSources.LoadOrStore(stack.ProjectName+"\x00"+source, true); seen {
		slog.Debug(msg, args...)
		return
	}
	slog.Warn(msg, args...)
}

// composeYAMLSecretValues returns what the locked compose view's YAML pass
// masks in content, found by the same rule (walkSensitiveYAML): every scalar
// under a sensitive key (a reference like ${X} is not a value), the value of
// a sensitive `KEY=value` item, and the argument of a sensitive long flag.
// Every YAML document counts, as compose merges them. A file that does not
// parse is read line by line instead, as the view masks it.
func composeYAMLSecretValues(content []byte) []string {
	values, _ := composeYAMLSecrets(content)
	return values
}

// composeYAMLSecrets is composeYAMLSecretValues plus the name of every
// variable a sensitive value is only a reference to (${PW}, $PW, ${PW?msg}):
// a build arg leaves no trace in the container, so `DB_PASSWORD: ${PW}` with
// PW under an ordinary name was masked nowhere (agent-os-oh7q).
func composeYAMLSecrets(content []byte) (out, refs []string) {
	aliased := map[*yaml.Node]bool{}
	var collect func(v *yaml.Node)
	collect = func(v *yaml.Node) {
		switch v.Kind {
		case yaml.ScalarNode:
			if isSecretReference(v.Value) {
				refs = append(refs, referenceName(v.Value))
			} else if v.Value != "" {
				// Both forms, as onItem adds them: an app prints the value it
				// got, which compose may hand it trimmed (round-2 review F5).
				out = append(out, v.Value, strings.TrimSpace(v.Value))
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
	onValue := func(v, k *yaml.Node) {
		// The argument of a flag only the wider flag rule finds is masked in
		// place but not listed (review round 2 F2).
		if name, ok := strings.CutPrefix(k.Value, "--"); ok && k.Kind == yaml.ScalarNode && !IsSensitiveEnvKey(name) {
			return
		}
		collect(v)
	}
	onItem := func(_ *yaml.Node, val string) {
		if isSecretReference(val) {
			refs = append(refs, referenceName(val))
			return
		}
		out = append(out, val, strings.TrimSpace(val))
	}

	dec := yaml.NewDecoder(bytes.NewReader(content))
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return out, refs
		}
		if err != nil {
			return append(out, sensitiveEnvValues(string(content))...), refs
		}
		walkSensitiveYAML(&doc, onValue, onItem)
	}
}
