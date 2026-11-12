package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Second review round on agent-os-yt2f (review-redact-delta.md DS1-DS3, DS5).

// DS1: GetGlobalEnv read global.env twice, values from the first read and the
// redaction verdict from the second, so an edit between the reads could pair
// a value with the wrong verdict. It now reads once and builds both from the
// same bytes. A race between two reads cannot be staged in a test, so this
// pins the shape instead: exactly one call that reads the file in GetGlobalEnv.
// Sources come from go:embed (handlerSources), so a -overlay mutant is what it
// sees.
func TestGlobalEnvLocked_Yt2f_ReadsTheFileOnce(t *testing.T) {
	src, err := handlerSources.ReadFile("settings.go")
	require.NoError(t, err)
	file, err := parser.ParseFile(token.NewFileSet(), "settings.go", src, 0)
	require.NoError(t, err)

	readers := map[string]bool{"ReadFile": true, "parseEnvFile": true, "readGlobalEnvFile": true}
	var found bool
	reads := 0
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "GetGlobalEnv" {
			return true
		}
		found = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch f := call.Fun.(type) {
			case *ast.Ident:
				if readers[f.Name] {
					reads++
				}
			case *ast.SelectorExpr:
				if readers[f.Sel.Name] {
					reads++
				}
			}
			return true
		})
		return false
	})
	require.True(t, found, "GetGlobalEnv not found in settings.go; this guard must be updated")
	assert.Equal(t, 1, reads, "GetGlobalEnv must read global.env exactly once and redact from those bytes")
}

// DS2: the fail-closed arm for rows matched before the mismatch is found. TZ
// matches line 1; the file then holds a row the vars do not, so the rows are
// not the file's rows and none may be trusted.
func TestGlobalEnvLocked_Yt2f_PartlyMatchedRowsFailClosed(t *testing.T) {
	vars := []map[string]string{{"key": "TZ", "value": "Europe/Amsterdam"}}
	redactGlobalEnvVars(vars, "TZ=Europe/Amsterdam\nEXTRA=1\n")
	assert.Equal(t, map[string]string{"key": "", "value": ""}, vars[0])
}

// DS3: a colon row's sensitivity was checked against the whole line, and an
// opaque credential (no "://") is only recognised as a whole value, so
// "RESTIC_REPOSITORY: sftp:u:pw@h:/r" reached a locked session.
func TestEnvLocked_Yt2f_ColonRowOpaqueCredentialHidden(t *testing.T) {
	const content = "RESTIC_REPOSITORY: sftp:u:yt2fcolonopaquepw@backup.example.com:/r\n" +
		"REPO: sftp:u:yt2fcolonrepopw@h:/r\n" +
		"TZ: Europe/Amsterdam\n"
	router, stackID, token, _ := envQuoteFixture(t, content)

	unlocked := getEnvBody(t, router, stackID, token)
	require.Contains(t, unlocked, "yt2fcolonopaquepw")
	require.Contains(t, unlocked, "yt2fcolonrepopw")
	locked := getEnvBody(t, router, stackID, "")
	assertBodyOmits(t, locked, "yt2fcolonopaquepw")
	assertBodyOmits(t, locked, "yt2fcolonrepopw")
	assert.Contains(t, locked, "TZ: Europe/Amsterdam", "a non-secret colon row keeps its text")
}
