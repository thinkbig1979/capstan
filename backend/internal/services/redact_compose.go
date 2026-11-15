package services

import (
	"log/slog"
	"net/url"
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
// its key is called (agent-os-yt2f), and so is an opaque one
// (sftp:user:pw@host, user:pw@host) or JSON with a sensitive field
// ({"password":"x"}, agent-os-3mjw). The locked env views mask by this.
func IsSensitiveEnvVar(key, value string) bool {
	return IsSensitiveEnvKey(key) || HasURLUserinfo(value) || hasOpaqueCredential(value) || hasSensitiveJSONField(value)
}

// opaqueCredentialRe is a whole value of the shape [scheme:]user:password@host,
// optionally followed by ":..." or "/...", with no whitespace.
var opaqueCredentialRe = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9+.\-]*:)?[^\s:@/]+:[^\s@]+@([A-Za-z0-9][A-Za-z0-9.\-]*|\[[0-9A-Fa-f:.]+\])(?:[:/]\S*)?$`)

// digestAlgorithmRe is what follows the "@" of an image pinned by digest
// (nginx:1.25@sha256:...), which has the user:password@host shape.
var digestAlgorithmRe = regexp.MustCompile(`(?i)^sha[0-9]+$`)

// hasOpaqueCredential reports whether value, trimmed and unquoted, is an
// inline credential with no "//": "sftp:user:pw@host", "user:pw@host:/p".
// RedactURLUserinfo reads only the per-scheme forms restic accepts, because
// what it stars locks the backup repository field (agent-os-zzhs); this is
// the env views' wider rule, where a wrongly hidden value costs one unlock.
// A "://" value is HasURLUserinfo's: its path can hold "x:y@z".
func hasOpaqueCredential(value string) bool {
	v := strings.TrimSpace(value)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		v = v[1 : len(v)-1]
	}
	if strings.Contains(v, "://") {
		return false
	}
	m := opaqueCredentialRe.FindStringSubmatch(v)
	return m != nil && !digestAlgorithmRe.MatchString(m[1])
}

// jsonFieldNameRe is a JSON object field name and its colon, its quotes
// optionally backslash-escaped: JSON written inside a double-quoted .env
// value reads {\"password\":\"x\"} (review of run/mask2, D1).
var jsonFieldNameRe = regexp.MustCompile(`\\?"([^"\\]{1,128})\\?"\s*:`)

// hasSensitiveJSONField reports whether value holds a JSON field whose name
// IsSensitiveEnvKey matches: {"password":"x"}, a service account's
// "private_key". The same over-masking as the key rule: {"key": ...} counts.
func hasSensitiveJSONField(value string) bool {
	if !strings.Contains(value, `"`) {
		return false
	}
	for _, m := range jsonFieldNameRe.FindAllStringSubmatch(value, -1) {
		if IsSensitiveEnvKey(m[1]) {
			return true
		}
	}
	return false
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
	return RedactURLUserinfoInText(s) != s
}

// RedactURLUserinfoInText stars the userinfo of every scheme://... token in
// free text, and of every URL nested in one (redactURLToken), and the inline
// credential of every sftp: or s3: opaque token (agent-os-vq3i), leaving the
// rest byte-for-byte.
func RedactURLUserinfoInText(s string) string {
	s = urlTokenRe.ReplaceAllStringFunc(s, redactURLToken)
	return opaqueTokenRe.ReplaceAllStringFunc(s, func(tok string) string {
		redacted, _ := redactOpaqueCredential(tok)
		return redacted
	})
}

// opaqueTokenRe finds the sftp: and s3: opaque repository forms in free text,
// the two whose grammar redactOpaqueCredential reads a credential from
// ("sftp:user:pw@host:/path"). A word boundary keeps "xsftp:" out; a
// "sftp://" token is urlTokenRe's, and splitOpaque leaves it alone.
var opaqueTokenRe = regexp.MustCompile(`(?i)\b(?:sftp|s3):[^\s"'<>]+`)

// nestedSchemeRe finds each "scheme://" in a URL token, where a nested URL
// starts.
var nestedSchemeRe = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.\-]*://`)

// redactURLToken is RedactURLUserinfo for one scheme://... token, plus the
// userinfo of every URL nested in it (agent-os-caeh): url.Parse reads
// "https://proxy/fetch?url=https://user:pw@host/x" as one URL whose query
// holds the inner one, so its userinfo was never seen. The token is redacted
// whole, as before; then each nested piece, from one "scheme://" to the next,
// has its userinfo spliced out by position (redactUnparseableUserinfo, which
// never re-encodes: a url.String() round trip of a piece such as
// "http://***@" appends text); and last every percent-encoded nested URL
// (redactEncodedNestedUserinfo). Each step only ever puts the placeholder in,
// so the result hides at least what the whole-token pass hid. Every byte is
// read a fixed number of times, whatever the nesting.
func redactURLToken(tok string) string {
	out := RedactURLUserinfo(tok)
	if starts := nestedSchemeRe.FindAllStringIndex(out, -1); len(starts) > 1 {
		var b strings.Builder
		b.Grow(len(out))
		b.WriteString(out[:starts[1][0]])
		for i := 1; i < len(starts); i++ {
			end := len(out)
			if i+1 < len(starts) {
				end = starts[i+1][0]
			}
			b.WriteString(redactUnparseableUserinfo(out[starts[i][0]:end]))
		}
		out = b.String()
	}
	return redactEncodedNestedUserinfo(out)
}

// maxEncodedNestedURLs is how many percent-encoded nested URLs one token is
// read for; no real URL nests more than a couple.
const maxEncodedNestedURLs = 16

// encodedSchemeSep is "://" percent-encoded, as a URL nested in a query
// parameter carries it, compared ASCII case-insensitively.
const encodedSchemeSep = "%3a%2f%2f"

// redactEncodedNestedUserinfo stars the userinfo of every percent-encoded
// nested URL in tok ("?url=https%3A%2F%2Fuser%3Apw%40host%2Fx"), keeping
// every other byte. One level of encoding only: "%253A" and a "+" are not
// decoded. The nested URL runs to the next "&" or "#", or the token's end.
func redactEncodedNestedUserinfo(tok string) string {
	lower := asciiLower(tok)
	var b strings.Builder
	at := 0
	for i, n := 0, 0; i < len(tok); n++ {
		j := strings.Index(lower[i:], encodedSchemeSep)
		if j < 0 {
			break
		}
		start := i + j + len(encodedSchemeSep)
		if n == maxEncodedNestedURLs {
			// Each nested URL reads on to its token's end, so this bounds
			// the work (review of run/mask2, F1): fail closed on the rest.
			b.WriteString(tok[at:start])
			b.WriteString(userinfoPlaceholder)
			return b.String()
		}
		end := len(tok)
		if k := strings.IndexAny(tok[start:], "&#"); k >= 0 {
			end = start + k
		}
		if ui, ok := encodedUserinfoEnd(lower[start:end]); ok {
			b.WriteString(tok[at:start])
			b.WriteString(userinfoPlaceholder)
			at = start + ui
		}
		i = max(start, at)
	}
	if at == 0 {
		return tok
	}
	b.WriteString(tok[at:])
	return b.String()
}

// encodedAuthorityEnd is where an encoded authority ends in seg (lower case):
// its first encoded or plain "/", "?" or "#", or len(seg).
func encodedAuthorityEnd(seg string) int {
	end := len(seg)
	for _, stop := range []string{"%2f", "%3f", "%23", "/", "?", "#"} {
		if k := strings.Index(seg[:end], stop); k >= 0 {
			end = k
		}
	}
	return end
}

// encodedUserinfoEnd is the index of the "%40" that ends the userinfo of the
// encoded URL whose authority starts seg (lower case), by the rule
// redactUnparseableUserinfo applies to a plain one: the last "%40" inside the
// authority; failing that, when the text before the authority's first "/" is
// not a host on its own (a password holding an encoded "/"), the last "%40"
// in seg, provided a host follows it. ok is false when there is none, or the
// userinfo holds no non-empty component, as spliceUserinfo reads it.
func encodedUserinfoEnd(seg string) (int, bool) {
	end := encodedAuthorityEnd(seg)
	at := strings.LastIndex(seg[:end], "%40")
	if at < 0 {
		if isParseableAuthority(percentDecoded(seg[:end])) {
			return 0, false
		}
		at = strings.LastIndex(seg, "%40")
		if at < 0 {
			return 0, false
		}
		host := seg[at+3:]
		if !isParseableAuthority(percentDecoded(host[:encodedAuthorityEnd(host)])) {
			return 0, false
		}
	}
	user, password, _ := strings.Cut(seg[:at], "%3a")
	return at, user != "" || password != ""
}

// percentDecoded is s with its percent-escapes decoded, or "" when one is
// malformed: "" is never a host, so a malformed escape decides nothing.
func percentDecoded(s string) string {
	d, err := url.PathUnescape(s)
	if err != nil {
		return ""
	}
	return d
}

// asciiLower lower-cases ASCII letters only, so every index into the result
// is an index into s (strings.ToLower can change a rune's length).
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
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
	s, _ = maskNeedleUnion(RedactURLUserinfoInText(s), withURLRedactedForms(secrets), userinfoPlaceholder)
	return s
}

// withURLRedactedForms is needles plus, for each one RedactURLUserinfoInText
// changes, that changed form. The needles are looked for in text the URL pass
// has already starred, where a value holding a URL credential no longer
// matches as written: `DB_PASSWORD="pw sftp:u:x@h:/p"` would show "pw" once
// its sftp token is starred (found by the agent-os-caeh/vq3i superset corpus,
// which starred more tokens than before). needles is not changed.
func withURLRedactedForms(needles []string) []string {
	out := needles
	for i, v := range needles {
		// Every credential the URL pass stars sits before an "@" or "%40".
		if !strings.ContainsAny(v, "@%") {
			continue
		}
		if r := RedactURLUserinfoInText(v); r != v {
			if len(out) == len(needles) {
				out = append(make([]string, 0, len(needles)+len(needles)-i), needles...)
			}
			out = append(out, r)
		}
	}
	return out
}

// maskNeedleUnion replaces with mask each run of overlapping occurrences of
// needles in s, all found on s itself, so the order of needles does not
// matter. Replacing one needle at a time let a masked needle break the match
// of another that overlapped it, leaving part of that one shown
// (agent-os-buwq). It returns how many needles occur in s.
//
// O(len(s) + len(v)) per needle v that occurs, and one strings.Index per
// needle that does not; it allocates nothing for an s without a needle.
func maskNeedleUnion(s string, needles []string, mask string) (string, int) {
	runs, found := needleUnionRuns(s, needles)
	return writeMaskedRuns(s, runs, func(string) string { return mask }), found
}

// needleUnionRuns returns, left to right, each maximal run [start, end) of
// overlapping occurrences of needles in s, and how many needles occur.
// maskNeedleUnion masks them; a caller that picks the mask per run
// (scrubRclone) writes them with writeMaskedRuns.
func needleUnionRuns(s string, needles []string) ([][2]int, int) {
	var ends []int // ends[p] is the furthest end of an occurrence starting at p
	found := 0
	for _, v := range needles {
		first := -1
		if v != "" {
			first = strings.Index(s, v)
		}
		if first < 0 {
			continue
		}
		if ends == nil {
			ends = make([]int, len(s))
		}
		markOccurrences(s, v, first, ends)
		found++
	}
	if found == 0 {
		return nil, 0
	}
	var runs [][2]int
	for p := 0; p < len(s); p++ {
		if ends[p] == 0 {
			continue
		}
		end := ends[p]
		for q := p + 1; q < end; q++ {
			end = max(end, ends[q])
		}
		runs = append(runs, [2]int{p, end})
		p = end - 1
	}
	return runs, found
}

// writeMaskedRuns is s with each of runs replaced by maskFor(the run's text).
func writeMaskedRuns(s string, runs [][2]int, maskFor func(string) string) string {
	if len(runs) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	at := 0
	for _, r := range runs {
		b.WriteString(s[at:r[0]])
		b.WriteString(maskFor(s[r[0]:r[1]]))
		at = r[1]
	}
	b.WriteString(s[at:])
	return b.String()
}

// markOccurrences records in ends every occurrence of v in s from first (an
// occurrence) on, overlapping ones included, with a Knuth-Morris-Pratt scan:
// searching again from each hit plus one compares a self-overlapping needle
// whole at every byte, n*m (review of agent-os-buwq, F1).
func markOccurrences(s, v string, first int, ends []int) {
	// fail[i] is the length of the longest proper prefix of v[:i+1] that is
	// also its suffix.
	fail := make([]int, len(v))
	for i, k := 1, 0; i < len(v); i++ {
		for k > 0 && v[i] != v[k] {
			k = fail[k-1]
		}
		if v[i] == v[k] {
			k++
		}
		fail[i] = k
	}
	for i, k := first, 0; i < len(s); i++ {
		for k > 0 && s[i] != v[k] {
			k = fail[k-1]
		}
		if s[i] == v[k] {
			k++
		}
		if k == len(v) {
			a := i + 1 - len(v)
			ends[a] = max(ends[a], i+1)
			k = fail[k-1]
		}
	}
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

	// What the compose files write under a sensitive name, and the env files
	// their services name, as the locked compose view hides them
	// (agent-os-rbmi review F1). The env files join the per-file pass below.
	inline, serviceFiles := composeSecretSources(stack)
	for _, v := range inline {
		add(v)
	}
	for _, f := range serviceFiles {
		p := f.path
		if read[p] {
			continue
		}
		read[p] = true
		data, ok := readServiceEnvFile(stack, p)
		if !ok {
			continue
		}
		if f.secret {
			// A secrets: file: holds one raw value, no KEY=value lines; the
			// container reads it trimmed of its final newline (round-2
			// review F6).
			add(strings.TrimSpace(string(data)))
		}
		files = append(files, p)
		for _, v := range sensitiveEnvValues(string(data)) {
			add(v)
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
			if IsSensitiveEnvVar(k, v) {
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
			if IsSensitiveEnvVar(k, v) {
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
			if sep < 0 {
				continue
			}
			key := strings.TrimSpace(line[:sep])
			raw := strings.TrimSpace(line[sep+1:])
			clean := raw
			if len(clean) >= 2 && (clean[0] == '"' || clean[0] == '\'') && clean[len(clean)-1] == clean[0] {
				clean = clean[1 : len(clean)-1]
			} else if c := strings.Index(clean, " #"); c != -1 {
				clean = strings.TrimSpace(clean[:c])
			}
			// The value counts too (agent-os-vq3i): a URL credential is a
			// secret under any key, as IsSensitiveEnvVar has it.
			if !IsSensitiveEnvVar(key, raw) && !IsSensitiveEnvVar(key, clean) {
				continue
			}
			add(raw)
			if clean != raw {
				add(clean)
			}
			if raw != "" && (raw[0] == '"' || raw[0] == '\'') && envQuoteEnd(raw, 1, raw[0]) < 0 {
				// The value goes on past this line: up to its closing quote.
				text, end := followQuotedValue(raw, lines[i+1:], 1, raw[0], false)
				if end >= 0 {
					text = text[:end+1]
				}
				add(text)
				add(text[1:])
				add(strings.TrimSuffix(text[1:], raw[:1]))
			}
		}
		// Every statement compose reads after a closing quote (agent-os-czik):
		// `NOTES="a" DB_PASSWORD=x` is two variables. compose-go finds them
		// only in a file it parses, and gives the unescaped value; this reads
		// them in every file, as written and unescaped.
		for _, st := range envTailStatements(line) {
			v := line[st.start:st.end]
			if !IsSensitiveEnvVar(st.key, v) {
				continue
			}
			if st.open {
				// The value goes on past this line: up to its closing quote.
				text, end := followQuotedValue(line[st.start:], lines[i+1:], 0, st.quote, true)
				v = text
				if end >= 0 {
					v = text[:end]
				}
			}
			add(v)
			if u := envUnescaped(v); u != v {
				add(u)
			}
		}
	}
	return values
}

// followQuotedValue is head followed by "\n" and each of rest in turn, read
// only up to the line holding the quote q that closes a value opened just
// before from in head, and that quote's index in the text (-1 when it never
// closes, and the text is all of it). A backslash escapes the next byte, a
// newline included, inside double quotes, and inside single quotes too when
// escapeSingle: envQuoteEnd's and envClosingQuote's rules on the joined text.
// Joining every later line for each open value made a file of them
// quadratic (review of run/mask2, F2).
func followQuotedValue(head string, rest []string, from int, q byte, escapeSingle bool) (string, int) {
	var b strings.Builder
	b.WriteString(head)
	pos := from
	for k := 0; ; k++ {
		s := b.String()
		for ; pos < len(s); pos++ {
			switch c := s[pos]; {
			case c == '\\' && (q == '"' || escapeSingle):
				pos++
			case c == q:
				return s, pos
			}
		}
		if k == len(rest) {
			return s, -1
		}
		b.WriteByte('\n')
		b.WriteString(rest[k])
	}
}

// envUnescaped is v with each backslash escape replaced by the byte it
// escapes, as compose-go reads a quoted value's \" and \\ (an approximation:
// it also maps \n to "n"; the escaped form is added beside it, never instead).
func envUnescaped(v string) string {
	if !strings.Contains(v, `\`) {
		return v
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] == '\\' && i+1 < len(v) {
			i++
		}
		b.WriteByte(v[i])
	}
	return b.String()
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
