package services

import (
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/dotenv"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// IsSensitiveEnvKey classifies a key as secret-bearing by name. It is the one
// rule shared by every surface that hides env values: the stack env editor, the
// global-env settings page, and compose output (redactComposeOutput) — a key
// that masks in one place and leaks in the other is the whole bug class. It
// lived in the handlers package until agent-os-fvk3 needed it here.
//
// The rule is by word (agent-os-t61y): the key is split at every character
// that is not a letter or digit, trailing digits are dropped from each word,
// and the key is sensitive when a word is one of sensitiveEnvWords, or ends
// in one of sensitiveEnvWordEndings, or the key contains "_API_". So
// DB_PASSWORD2, PASSWORD_DB, a bare PASSWORD and DBPASSWORD all match, and an
// "export " prefix is just one more word. KEY counts only as a whole word,
// so MONKEY and KEYCLOAK_URL stay visible. PWD and PASS are left out: PWD is
// the shell's working directory and PASS starts ordinary words.
//
// It over-masks on purpose: PASSWORD_MIN_LENGTH, TOKEN_EXPIRY and
// AWS_ACCESS_KEY_ID are hidden too. A wrongly hidden value costs one unlock;
// a wrongly shown one is a leaked secret. The frontend's copy is
// frontend/src/components/stack/env-editor/sensitiveKey.ts.
func IsSensitiveEnvKey(key string) bool {
	upperKey := strings.ToUpper(key)
	words := strings.FieldsFunc(upperKey, func(r rune) bool {
		return (r < 'A' || r > 'Z') && (r < '0' || r > '9')
	})
	for _, w := range words {
		w = strings.TrimRight(w, "0123456789")
		if sensitiveEnvWords[w] {
			return true
		}
		for _, end := range sensitiveEnvWordEndings {
			if strings.HasSuffix(w, end) {
				return true
			}
		}
	}
	return strings.Contains(upperKey, "_API_")
}

// IsSensitiveEnvVar is IsSensitiveEnvKey widened by the value: a value that
// carries a URL credential (postgres://user:pw@host/db) is a secret whatever
// its key is called (agent-os-yt2f). The locked env views mask by this.
func IsSensitiveEnvVar(key, value string) bool {
	return IsSensitiveEnvKey(key) || HasURLUserinfo(value)
}

// HasURLUserinfo reports whether s, or any scheme://... token in it, carries a
// credential, by asking RedactURLUserinfo: it returns a URL without one
// byte-for-byte, so any change means it found userinfo to star. The whole
// value is asked too, because the token scan needs "://" and RedactURLUserinfo
// also reads opaque forms such as sftp:user:pw@host:/path (review S3).
func HasURLUserinfo(s string) bool {
	if t := strings.TrimSpace(s); RedactURLUserinfo(t) != t {
		return true
	}
	for _, tok := range urlTokenRe.FindAllString(s, -1) {
		if RedactURLUserinfo(tok) != tok {
			return true
		}
	}
	return false
}

// RedactURLUserinfoInText stars the userinfo of every scheme://... token in
// free text, leaving the rest byte-for-byte.
func RedactURLUserinfoInText(s string) string {
	return urlTokenRe.ReplaceAllStringFunc(s, RedactURLUserinfo)
}

// sensitiveEnvWords are the words that make a key sensitive on their own.
var sensitiveEnvWords = map[string]bool{
	"PASSWORD": true, "PASSWD": true, "SECRET": true, "TOKEN": true,
	"KEY": true, "APIKEY": true, "PRIVATEKEY": true,
}

// sensitiveEnvWordEndings also match at the end of a longer word
// (CLIENTSECRET, AUTHTOKEN). KEY is not one: MONKEY and HOTKEY end in it.
var sensitiveEnvWordEndings = []string{"PASSWORD", "PASSWD", "SECRET", "TOKEN"}

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
	s = RedactURLUserinfoInText(s)
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
// The files are resolveComposeEnv's superset: the ones buildComposeArgs hands
// compose (global.env and the stack's EnvFile) plus the project directory's
// .env, which compose reads when no --env-file is given. Over-including .env
// costs nothing: it only adds values of sensitive keys.
//
// Each value is learned twice (agent-os-tfef). First as compose resolves it,
// by resolveComposeEnv (compose-go's dotenv.GetEnvFromFile, the function
// compose-go's cli.WithDotEnv calls, seeded with the compose child's
// environment). Quotes, escapes, "KEY: value" and ${VAR} references therefore
// come out as compose sees them, with compose's precedence: a value set in the
// same file wins over global.env's. Second by the older sensitiveEnvValues, on
// every readable file, always: a file compose-go rejects then still masks what
// that parser finds, and the raw text of a line is masked too. Never fewer
// values than before this change.
//
// A file that cannot be read is skipped. compose runs as the same user against
// the same file, so it could not read it either and cannot have echoed it; the
// URL arm still applies.
func (s *DockerService) composeSecrets(stack models.Stack) []string {
	var globalPath string
	if s.config != nil && s.config.DataDir != "" {
		globalPath = filepath.Join(s.config.DataDir, "global.env")
	}
	return StackSecretValues(stack, globalPath)
}

// ComposeSecretValues is composeSecrets for the handlers: the stack's secret
// values, masked wherever a locked view shows them verbatim (agent-os-yvlh).
// A nil service has no config to find global.env with, so it answers
// ErrDockerUnavailable rather than a set that may be missing values.
func (s *DockerService) ComposeSecretValues(stack models.Stack) ([]string, error) {
	if s == nil {
		return nil, ErrDockerUnavailable
	}
	return s.composeSecrets(stack), nil
}

// StackSecretValues is composeSecrets for callers without a DockerService:
// the locked views mask these values wherever they appear verbatim
// (agent-os-w6wx). globalPath "" reads no global.env.
func StackSecretValues(stack models.Stack, globalPath string) []string {
	seen := map[string]bool{}
	var secrets []string
	add := func(v string) {
		for _, s := range append([]string{v}, secretLines(v)...) {
			if len(s) >= composeSecretMinLen && !seen[s] {
				seen[s] = true
				secrets = append(secrets, s)
			}
		}
	}

	dotEnv, envFile := stackEnvPaths(stack)
	chains := supersetEnvFileChains(globalPath, dotEnv, envFile)
	read := map[string]bool{}
	var files []string
	for _, chain := range chains {
		for _, p := range chain {
			if read[p] {
				continue
			}
			read[p] = true
			//nolint:gosec // G304: the paths come from config.DataDir and the scanned stack record, the same files buildComposeArgs passes to compose
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			files = append(files, p)
			for _, v := range sensitiveEnvValues(string(data)) {
				add(v)
			}
		}
	}

	// compose-go stops a chain at its first file that does not parse, so a
	// broken global.env would hide the stack's own files from it. Each file
	// is therefore also resolved alone (agent-os-0c9k); its ${VAR} references
	// to another file may then resolve differently, which only adds values.
	processEnv := processComposeEnv()
	for _, p := range files {
		env, err := dotenv.GetEnvFromFile(processEnv, []string{p})
		if err != nil {
			// The partial map is still used. err is not logged: it can quote
			// the offending line, which can be a secret.
			slog.Debug("compose-go could not parse an env file on its own; redacting with what it read",
				"stack", stack.ProjectName)
		}
		for k, v := range env {
			if IsSensitiveEnvKey(k) {
				add(v)
			}
		}
	}

	envs, err := resolveComposeEnv(stack, globalPath, composeEnvSuperset)
	if err != nil {
		// The partial maps are still used and the sensitiveEnvValues pass
		// above covers the rest. err is not logged: compose-go quotes the
		// offending line's text, which can be a secret.
		slog.Debug("compose-go could not parse a stack's env files; redacting with the fallback parser's values too",
			"stack", stack.ProjectName)
	}
	for _, env := range envs {
		for k, v := range env {
			if IsSensitiveEnvKey(k) {
				add(v)
			}
		}
	}

	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return secrets
}

// sensitiveEnvValues returns the values of sensitive keys in a dotenv file.
// Both the raw value and its unquoted, comment-stripped form are returned
// when they differ, because compose output may show either.
//
// Every reading is ADDED, never one instead of another (agent-os-0c9k):
//   - each line on its own, as the older parser read it, with the key ending
//     at the first "=";
//   - the same line with the key ending at the first "=" or ":", as compose-go
//     reads it (locateKeyName), so `KEY: value` is a variable too;
//   - for a quoted value that does not close on its line, everything up to its
//     closing quote on a later line, or to the end of the file when it never
//     closes, since all of that may be the value. The lines after it are
//     still read one by one as well.
//
// A multi-line value is returned whole and line by line (secretLines).
func sensitiveEnvValues(content string) []string {
	var values []string
	add := func(v string) {
		for _, s := range append([]string{v}, secretLines(v)...) {
			if len(s) >= composeSecretMinLen {
				values = append(values, s)
			}
		}
	}
	lines := strings.Split(content, "\n")
	for i := range lines {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		seps := []int{strings.IndexByte(line, '=')}
		if sep := strings.IndexAny(line, "=:"); sep != seps[0] {
			seps = append(seps, sep)
		}
		for _, sep := range seps {
			if sep < 0 || !IsSensitiveEnvKey(strings.TrimSpace(line[:sep])) {
				continue
			}
			raw := strings.TrimSpace(line[sep+1:])
			add(raw)
			clean := raw
			if len(clean) >= 2 && (clean[0] == '"' || clean[0] == '\'') && clean[len(clean)-1] == clean[0] {
				clean = clean[1 : len(clean)-1]
			} else if c := strings.Index(clean, " #"); c != -1 {
				clean = strings.TrimSpace(clean[:c])
			}
			if clean != raw {
				add(clean)
			}
			if raw != "" && (raw[0] == '"' || raw[0] == '\'') && envQuoteEnd(raw, 1, raw[0]) < 0 {
				// The value goes on past this line: up to its closing quote.
				text := raw + "\n" + strings.Join(lines[i+1:], "\n")
				if end := envQuoteEnd(text, 1, raw[0]); end >= 0 {
					text = text[:end+1]
				}
				add(text)
				add(text[1:])
				add(strings.TrimSuffix(text[1:], raw[:1]))
			}
		}
	}
	return values
}

// envQuoteEnd is the index in s of the quote q that closes a value opened
// just before from, or -1: a backslash escapes the next byte inside double
// quotes, as compose-go reads them.
func envQuoteEnd(s string, from int, q byte) int {
	for i := from; i < len(s); i++ {
		switch {
		case q == '"' && s[i] == '\\':
			i++
		case s[i] == q:
			return i
		}
	}
	return -1
}

// secretLines is each line of a multi-line secret, trimmed, that is long
// enough to mask on its own (composeSecretMinLen); nil for a one-line value.
// Output often shows such a value one line at a time: the compose stream is
// redacted line by line, and a compose file or diff can re-indent it
// (agent-os-0c9k).
func secretLines(v string) []string {
	if !strings.Contains(v, "\n") {
		return nil
	}
	var out []string
	for _, l := range strings.Split(v, "\n") {
		if l = strings.TrimSpace(l); len(l) >= composeSecretMinLen {
			out = append(out, l)
		}
	}
	return out
}
