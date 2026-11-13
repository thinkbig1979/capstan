package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-siou: LintCompose interpolates with the env deploy uses, so a
// ${VAR:?msg} reference is checked against real values instead of failing the
// load.

const requiredVarComposeSiouSvc = "services:\n  db:\n    image: postgres:16\n    environment:\n      POSTGRES_PASSWORD: ${DB_PASS:?DB_PASS is required}\n      OTHER: ${OTHER_REQ?needed}\n"

func rulesSiou(results []models.LintResult) map[string][]models.LintResult {
	out := map[string][]models.LintResult{}
	for _, r := range results {
		out[r.Rule] = append(out[r.Rule], r)
	}
	return out
}

func TestLinterService_RequiredVarSetInEnv_siou(t *testing.T) {
	results, err := NewLinterService().LintCompose(requiredVarComposeSiouSvc, t.TempDir(),
		LintEnv{Vars: map[string]string{"DB_PASS": "hunter2", "OTHER_REQ": ""}})
	require.NoError(t, err)

	byRule := rulesSiou(results)
	for _, r := range results {
		assert.NotEqual(t, "error", r.Level, "unexpected error: %+v", r)
	}
	assert.Empty(t, byRule["required-variable"], "both variables are set (OTHER_REQ? accepts empty)")
	// The load got past interpolation: the service checks ran.
	assert.NotEmpty(t, byRule["no-restart-policy"], "service checks must run: %+v", results)
}

func TestLinterService_RequiredVarUnset_siou(t *testing.T) {
	results, err := NewLinterService().LintCompose(requiredVarComposeSiouSvc, t.TempDir(), LintEnv{})
	require.NoError(t, err)

	for _, r := range results {
		assert.NotEqual(t, "error", r.Level, "a missing variable is a warning: %+v", r)
	}
	byRule := rulesSiou(results)
	require.Len(t, byRule["required-variable"], 2, "both unset variables are named: %+v", results)
	assert.Equal(t, "warning", byRule["required-variable"][0].Level)
	assert.Contains(t, byRule["required-variable"][0].Message, "DB_PASS")
	assert.Equal(t, 5, byRule["required-variable"][0].Line)
	assert.Contains(t, byRule["required-variable"][1].Message, "OTHER_REQ")
	// The uninterpolated reload still runs the service checks.
	assert.NotEmpty(t, byRule["no-restart-policy"], "service checks must run: %+v", results)
}

// :? refuses an empty value, ? does not.
func TestLinterService_RequiredVarEmptyColonForm_siou(t *testing.T) {
	results, err := NewLinterService().LintCompose(requiredVarComposeSiouSvc, t.TempDir(),
		LintEnv{Vars: map[string]string{"DB_PASS": "", "OTHER_REQ": ""}})
	require.NoError(t, err)
	byRule := rulesSiou(results)
	require.Len(t, byRule["required-variable"], 1, "%+v", results)
	assert.Contains(t, byRule["required-variable"][0].Message, "DB_PASS")
}

func TestLinterService_EscapedReferenceIsNotRequired_siou(t *testing.T) {
	compose := "services:\n  web:\n    image: nginx:1.27\n    command: echo $${LITERAL:?x} ${DB_PASS:?y}\n"
	results, err := NewLinterService().LintCompose(compose, t.TempDir(), LintEnv{})
	require.NoError(t, err)
	byRule := rulesSiou(results)
	require.Len(t, byRule["required-variable"], 1, "%+v", results)
	assert.Contains(t, byRule["required-variable"][0].Message, "DB_PASS")
}

// D3: an env_file that is not on disk is a warning for an existing stack
// folder, and nothing at all for a folder that does not exist yet (create).
func TestLinterService_MissingEnvFileIsWarning_siou(t *testing.T) {
	compose := "services:\n  web:\n    image: nginx:1.27\n    env_file: .env\n  opt:\n    image: nginx:1.27\n    env_file:\n      - path: ./optional.env\n        required: false\n"
	dir := t.TempDir()

	results, err := NewLinterService().LintCompose(compose, dir, LintEnv{})
	require.NoError(t, err)
	for _, r := range results {
		assert.NotEqual(t, "error", r.Level, "%+v", r)
	}
	missing := rulesSiou(results)["missing-env-file"]
	require.Len(t, missing, 1, "only the required one: %+v", results)
	assert.Contains(t, missing[0].Message, "web")
	assert.Contains(t, missing[0].Message, ".env")

	// Present on disk: no warning.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("A=1\n"), 0o600))
	results, err = NewLinterService().LintCompose(compose, dir, LintEnv{})
	require.NoError(t, err)
	assert.Empty(t, rulesSiou(results)["missing-env-file"], "%+v", results)

	// Create: the folder does not exist yet.
	results, err = NewLinterService().LintCompose(compose, filepath.Join(dir, "not-yet"), LintEnv{})
	require.NoError(t, err)
	for _, r := range results {
		assert.NotEqual(t, "error", r.Level, "%+v", r)
	}
	assert.Empty(t, rulesSiou(results)["missing-env-file"], "%+v", results)
}

// compose-go quotes interpolated values in its errors. Two-sided: the message
// is about the value (so it was interpolated), and the value is starred.
func TestLinterService_MessagesNeverQuoteSensitiveValues_siou(t *testing.T) {
	const secret = "hunter2SECRETsvc"
	compose := "services:\n  db:\n    image: postgres:16\n    cpus: ${DB_PASSWORD}\n"
	results, err := NewLinterService().LintCompose(compose, t.TempDir(),
		LintEnv{Vars: map[string]string{"DB_PASSWORD": secret, "PLAIN": "visible"}})
	require.NoError(t, err)

	var structure []models.LintResult
	for _, r := range results {
		assert.NotContains(t, r.Message, secret, "%+v", r)
		if r.Rule == "compose-structure" {
			structure = append(structure, r)
		}
	}
	require.Len(t, structure, 1, "%+v", results)
	assert.True(t, strings.Contains(structure[0].Message, "***"), "the value is replaced: %q", structure[0].Message)
}

func TestLintEnv_WithEnvTextOverlays_siou(t *testing.T) {
	base := LintEnv{Vars: map[string]string{"A": "old", "B": "keep"}}
	got := base.WithEnvText("A=new\nC=${B}-x\n")
	assert.False(t, got.Unresolved)
	assert.Equal(t, "new", got.Vars["A"])
	assert.Equal(t, "keep", got.Vars["B"])
	assert.Equal(t, "keep-x", got.Vars["C"])
	assert.Equal(t, "old", base.Vars["A"], "the base map is not mutated")

	bad := base.WithEnvText("A=\"unterminated\n")
	assert.True(t, bad.Unresolved)
}

// An env file compose cannot read yields a warning that never quotes it.
func TestStackLintEnv_UnreadableEnvFileWarns_siou(t *testing.T) {
	dir := t.TempDir()
	const secret = "s3cretLINE"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("DB_PASSWORD=\""+secret+"\n"), 0o600))
	env := StackLintEnv(models.Stack{Directory: dir, ComposeFile: "compose.yaml", EnvFile: ".env"}, "")
	require.True(t, env.Unresolved)

	results, err := NewLinterService().LintCompose("services:\n  web:\n    image: nginx:1.27\n", dir, env)
	require.NoError(t, err)
	unresolved := rulesSiou(results)["env-unresolved"]
	require.Len(t, unresolved, 1, "%+v", results)
	for _, r := range results {
		assert.NotContains(t, r.Message, secret)
	}
}
