package handlers

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math/rand"
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

// agent-os-1nqr, check 2: every variable compose-go reads from a .env file as
// sensitive is absent from the locked stack view and the locked global view,
// over a seeded generated corpus of the line shapes the parser handles.
func TestEnvGuards_1nqr_ComposeSensitiveValuesHiddenWhileLocked(t *testing.T) {
	rng := rand.New(rand.NewSource(1)) //nolint:gosec // a seeded corpus, not a secret
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
		switch rng.Intn(12) {
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
		default:
			return k
		}
	}

	files, checked := 0, 0
	for range 3000 {
		var b strings.Builder
		for range 1 + rng.Intn(5) {
			b.WriteString(line())
			b.WriteString([]string{"\n", "\r\n"}[rng.Intn(2)])
		}
		content := b.String()
		vars, err := dotenv.UnmarshalWithLookup(content, func(string) (string, bool) { return "", false })
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
