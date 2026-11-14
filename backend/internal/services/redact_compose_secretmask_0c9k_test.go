package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-0c9k: the secret list missed a `KEY: value` line, held only the
// first line of a multi-line value, and lost every value of a stack's env file
// when another file of its chain did not parse. The masker then skipped any
// secret holding a newline, and the streaming compose consumers redact one
// line at a time, so a multi-line secret never matched there either.

const (
	sm0c9kColon   = "colon-secret-0c9k"
	sm0c9kLine1   = "pem-line-one-0c9k"
	sm0c9kLine2   = "pem-line-two-0c9k"
	sm0c9kVisible = "visible-host-0c9k"
)

// sm0c9kStackEnv is a stack .env holding a `KEY: value` secret, a
// double-quoted two-line secret and a non-sensitive value.
const sm0c9kStackEnv = "DB_PASSWORD: " + sm0c9kColon + "\n" +
	"CERT_KEY=\"" + sm0c9kLine1 + "\n" + sm0c9kLine2 + "\"\n" +
	"DB_HOST=" + sm0c9kVisible + "\n"

// sm0c9kSecrets writes global.env (when non-empty) and the stack's .env, and
// returns what composeSecrets learns from them.
func sm0c9kSecrets(t *testing.T, globalEnv string) []string {
	t.Helper()
	svc, stack := stubbedStatusService(t)
	if globalEnv != "" {
		require.NoError(t, os.WriteFile(filepath.Join(svc.config.DataDir, "global.env"), []byte(globalEnv), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(stack.Directory, ".env"), []byte(sm0c9kStackEnv), 0o600))
	stack.EnvFile = ".env"
	return svc.composeSecrets(stack)
}

func TestSensitiveEnvValues0c9k_ColonSeparator(t *testing.T) {
	got := sensitiveEnvValues("DB_PASSWORD: " + sm0c9kColon + "\nDB_HOST: " + sm0c9kVisible + "\n")
	assert.Contains(t, got, sm0c9kColon, "a `KEY: value` secret is not collected")
	assert.NotContains(t, got, sm0c9kVisible, "a non-sensitive value was collected")
}

func TestSensitiveEnvValues0c9k_MultiLineQuotedValue(t *testing.T) {
	got := sensitiveEnvValues(sm0c9kStackEnv)
	assert.Contains(t, got, sm0c9kLine1, "the first line of a multi-line value is not collected on its own")
	assert.Contains(t, got, sm0c9kLine2, "the second line of a multi-line value is not collected")
	assert.Contains(t, got, sm0c9kLine1+"\n"+sm0c9kLine2, "the whole multi-line value is not collected")
}

// An unterminated quote runs to the end of the file: everything after it may
// be the value, so it is all collected (fail closed).
func TestSensitiveEnvValues0c9k_UnterminatedQuoteFailsClosed(t *testing.T) {
	got := sensitiveEnvValues("API_TOKEN=\"" + sm0c9kLine1 + "\n" + sm0c9kLine2 + "\n")
	assert.Contains(t, got, sm0c9kLine2)
}

// A broken global.env must not drop the stack's own values: compose-go stops
// at the first file of a chain it cannot parse.
func TestStackSecretValues0c9k_BrokenSiblingEnvFile(t *testing.T) {
	got := sm0c9kSecrets(t, "BROKEN=\"never closed\n")
	for _, want := range []string{sm0c9kColon, sm0c9kLine1, sm0c9kLine2} {
		assert.Contains(t, got, want)
	}
	assert.NotContains(t, got, sm0c9kVisible)
}

// The control: the same stack .env with a healthy global.env.
func TestStackSecretValues0c9k_HealthyChain(t *testing.T) {
	got := sm0c9kSecrets(t, "OTHER=value\n")
	for _, want := range []string{sm0c9kColon, sm0c9kLine1, sm0c9kLine2} {
		assert.Contains(t, got, want)
	}
}

// The streaming consumers (RunStreaming, streamComposeCmd) redact each output
// line on its own, so each line of a multi-line secret must match alone.
func TestRedactComposeOutput0c9k_StreamedLinesOfMultiLineSecret(t *testing.T) {
	secrets := sm0c9kSecrets(t, "")
	for _, line := range []string{"  " + sm0c9kLine1, sm0c9kLine2 + " host=" + sm0c9kVisible} {
		out := redactComposeOutput(line, secrets)
		assert.NotContains(t, out, sm0c9kLine1)
		assert.NotContains(t, out, sm0c9kLine2)
		assert.Contains(t, out, userinfoPlaceholder)
	}
	assert.Equal(t, "host="+sm0c9kVisible, redactComposeOutput("host="+sm0c9kVisible, secrets))
}

// The locked compose view: a multi-line secret shown under a key that is not
// sensitive is masked on every line it touches, with the stack's list.
func TestMaskSecrets0c9k_LockedComposeViewMultiLineSecret(t *testing.T) {
	secrets := sm0c9kSecrets(t, "BROKEN=\"never closed\n")
	compose := "services:\n  app:\n    command: |\n      " + sm0c9kLine1 + "\n      " + sm0c9kLine2 +
		"\n    environment:\n      DB_HOST: " + sm0c9kVisible + "\n      NOTE: " + sm0c9kColon + "\n"
	out, n := MaskSecrets(compose, "compose.yaml", secrets)
	for _, leak := range []string{sm0c9kLine1, sm0c9kLine2, sm0c9kColon} {
		assert.NotContains(t, out, leak)
	}
	assert.Contains(t, out, sm0c9kVisible)
	assert.Positive(t, n)
	assert.Equal(t, strings.Count(compose, "\n"), strings.Count(out, "\n"), "the line count changed")
}

// A caller's own list may hold a multi-line secret: MaskSecrets masks each of
// its lines instead of skipping it.
func TestMaskSecrets0c9k_MultiLineSecretInCallerList(t *testing.T) {
	text := "x\n" + sm0c9kLine1 + "\n    " + sm0c9kLine2 + "\ny"
	out, n := MaskSecrets(text, "notes.txt", []string{sm0c9kLine1 + "\n" + sm0c9kLine2})
	assert.Equal(t, "x\n"+secretMask+"\n    "+secretMask+"\ny", out)
	assert.Equal(t, 2, n)
}

// Review S1: the multi-line reading must ADD to the line-by-line one, never
// replace it. An unterminated quote, or a value under a ":" key that holds
// "=", lost values the base splitter found.
func TestSensitiveEnvValues0c9kS1_UnionWithLineReading(t *testing.T) {
	cases := map[string]struct{ env, want string }{
		"unterminated = quote, later plain":  {"DB_PASSWORD=\"abc\nAPI_TOKEN=xyz12345\n", "xyz12345"},
		"unterminated = quote, later quoted": {"DB_PASSWORD=\"abc\nAPI_TOKEN=\"xyz12345\"\n", "xyz12345"},
		"unterminated : quote, later plain":  {"DB_PASSWORD: \"abc\nAPI_TOKEN=xyz12345\n", "xyz12345"},
		": key whose value holds =":          {"DB_PASSWORD: abcd=efghijkl\n", "efghijkl"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			svc, stack := stubbedStatusService(t)
			require.NoError(t, os.WriteFile(filepath.Join(stack.Directory, ".env"), []byte(c.env), 0o600))
			stack.EnvFile = ".env"
			secrets := svc.composeSecrets(stack)
			out := redactComposeOutput("value "+c.want+" end", secrets)
			assert.Equal(t, "value *** end", out, "redactComposeOutput")
			masked, _ := MaskSecrets("note: value "+c.want+" end\n", "notes.txt", secrets)
			assert.NotContains(t, masked, c.want, "MaskSecrets")
		})
	}
}

// Review S3: a multi-line value only compose-go reads (a single-quoted value
// whose \' does not close it), in a stack .env beside a broken global.env.
// Only the per-file compose-go pass learns it, and only listing its lines
// lets a streamed line match.
const sm0c9kS3Env = "DB_PASSWORD='l1-abcd\\'\nl2-efgh'\n"

func sm0c9kS3Secrets(t *testing.T) []string {
	t.Helper()
	svc, stack := stubbedStatusService(t)
	require.NoError(t, os.WriteFile(filepath.Join(svc.config.DataDir, "global.env"), []byte("BROKEN=\"never closed\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(stack.Directory, ".env"), []byte(sm0c9kS3Env), 0o600))
	stack.EnvFile = ".env"
	return svc.composeSecrets(stack)
}

func TestMaskSecrets0c9kS3_ValueOnlyComposeGoReads(t *testing.T) {
	out, _ := MaskSecrets("services:\n  app:\n    command: |\n      l2-efgh\n", "compose.yaml", sm0c9kS3Secrets(t))
	assert.NotContains(t, out, "l2-efgh")
}

func TestRedactComposeOutput0c9kS3_StreamedLineOfValueOnlyComposeGoReads(t *testing.T) {
	assert.Equal(t, "app  | *** done", redactComposeOutput("app  | l2-efgh done", sm0c9kS3Secrets(t)))
}
