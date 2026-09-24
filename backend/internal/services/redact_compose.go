package services

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// IsSensitiveEnvKey classifies a key as secret-bearing by name. It is the one
// rule shared by every surface that hides env values: the stack env editor, the
// global-env settings page, and compose output (redactComposeOutput) — a key
// that masks in one place and leaks in the other is the whole bug class. It
// lived in the handlers package until agent-os-fvk3 needed it here.
func IsSensitiveEnvKey(key string) bool {
	upperKey := strings.ToUpper(key)

	if strings.HasPrefix(upperKey, "EXPORT ") {
		upperKey = strings.TrimSpace(upperKey[7:])
	}

	sensitiveSuffixes := []string{"_KEY", "_SECRET", "_PASSWORD", "_TOKEN"}
	for _, suffix := range sensitiveSuffixes {
		if strings.HasSuffix(upperKey, suffix) {
			return true
		}
	}

	return strings.Contains(upperKey, "_API_")
}

// composeSecretMinLen is the shortest env value redactComposeOutput replaces.
// A sensitive key holding "1" or "yes" would otherwise turn every such
// substring in the output into the placeholder and leave nothing readable.
const composeSecretMinLen = 4

// urlTokenRe finds scheme://... tokens inside free text, so each one can be
// handed to RedactURLUserinfo. It stops at whitespace and quotes, which is
// where compose and BuildKit end a URL they quote in a message.
var urlTokenRe = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.\-]*://[^\s"'<>]+`)

// redactComposeOutput is the redaction policy for docker compose output that
// reaches an API consumer or the action log (agent-os-fvk3). It is the compose
// sibling of redactToken (git_credentials.go) and RedactURLUserinfo.
//
// REDACTED, each replaced by "***":
//
//   - The userinfo of every scheme://user:pass@host URL in the text. A docker
//     image reference cannot carry a credential, so this is where registry and
//     git credentials actually surface: build contexts, registry URLs quoted in
//     errors. Done by RedactURLUserinfo, token by token.
//   - The value of every env var whose KEY is sensitive by IsSensitiveEnvKey,
//     taken from the env files compose reads (see composeSecrets). This is the
//     same rule the env-unlock gate applies, so compose output cannot hand a
//     locked session a value the env editor withholds from it. Build output
//     (compose up builds) can echo interpolated values, which is the path this
//     arm closes.
//
// DELIBERATELY NOT REDACTED:
//
//   - Absolute host paths. They are not secrets: stack.Directory is in every
//     stack API response and bind-mount sources are in the compose file the
//     API serves. They are also the main clue in a mount or "no such file"
//     error, so stripping them would cost diagnosis and protect nothing.
//   - Values of non-sensitive keys, which the env editor already shows to a
//     locked session, and values shorter than composeSecretMinLen.
//   - Key names, image names, service names and compose's own messages. What
//     an operator reads on failure is the full compose text with only secret
//     values and URL credentials starred.
//
// This redacts by value for env vars and by pattern for URLs. A secret compose
// learns some other way (a credential helper, a variable only in the process
// environment) is not known here and is not covered.
func redactComposeOutput(s string, secrets []string) string {
	if s == "" {
		return s
	}
	s = urlTokenRe.ReplaceAllStringFunc(s, RedactURLUserinfo)
	for _, v := range secrets {
		s = strings.ReplaceAll(s, v, userinfoPlaceholder)
	}
	return s
}

// redactComposeOutputFor applies redactComposeOutput with the stack's own
// secret values.
func (s *DockerService) redactComposeOutputFor(stack models.Stack, out string) string {
	if out == "" {
		return out
	}
	return redactComposeOutput(out, s.composeSecrets(stack))
}

// composeSecrets returns the sensitive env values compose can interpolate for
// this stack, longest first so a value that contains another is replaced whole.
//
// The files are the ones buildComposeArgs hands compose (global.env and the
// stack's EnvFile) plus the project directory's .env, which compose reads when
// no --env-file is given. Over-including .env costs nothing: it only adds
// values of sensitive keys.
//
// A file that cannot be read is skipped. compose runs as the same user against
// the same file, so it could not read it either and cannot have echoed it; the
// URL arm still applies.
func (s *DockerService) composeSecrets(stack models.Stack) []string {
	var paths []string
	if s.config != nil && s.config.DataDir != "" {
		paths = append(paths, filepath.Join(s.config.DataDir, "global.env"))
	}
	if stack.Directory != "" {
		paths = append(paths, filepath.Join(stack.Directory, ".env"))
	}
	if stack.EnvFile != "" {
		envPath := stack.EnvFile
		if !filepath.IsAbs(envPath) {
			envPath = filepath.Join(stack.Directory, envPath)
		}
		paths = append(paths, envPath)
	}

	seen := map[string]bool{}
	var secrets []string
	for _, p := range paths {
		//nolint:gosec // G304: the paths come from config.DataDir and the scanned stack record, the same files buildComposeArgs passes to compose
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, v := range sensitiveEnvValues(string(data)) {
			if !seen[v] {
				seen[v] = true
				secrets = append(secrets, v)
			}
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return secrets
}

// sensitiveEnvValues returns the values of sensitive keys in a dotenv file.
// Both the raw value and its unquoted, comment-stripped form are returned
// when they differ, because compose output may show either.
func sensitiveEnvValues(content string) []string {
	var values []string
	add := func(v string) {
		if len(v) >= composeSecretMinLen {
			values = append(values, v)
		}
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !IsSensitiveEnvKey(strings.TrimSpace(key)) {
			continue
		}
		raw := strings.TrimSpace(value)
		add(raw)
		clean := raw
		if len(clean) >= 2 && (clean[0] == '"' || clean[0] == '\'') && clean[len(clean)-1] == clean[0] {
			clean = clean[1 : len(clean)-1]
		} else if i := strings.Index(clean, " #"); i != -1 {
			clean = strings.TrimSpace(clean[:i])
		}
		if clean != raw {
			add(clean)
		}
	}
	return values
}
