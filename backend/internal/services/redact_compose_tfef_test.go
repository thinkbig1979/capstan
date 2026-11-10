package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-tfef: composeSecrets must learn the value compose RESOLVES from the
// env files, not the text a hand parser cuts out of them. Each case below is a
// dotenv shape where the two differ; the resolved values were observed with
// compose-go's dotenv and with `docker compose config` (Compose v5.7.0).

const tfefVisible = "dbhost-tfef-visible"

// tfefSecrets writes global.env (when non-empty) and the stack's .env, names
// .env as the stack's EnvFile (what the scanner records when one exists), and
// returns what composeSecrets learns.
func tfefSecrets(t *testing.T, globalEnv, stackEnv string) []string {
	t.Helper()
	svc, stack := stubbedStatusService(t)
	if globalEnv != "" {
		require.NoError(t, os.WriteFile(filepath.Join(svc.config.DataDir, "global.env"), []byte(globalEnv), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(stack.Directory, ".env"),
		[]byte(stackEnv+"\nDB_HOST="+tfefVisible+"\n"), 0o600))
	stack.EnvFile = ".env"
	return svc.composeSecrets(stack)
}

// assertTfefMasked checks both sides on one string: the resolved secret is
// starred, and the non-sensitive value beside it is not.
func assertTfefMasked(t *testing.T, secrets []string, resolved string) {
	t.Helper()
	out := redactComposeOutput("pw="+resolved+" host="+tfefVisible, secrets)
	assert.NotContains(t, out, resolved, "the compose-resolved value leaks")
	assert.Contains(t, out, "pw=***")
	assert.Contains(t, out, "host="+tfefVisible, "a non-sensitive value was redacted")
}

func TestTfef_SingleQuotedCommentMasked(t *testing.T) {
	assertTfefMasked(t, tfefSecrets(t, "", `DB_PASSWORD='ab$cd' # c`), `ab$cd`)
}

func TestTfef_DoubleQuotedEscapeMasked(t *testing.T) {
	assertTfefMasked(t, tfefSecrets(t, "", `DB_PASSWORD="a\"bcdef"`), `a"bcdef`)
}

func TestTfef_ColonSeparatorMasked(t *testing.T) {
	assertTfefMasked(t, tfefSecrets(t, "", "DB_PASSWORD: secretvalue"), "secretvalue")
}

func TestTfef_InterpolatedFromGlobalEnvMasked(t *testing.T) {
	assertTfefMasked(t, tfefSecrets(t, "OTHER=xyz12345\n", "DB_PASSWORD=${OTHER}"), "xyz12345")
}

func TestTfef_InterpolatedFromSameFileMasked(t *testing.T) {
	assertTfefMasked(t, tfefSecrets(t, "", "OTHER=xyz12345\nDB_PASSWORD=${OTHER}"), "xyz12345")
}

// Compose resolves ${OTHER} to the stack file's own OTHER even though
// global.env also sets it (observed with `docker compose --env-file g.env
// --env-file s.env config`). What this rules out is parsing each file alone
// with global.env's values as the lookup: compose-go consults a lookup before
// the file's own values, so that design resolves "fromglobal" (observed with
// dotenv.UnmarshalWithLookup, v2.16.1). It does NOT test the order of the
// files; TestTfef_InterpolatedFromGlobalEnvMasked does.
func TestTfef_SameFileValueBeatsGlobalEnv(t *testing.T) {
	secrets := tfefSecrets(t, "OTHER=fromglobal\n", "OTHER=fromstack1\nDB_PASSWORD=${OTHER}")
	assertTfefMasked(t, secrets, "fromstack1")
}

func TestTfef_EscapedNewlineMasked(t *testing.T) {
	assertTfefMasked(t, tfefSecrets(t, "", `DB_PASSWORD="line1\nline2"`), "line1\nline2")
}

// Compose also interpolates from its own process environment, which is
// dockerenv's allowlist (cmd.Env = dockerEnv()). DOCKER_CERT_PATH is on it.
func TestTfef_InterpolatedFromComposeProcessEnvMasked(t *testing.T) {
	t.Setenv("DOCKER_CERT_PATH", "/certs/tfefcert99")
	assertTfefMasked(t, tfefSecrets(t, "", "DB_PASSWORD=${DOCKER_CERT_PATH}"), "/certs/tfefcert99")
}

// The scanner records EnvFile=".env.<stack name>" when that file exists
// (determineEnvFile), and compose then reads it instead of .env. A secret only
// in that file must be resolved too, not just found by the old parser.
func TestTfef_NamedEnvFileMasked(t *testing.T) {
	svc, stack := stubbedStatusService(t)
	require.NoError(t, os.WriteFile(filepath.Join(stack.Directory, ".env.myapp"),
		[]byte(`DB_PASSWORD='ab$cdzz' # c`+"\nDB_HOST="+tfefVisible+"\n"), 0o600))
	stack.EnvFile = ".env.myapp"
	assertTfefMasked(t, svc.composeSecrets(stack), `ab$cdzz`)
}

// PIN, not a fail-first test: it passes before the fix too. compose-go rejects
// this file at line 1 and resolves nothing, so the value is masked only
// because the old parser's output is still unioned in (fail closed).
func TestTfef_RejectedFileKeepsOldParserValues(t *testing.T) {
	assertTfefMasked(t, tfefSecrets(t, "", "foo bar\nDB_PASSWORD=goodvalue1"), "goodvalue1")
}
