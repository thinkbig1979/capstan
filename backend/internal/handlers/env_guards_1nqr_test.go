package handlers

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"math/rand"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-1nqr, check 1: serializeEnvFile alone loses a file's indentation,
// CRLF line ends and final-newline state (agent-os-omzj, agent-os-4qiw), and
// writes a fresh value raw (agent-os-uxfl). So env text is written only
// through the functions that write over the file's text with fileEOL, and
// the other uses are probes whose output is parsed, never written. Sources
// come from go:embed (handlerSources), so a `go test -overlay` mutant is what
// this reads.
var serializeEnvFileUsers = map[string]string{
	"serializeEnvFileOver":  "writes a table save over the file's text",
	"mergeGlobalEnv":        "writes a global env save over the file's text",
	"validateEnvCommentRow": "probe: parses the row back",
	"validateEnvKey":        "probe: reads the key back through compose-go",
	"composeReadsValue":     "probe: reads the value back through compose-go",
}

func TestEnvGuards_1nqr_SerializeEnvFileOnlyFromSafeEntryPoints(t *testing.T) {
	entries, err := handlerSources.ReadDir(".")
	require.NoError(t, err)
	fset := token.NewFileSet()
	var sites, violations []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := handlerSources.ReadFile(name)
		require.NoError(t, err)
		f, err := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, err)
		for _, decl := range f.Decls {
			owner := "<package level>"
			if fd, ok := decl.(*ast.FuncDecl); ok {
				owner = fd.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if !ok || id.Name != "serializeEnvFile" {
					return true
				}
				if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name == id {
					return true // the declaration itself
				}
				site := fmt.Sprintf("%s (%s)", fset.Position(id.Pos()), owner)
				sites = append(sites, site)
				if _, ok := serializeEnvFileUsers[owner]; !ok {
					violations = append(violations, site)
				}
				return true
			})
		}
	}
	// Positive side: the walk sees every known use (a reference, not only a
	// call, so `f := serializeEnvFile` is caught too).
	require.GreaterOrEqual(t, len(sites), len(serializeEnvFileUsers), "sites seen: %v", sites)
	assert.Empty(t, violations, "serializeEnvFile used outside its safe entry points; write env text with serializeEnvFileOver (stack) or mergeGlobalEnv (global)")
}

// genEnvCorpus is a seeded corpus of .env files over the line shapes the
// parser handles: KEY=v, quoted both ways, export, KEY: v, an inline comment,
// indentation, a second statement after a closing quote, a quoted value
// across two lines (also with a statement after its close, review F3, and a
// URL credential split across those lines, review R2-1), a
// commented-out assignment and a bare KEY; LF or CRLF, and a third of the
// files with no final line end (the qdnb shape, review F3). Values are
// unique, so a value found in a body is that variable's.
func genEnvCorpus(n int, seed int64) []string {
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // a seeded corpus, not a secret
	keys := []string{"DB_PASSWORD", "API_TOKEN", "SECRET", "AWS_SECRET_ACCESS_KEY", "TZ", "PORT", "NOTES", "A", "DATABASE_URL"}
	seq := 0
	value := func() string {
		seq++
		switch rng.Intn(4) {
		case 0:
			return fmt.Sprintf("postgres://u:pw%dzq@db/app", seq)
		case 1:
			return fmt.Sprintf("u%dzq:pw%dzq@host", seq, seq)
		default:
			return fmt.Sprintf("val%dzq", seq)
		}
	}
	key := func() string { return keys[rng.Intn(len(keys))] }
	line := func() string {
		k, v := key(), value()
		switch rng.Intn(14) {
		case 0:
			return k + "=" + v
		case 1:
			return k + "='" + v + "'"
		case 2:
			return k + `="` + v + `"`
		case 3:
			return "export " + k + "=" + v
		case 4:
			return k + ": " + v
		case 5:
			return k + "=" + v + " # note"
		case 6:
			return "  " + k + " = " + v
		case 7:
			return key() + `="x" ` + k + "=" + v
		case 8:
			return key() + `='x' ` + k + ": '" + v + "'"
		case 9:
			return k + `="` + v + "\n" + value() + `"`
		case 10:
			return "#" + k + "=" + v
		case 11:
			return k + `="` + v + "\n" + value() + `" ` + key() + "=" + value()
		case 12:
			// A URL credential split across an unfolded span (review R2-1).
			seq++
			return k + fmt.Sprintf(`="postgres://u:`+"\n"+`pw%dzq@db/app" `, seq) + key() + "=" + value()
		default:
			return k
		}
	}
	files := make([]string, 0, n)
	for range n {
		var b strings.Builder
		for range 1 + rng.Intn(5) {
			b.WriteString(line())
			b.WriteString([]string{"\n", "\r\n"}[rng.Intn(2)])
		}
		content := b.String()
		if rng.Intn(3) == 0 {
			content = strings.TrimSuffix(strings.TrimSuffix(content, "\n"), "\r")
		}
		files = append(files, content)
	}
	return files
}

func composeReadsLenient(content string) (map[string]string, error) {
	return dotenv.UnmarshalWithLookup(content, func(string) (string, bool) { return "", false })
}

// agent-os-1nqr, check 2: every variable compose-go reads from a .env file as
// sensitive is absent from the locked stack view and the locked global view,
// over the generated corpus.
func TestEnvGuards_1nqr_ComposeSensitiveValuesHiddenWhileLocked(t *testing.T) {
	files, checked := 0, 0
	for _, content := range genEnvCorpus(3000, 1) {
		vars, err := composeReadsLenient(content)
		if err != nil {
			continue // a file compose rejects defines nothing
		}
		files++

		h := &EnvHandler{}
		resp := &EnvResponse{Entries: h.parseEnvFile(content), Raw: content}
		redactEnvResponse(resp)
		stackBody, err := json.Marshal(resp)
		require.NoError(t, err)
		globalBody, err := json.Marshal(globalEnvResponseVars(content, true))
		require.NoError(t, err)

		names := make([]string, 0, len(vars))
		for k := range vars {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			v := vars[k]
			if k == "" || !services.IsSensitiveEnvVar(k, v) || len(v) < 6 {
				continue
			}
			checked++
			for _, part := range strings.Split(v, "\n") {
				if len(part) < 6 {
					continue
				}
				if !assert.NotContains(t, string(stackBody), part, "stack, file %q, key %s", content, k) ||
					!assert.NotContains(t, string(globalBody), part, "global, file %q, key %s", content, k) {
					return
				}
			}
		}
	}
	// Positive side: the corpus really holds sensitive variables compose reads.
	require.Greater(t, files, 1000)
	require.Greater(t, checked, 1000)
	t.Logf("files=%d sensitive values checked=%d", files, checked)
}

// editValues are typed values each needing a different quoting (or none) for
// compose to read them as typed, and some no quoting keeps.
var editValues = []string{"plain%d", "a # b%d", "'x%d", "p$y%d", "it's%d", `a"b%d`, `a\'b%d`, `'q"%d`, "  sp%d  "}

// agent-os-1nqr review F3, write side: after a save that edits every
// editable row, compose-go reads each edited variable as the typed text and
// every other variable as before, or the save is refused. Stack table
// (serializeEnvFileOver) and global page (mergeGlobalEnv, unquoted values).
// Only files where compose and the table agree on which keys exist, each
// once, are judged, so a key compose takes from a later duplicate or a second
// statement cannot pass for an edit.
func TestEnvGuards_1nqr_EditedSaveReadsBackAsTyped(t *testing.T) {
	rng := rand.New(rand.NewSource(2)) //nolint:gosec // a seeded choice of edits
	judged, stackSaved, globalSaved, droppedByRowText := 0, 0, 0, 0
	droppedExample := ""
	for n, content := range genEnvCorpus(3000, 3) {
		before, err := composeReadsLenient(content)
		if err != nil {
			continue
		}
		rows := (&EnvHandler{}).parseEnvFile(content)
		rowKeys := map[string]int{}
		for _, r := range rows {
			if r.Key != "" {
				rowKeys[r.Key]++
			}
		}
		agree := len(rowKeys) == len(before)
		for k, c := range rowKeys {
			if _, ok := before[k]; !ok || c != 1 {
				agree = false
			}
		}
		if !agree {
			continue
		}
		// Each row's own text must define exactly its key to compose, or a
		// key passes for one row while compose takes it from another (a
		// second statement after a closing quote, say).
		for _, r := range rows {
			if r.Key == "" {
				continue
			}
			own, oerr := composeReadsLenient(serializeEnvFile([]EnvEntry{r}))
			if _, ok := own[r.Key]; oerr != nil || len(own) != 1 || !ok {
				agree = false
			}
		}
		if !agree {
			droppedByRowText++
			if droppedExample == "" {
				droppedExample = content
			}
			continue
		}
		judged++

		// Stack table: edit every editable row.
		want := maps.Clone(before)
		edited := slices.Clone(rows)
		for i := range edited {
			if edited[i].Comment || edited[i].Key == "" {
				continue
			}
			v := fmt.Sprintf(editValues[rng.Intn(len(editValues))], n*10+i)
			edited[i].Value = v
			want[edited[i].Key] = v
		}
		if out, err := serializeEnvFileOver(content, edited); err == nil {
			stackSaved++
			after, cerr := composeReadsLenient(out)
			if !assert.NoError(t, cerr, "stack save broke the file: %q -> %q", content, out) ||
				!assert.Equal(t, want, after, "stack save: %q -> %q", content, out) {
				return
			}
		}

		// Global page: change every editable variable to a typed value.
		vars := globalEnvResponseVars(content, false)
		req := make([]EnvEntry, len(vars))
		gwant := maps.Clone(before)
		for i, v := range vars {
			req[i] = EnvEntry{Key: v.Key, Value: v.Value}
			if rows[i].Comment {
				continue // a read-only line, whose change is refused
			}
			nv := fmt.Sprintf(editValues[rng.Intn(len(editValues))], n*10+i)
			req[i].Value, gwant[v.Key] = nv, strings.TrimSpace(nv)
		}
		if out, err := mergeGlobalEnv(content, req); err == nil {
			globalSaved++
			after, cerr := composeReadsLenient(out)
			if !assert.NoError(t, cerr, "global save broke the file: %q -> %q", content, out) ||
				!assert.Equal(t, gwant, after, "global save: %q -> %q", content, out) {
				return
			}
		}
	}
	// Positive side: enough files were judged and enough saves went through.
	require.Greater(t, judged, 500)
	require.Greater(t, stackSaved, 100)
	require.Greater(t, globalSaved, 50)
	t.Logf("judged=%d stack_saved=%d global_saved=%d dropped_by_row_text=%d first_dropped=%q", judged, stackSaved, globalSaved, droppedByRowText, droppedExample)
}
