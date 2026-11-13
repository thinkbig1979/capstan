package services

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// secretMask replaces a masked value. It is the placeholder the env views and
// compose output use.
const secretMask = userinfoPlaceholder

// MaskSecrets is the masking for a file shown to a session that has not
// unlocked env values (agent-os-w6wx): the compose editor and every diff. The
// env views hide the same values; a file that shows them would undo that.
//
// It keeps the text's line count and every line it does not mask
// byte-for-byte, so a diff of two masked texts lines up with the diff of the
// originals. It returns the text and how many values it masked.
//
// What it masks, as the union of four passes:
//
//   - YAML structure (a .yml/.yaml path): the value of every mapping key that
//     IsSensitiveEnvKey matches, in block or flow style, on its line or the
//     next, block scalars included; every `KEY=value` sequence item (block or
//     flow) whose KEY matches. A value under a sensitive key that is itself a
//     mapping or sequence has every scalar in it masked.
//   - Lines, in every file: `KEY=value`, `KEY: value`, `- KEY=value`,
//     `export KEY=value`, quoted keys and items, and the same commented out
//     with `#`, plus `KEY=value` / `KEY: value` tokens inside { } and [ ] on
//     the line. This is what covers a YAML file that does not parse and every
//     other file.
//   - URL credentials anywhere (RedactURLUserinfoInText), whatever the key.
//   - Any of secrets (values from the stack's env files) found verbatim.
//
// What it leaves visible: a value that is only a reference, ${X}, $X, ${X?msg}
// or ${X:?msg}, because it holds no secret and hiding it would make every
// well-written compose file read-only while locked. A reference with a
// default, ${X:-literal} or ${X-literal}, is masked: the default can be the
// secret.
//
// Where it cannot tell where a value ends (a multi-line plain or quoted
// scalar, a value it found on the next line, a flow value under a sensitive
// key), it masks the rest of the line and every following line indented
// deeper than the key's line: it fails closed. It over-masks by design, as
// IsSensitiveEnvKey does; a wrongly hidden value costs one unlock.
//
// Free text is covered where it names the key: `--db-password=x` and
// `PASSWORD=x` tokens anywhere on a line. Residual, not covered: a secret
// that no sensitive name points at and that is not in the stack's env files
// (`command: hunter2`).
func MaskSecrets(text, filePath string, secrets []string) (string, int) {
	if text == "" {
		return text, 0
	}
	m := &lineMasker{lines: strings.Split(text, "\n")}
	m.masked = make([]bool, len(m.lines))

	if ext := strings.ToLower(path.Ext(filePath)); ext == ".yml" || ext == ".yaml" {
		m.yamlPass(text)
	}
	m.linePass()
	for i, line := range m.lines {
		if out := RedactURLUserinfoInText(line); out != line {
			m.lines[i] = out
			m.count++
		}
	}
	for _, v := range secrets {
		if len(v) < composeSecretMinLen || strings.Contains(v, "\n") {
			continue
		}
		for i, line := range m.lines {
			if strings.Contains(line, v) {
				m.lines[i] = strings.ReplaceAll(line, v, secretMask)
				m.count++
			}
		}
	}
	return strings.Join(m.lines, "\n"), m.count
}

type lineMasker struct {
	lines []string
	// masked marks a line whose whole value part has been replaced, so a
	// later pass does not count it again.
	masked []bool
	count  int
}

// isSecretReference reports whether a value is only a variable reference
// with no default: ${X}, $X, ${X?msg}, ${X:?msg}.
var secretReferenceRe = regexp.MustCompile(`^(\$[A-Za-z_][A-Za-z0-9_]*|\$\{[A-Za-z_][A-Za-z0-9_]*(:?\?[^}]*)?\})$`)

func isSecretReference(v string) bool {
	return secretReferenceRe.MatchString(strings.TrimSpace(v))
}

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// maskDeeper masks every line after index i that is blank or indented deeper
// than indent, stopping at the first that is not. It reports whether any was
// masked.
func (m *lineMasker) maskDeeper(i, indent int) bool {
	any := false
	for j := i + 1; j < len(m.lines); j++ {
		l := m.lines[j]
		if strings.TrimSpace(l) == "" {
			continue
		}
		if indentOf(l) <= indent {
			break
		}
		if !m.masked[j] {
			m.lines[j] = l[:indentOf(l)] + secretMask
			m.masked[j] = true
			any = true
		}
	}
	return any
}

// maskFrom replaces line i from byte col to its end, then fails closed on the
// lines below it.
func (m *lineMasker) maskFrom(i, col int) {
	l := m.lines[i]
	if col > len(l) {
		col = len(l)
	}
	m.lines[i] = l[:col] + secretMask
	m.masked[i] = true
	m.maskDeeper(i, indentOf(l))
	m.count++
}

// ── YAML pass ──────────────────────────────────────────────────────────────

func (m *lineMasker) yamlPass(text string) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return // the line pass covers a file that does not parse
	}
	m.walk(&doc)
}

func (m *lineMasker) walk(n *yaml.Node) {
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			m.walk(c)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if IsSensitiveEnvKey(k.Value) {
				m.maskNode(v, k)
			} else {
				m.walk(v)
			}
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if c.Kind == yaml.ScalarNode {
				if key, val, ok := strings.Cut(c.Value, "="); ok && IsSensitiveEnvVar(strings.TrimSpace(key), val) && !isSecretReference(val) {
					m.maskItemAfterEquals(c)
				}
				continue
			}
			m.walk(c)
		}
	}
}

// maskNode masks the value v of sensitive key k: a scalar's text, or every
// scalar under a mapping or sequence.
func (m *lineMasker) maskNode(v, k *yaml.Node) {
	switch v.Kind {
	case yaml.ScalarNode:
		if v.Tag == "!!null" && v.Value == "" {
			return
		}
		if isSecretReference(v.Value) {
			return
		}
		m.maskScalar(v, k)
	case yaml.MappingNode:
		for i := 1; i < len(v.Content); i += 2 {
			m.maskNode(v.Content[i], k)
		}
	case yaml.SequenceNode:
		for _, c := range v.Content {
			m.maskNode(c, k)
		}
	case yaml.AliasNode:
		// The anchor's value is masked where it is defined, if its key is
		// sensitive; an alias holds no text of its own.
	}
}

// maskScalar replaces scalar n's source text with the mask, keeping quotes.
// k is the key it belongs to, whose line sets how deep a continuation is.
func (m *lineMasker) maskScalar(n, k *yaml.Node) {
	li, col := n.Line-1, n.Column-1
	if li < 0 || li >= len(m.lines) || m.masked[li] {
		return
	}
	line := m.lines[li]
	keyIndent := indentOf(line)
	if k != nil && k.Line-1 >= 0 && k.Line-1 < len(m.lines) {
		keyIndent = indentOf(m.lines[k.Line-1])
	}
	if col > len(line) {
		m.maskFrom(li, len(line))
		return
	}
	switch n.Style {
	case yaml.LiteralStyle, yaml.FoldedStyle:
		// The header (|, >-, ...) stays; the body is every deeper line.
		if m.maskDeeper(li, keyIndent) {
			m.count++
		}
		return
	case yaml.DoubleQuotedStyle, yaml.SingleQuotedStyle:
		q := line[col]
		end := closingQuote(line, col+1, q)
		if end < 0 {
			m.maskFrom(li, col)
			return
		}
		m.lines[li] = line[:col+1] + secretMask + line[end:]
		m.count++
		m.maskDeeper(li, keyIndent)
		return
	}
	// Plain: single-line when the source holds the value verbatim.
	if strings.HasPrefix(line[col:], n.Value) && !continues(m.lines, li, keyIndent) {
		m.lines[li] = line[:col] + secretMask + line[col+len(n.Value):]
		m.count++
		return
	}
	m.maskFrom(li, col)
}

// maskItemAfterEquals masks the part after "=" of a KEY=value sequence item.
func (m *lineMasker) maskItemAfterEquals(n *yaml.Node) {
	li, col := n.Line-1, n.Column-1
	if li < 0 || li >= len(m.lines) || m.masked[li] || col >= len(m.lines[li]) {
		return
	}
	line := m.lines[li]
	eq := strings.IndexByte(line[col:], '=')
	if eq < 0 {
		m.maskFrom(li, col)
		return
	}
	start := col + eq + 1
	end := len(line)
	switch line[col] {
	case '"', '\'':
		if e := closingQuote(line, col+1, line[col]); e >= 0 {
			end = e
		} else {
			m.maskFrom(li, start)
			return
		}
	default:
		if e := strings.IndexAny(line[start:], ",]}#"); e >= 0 {
			end = start + e
			for end > start && line[end-1] == ' ' {
				end--
			}
		}
	}
	m.lines[li] = line[:start] + secretMask + line[end:]
	m.count++
}

// closingQuote is the index of the quote that closes a scalar opened just
// before from, or -1 when it does not close on this line.
func closingQuote(line string, from int, q byte) int {
	for i := from; i < len(line); i++ {
		switch {
		case q == '"' && line[i] == '\\':
			i++
		case line[i] == q:
			if q == '\'' && i+1 < len(line) && line[i+1] == '\'' {
				i++
				continue
			}
			return i
		}
	}
	return -1
}

// continues reports whether the line after li is a continuation of a plain
// scalar: a non-blank line indented deeper than the key.
func continues(lines []string, li, keyIndent int) bool {
	for j := li + 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "" {
			continue
		}
		return indentOf(lines[j]) > keyIndent && !strings.HasPrefix(strings.TrimSpace(lines[j]), "#")
	}
	return false
}

// ── line pass ──────────────────────────────────────────────────────────────

// leadKeyRe is a line that starts with a key: optional comment marker, list
// dash and "export", an optionally quoted key, then = or a YAML colon.
var leadKeyRe = regexp.MustCompile(`^(\s*(?:#\s*)?(?:-\s+)?(?:export\s+)?)(["']?)([A-Za-z_][A-Za-z0-9_.\-]*)(["']?)(\s*=\s*|\s*:(?:\s+|$))`)

// flowKeyRe is a KEY=value or KEY: value token inside { } or [ ].
var flowKeyRe = regexp.MustCompile(`([\{\[,]\s*)(["']?)([A-Za-z_][A-Za-z0-9_.\-]*)(["']?)(\s*=\s*|\s*:\s+)`)

// blockIndicatorRe is a YAML block scalar header: |, >, with chomping and
// indentation indicators.
var blockIndicatorRe = regexp.MustCompile(`^[|>][+-]?[0-9]?[+-]?$`)

func (m *lineMasker) linePass() {
	for i := range m.lines {
		if m.masked[i] {
			continue
		}
		m.maskLeadKey(i)
		if !m.masked[i] {
			m.maskFlowTokens(i)
		}
		if !m.masked[i] {
			m.maskInlineKV(i)
		}
	}
}

func (m *lineMasker) maskLeadKey(i int) {
	line := m.lines[i]
	loc := leadKeyRe.FindStringSubmatchIndex(line)
	if loc == nil {
		return
	}
	key := line[loc[6]:loc[7]]
	openQ, closeQ := line[loc[4]:loc[5]], line[loc[8]:loc[9]]
	start := loc[1]
	rest := line[start:]
	value, _ := splitTrailingComment(rest)
	if !IsSensitiveEnvKey(key) && !HasURLUserinfo(value) {
		return
	}
	trimmed := strings.TrimSpace(value)

	// A quoted item "KEY=value": the value ends at the item's closing quote.
	if openQ != "" && closeQ == "" {
		if e := closingQuote(line, start, openQ[0]); e >= 0 {
			v := line[start:e]
			if isSecretReference(v) || v == secretMask {
				return
			}
			m.lines[i] = line[:start] + secretMask + line[e:]
			m.count++
			return
		}
		m.maskFrom(i, start)
		return
	}

	switch {
	case trimmed == "" || blockIndicatorRe.MatchString(trimmed):
		// The value is on the following lines: a block scalar, a nested
		// mapping or sequence, or a next-line plain value.
		if m.maskDeeper(i, indentOf(line)) {
			m.count++
		}
	case trimmed == secretMask || isSecretReference(trimmed):
		// Already masked by the YAML pass, or only a reference.
	case trimmed[0] == '{' || trimmed[0] == '[':
		m.maskFrom(i, start)
	case trimmed[0] == '"' || trimmed[0] == '\'':
		vs := start + strings.Index(rest, trimmed[:1])
		e := closingQuote(line, vs+1, trimmed[0])
		if e < 0 {
			m.maskFrom(i, vs)
			return
		}
		if v := line[vs+1 : e]; isSecretReference(v) || v == secretMask {
			return
		}
		m.lines[i] = line[:vs+1] + secretMask + line[e:]
		m.count++
		m.maskDeeper(i, indentOf(line))
	default:
		vs := start + strings.Index(rest, trimmed)
		ve := vs + len(trimmed)
		m.lines[i] = line[:vs] + secretMask + line[ve:]
		m.count++
		m.maskDeeper(i, indentOf(line))
	}
}

// maskFlowTokens masks the values of sensitive KEY=value / KEY: value tokens
// inside { } or [ ] on line i.
func (m *lineMasker) maskFlowTokens(i int) {
	for {
		line := m.lines[i]
		changed := false
		for _, loc := range flowKeyRe.FindAllStringSubmatchIndex(line, -1) {
			key := line[loc[6]:loc[7]]
			start := loc[1]
			end := start
			if start < len(line) && (line[start] == '"' || line[start] == '\'') {
				e := closingQuote(line, start+1, line[start])
				if e < 0 {
					if IsSensitiveEnvKey(key) {
						m.maskFrom(i, start)
					}
					return
				}
				end = e + 1
			} else {
				for end < len(line) && !strings.ContainsRune(",}]", rune(line[end])) {
					end++
				}
				// The token's own closing quote ("KEY=v", in a list).
				if q := line[loc[4]:loc[5]]; q != "" {
					if e := strings.LastIndex(line[start:end], q); e >= 0 {
						end = start + e
					}
				}
			}
			value := strings.TrimSpace(line[start:end])
			v := strings.Trim(value, `"'`)
			if (!IsSensitiveEnvKey(key) && !HasURLUserinfo(v)) || v == secretMask || v == "" || isSecretReference(v) {
				continue
			}
			for end > start && line[end-1] == ' ' {
				end--
			}
			repl := secretMask
			if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') {
				repl = value[:1] + secretMask + value[:1]
			}
			m.lines[i] = line[:start] + repl + line[end:]
			m.count++
			changed = true
			break // indices moved; scan the new line again
		}
		if !changed {
			return
		}
	}
}

// inlineKVRe is a KEY=value token anywhere in free text, a command-line flag
// included (--db-password=x): after a line start, space, quote, bracket,
// brace, comma or parenthesis.
var inlineKVRe = regexp.MustCompile(`(?:^|[\s"'\[\{,(])-{0,2}([A-Za-z_][A-Za-z0-9_.\-]*)=`)

// maskInlineKV masks the value of every sensitive KEY=value token on line i,
// up to the next space, quote, comma or closing bracket.
func (m *lineMasker) maskInlineKV(i int) {
	for {
		line := m.lines[i]
		changed := false
		for _, loc := range inlineKVRe.FindAllStringSubmatchIndex(line, -1) {
			key := line[loc[2]:loc[3]]
			start := loc[1]
			end := start
			if strings.HasPrefix(line[start:], "${") {
				// A reference ends at its own brace, not at the first stop.
				if e := strings.IndexByte(line[start:], '}'); e >= 0 {
					end = start + e + 1
				}
			}
			for end < len(line) && !strings.ContainsRune(" \t\"',]})", rune(line[end])) {
				end++
			}
			v := line[start:end]
			if v == "" || v == secretMask || isSecretReference(v) || (!IsSensitiveEnvKey(key) && !HasURLUserinfo(v)) {
				continue
			}
			m.lines[i] = line[:start] + secretMask + line[end:]
			m.count++
			changed = true
			break
		}
		if !changed {
			return
		}
	}
}

// splitTrailingComment splits " # comment" off an unquoted value.
func splitTrailingComment(s string) (value, comment string) {
	t := strings.TrimLeft(s, " \t")
	if t != "" && (t[0] == '"' || t[0] == '\'') {
		return s, ""
	}
	if i := strings.Index(s, " #"); i >= 0 {
		return s[:i], s[i:]
	}
	return s, ""
}

// ── diffs ──────────────────────────────────────────────────────────────────

// fullContextArg makes git print each changed file whole: one hunk holding
// every line of both sides, which is what MaskFullContextDiff rebuilds them
// from.
const fullContextArg = "--unified=999999999"

// diffContextLines is how much context MaskFullContextDiff re-cuts hunks
// with, git's default.
const diffContextLines = 3

var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// MaskFullContextDiff masks a unified diff that git printed with
// fullContextArg, as the diff of the masked old file against the masked new
// file (agent-os-w6wx): each hunk's two sides are rebuilt, masked whole with
// MaskSecrets, put back line by line (MaskSecrets keeps the line count), and
// the hunks are re-cut to diffContextLines of context. A hunk that would have
// started inside a multi-line value is therefore masked with the whole value
// in view. A changed line whose two masked sides are equal stays a -/+ pair:
// it shows that a value changed, never what to.
//
// Hunk headers are rebuilt without git's trailing function-context text, which
// quotes a line from outside the hunk. File headers, binary notices and mode
// lines are kept as git printed them. A hunk whose counts do not match its
// body is masked line by line with no context, failing closed.
func MaskFullContextDiff(diff string, secrets []string) string {
	if diff == "" {
		return diff
	}
	lines := strings.Split(diff, "\n")
	var out []string
	filePath := ""
	for i := 0; i < len(lines); {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "diff --git "):
			filePath = ""
			out = append(out, line)
			i++
		case strings.HasPrefix(line, "+++ "):
			if p := strings.TrimPrefix(line, "+++ "); p != "/dev/null" {
				filePath = strings.TrimPrefix(p, "b/")
			}
			out = append(out, line)
			i++
		case strings.HasPrefix(line, "--- "):
			if filePath == "" {
				filePath = strings.TrimPrefix(strings.TrimPrefix(line, "--- "), "a/")
			}
			out = append(out, line)
			i++
		case strings.HasPrefix(line, "@@ "):
			j := i + 1
			for j < len(lines) && len(lines[j]) > 0 && strings.ContainsRune(" +-\\", rune(lines[j][0])) &&
				!strings.HasPrefix(lines[j], "--- ") && !strings.HasPrefix(lines[j], "+++ ") {
				j++
			}
			// A context line whose content is empty prints as " ", never "";
			// a body ends at the first empty line or the next header.
			out = append(out, maskHunk(line, lines[i+1:j], filePath, secrets)...)
			i = j
		default:
			out = append(out, line)
			i++
		}
	}
	return strings.Join(out, "\n")
}

type diffOp struct {
	kind   byte // ' ', '-', '+'
	text   string
	noEOL  bool // followed by "\ No newline at end of file"
	oldNo  int  // this line's number on the old side, or the next one's
	newNo  int
	maskTx string
}

func maskHunk(header string, body []string, filePath string, secrets []string) []string {
	m := hunkHeaderRe.FindStringSubmatch(header)
	failClosed := func() []string {
		out := []string{hunkHeaderRe.FindString(header)}
		for _, l := range body {
			if l == "" || l[0] == '\\' {
				out = append(out, l)
				continue
			}
			masked, _ := MaskSecrets(l[1:], filePath, secrets)
			if masked == l[1:] && strings.TrimSpace(l[1:]) != "" {
				// Seen out of context, an unchanged line may still sit
				// inside a multi-line value.
				masked = secretMask
			}
			out = append(out, l[:1]+masked)
		}
		return out
	}
	if m == nil {
		return failClosed()
	}
	oldStart, newStart := hunkNum(m[1], 0), hunkNum(m[3], 0)

	var ops []diffOp
	var oldLines, newLines []string
	oldNo, newNo := oldStart, newStart
	for _, l := range body {
		if l == "" {
			continue
		}
		if l[0] == '\\' {
			if len(ops) > 0 {
				ops[len(ops)-1].noEOL = true
			}
			continue
		}
		op := diffOp{kind: l[0], text: l[1:], oldNo: oldNo, newNo: newNo}
		switch op.kind {
		case ' ':
			oldLines = append(oldLines, op.text)
			newLines = append(newLines, op.text)
			oldNo++
			newNo++
		case '-':
			oldLines = append(oldLines, op.text)
			oldNo++
		case '+':
			newLines = append(newLines, op.text)
			newNo++
		default:
			return failClosed()
		}
		ops = append(ops, op)
	}
	if (m[2] != "" && hunkNum(m[2], -1) != len(oldLines)) || (m[4] != "" && hunkNum(m[4], -1) != len(newLines)) {
		return failClosed()
	}

	maskedOld, _ := MaskSecrets(strings.Join(oldLines, "\n"), filePath, secrets)
	maskedNew, _ := MaskSecrets(strings.Join(newLines, "\n"), filePath, secrets)
	mo, mn := strings.Split(maskedOld, "\n"), strings.Split(maskedNew, "\n")
	if len(oldLines) == 0 {
		mo = nil
	}
	if len(newLines) == 0 {
		mn = nil
	}
	if len(mo) != len(oldLines) || len(mn) != len(newLines) {
		return failClosed()
	}
	oi, ni := 0, 0
	for k := range ops {
		switch ops[k].kind {
		case ' ':
			// A context line is unchanged on both sides; its new side is
			// what the file holds now.
			ops[k].maskTx = mn[ni]
			oi++
			ni++
		case '-':
			ops[k].maskTx = mo[oi]
			oi++
		case '+':
			ops[k].maskTx = mn[ni]
			ni++
		}
	}
	return recutHunks(ops)
}

// recutHunks emits ops as unified-diff hunks with diffContextLines of
// context, as git would have printed them.
func recutHunks(ops []diffOp) []string {
	var changed []int
	for k, op := range ops {
		if op.kind != ' ' {
			changed = append(changed, k)
		}
	}
	var out []string
	for g := 0; g < len(changed); {
		first, last := changed[g], changed[g]
		g++
		for g < len(changed) && changed[g]-last <= 2*diffContextLines+1 {
			last = changed[g]
			g++
		}
		from := max(0, first-diffContextLines)
		to := min(len(ops)-1, last+diffContextLines)
		oldLen, newLen := 0, 0
		for _, op := range ops[from : to+1] {
			if op.kind != '+' {
				oldLen++
			}
			if op.kind != '-' {
				newLen++
			}
		}
		oldStart, newStart := ops[from].oldNo, ops[from].newNo
		if oldLen == 0 {
			oldStart--
		}
		if newLen == 0 {
			newStart--
		}
		out = append(out, "@@ -"+hunkRange(oldStart, oldLen)+" +"+hunkRange(newStart, newLen)+" @@")
		for _, op := range ops[from : to+1] {
			out = append(out, string(op.kind)+op.maskTx)
			if op.noEOL {
				out = append(out, `\ No newline at end of file`)
			}
		}
	}
	return out
}

// hunkNum parses a hunk header number, def when it does not parse.
func hunkNum(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func hunkRange(start, n int) string {
	if n == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(n)
}
