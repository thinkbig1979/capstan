package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-fvk3: compose output reached the API response, the action log and
// ActionResult.Details["output"] with no redaction. These tests plant a secret
// in each place compose can learn one (a sensitive key in the stack .env, a
// quoted one in global.env, a credential in a URL) and assert none survives,
// while the diagnostic text around them does.

const (
	fvk3StackSecret  = "hunter2stacksecret"
	fvk3GlobalSecret = "globaltoken99"
	fvk3URLPassword  = "urlpass123"
	fvk3Visible      = "dbhost-visible"
	fvk3Diagnosis    = "ERROR mount /srv/data: no such file or directory"
)

// fvk3Script writes every planted secret to both streams, next to text an
// operator needs, then exits with code.
func fvk3Script(code string) string {
	line := "pulling https://bob:" + fvk3URLPassword + "@registry.example.com/app " +
		"password=" + fvk3StackSecret + " token=" + fvk3GlobalSecret + " host=" + fvk3Visible
	return "printf '%s\\n' '" + line + "'; printf '%s\\n' '" + fvk3Diagnosis + "' >&2; exit " + code
}

func fvk3Service(t *testing.T, script string) (*DockerService, models.Stack) {
	t.Helper()
	svc, stack := stubbedStatusService(t)
	require.NoError(t, os.WriteFile(filepath.Join(stack.Directory, ".env"),
		[]byte("DB_PASSWORD="+fvk3StackSecret+"\nDB_HOST="+fvk3Visible+"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(svc.config.DataDir, "global.env"),
		[]byte(`REGISTRY_TOKEN="`+fvk3GlobalSecret+`"`+"\n"), 0o600))
	stubComposeScript(t, script)
	return svc, stack
}

func assertFvk3Redacted(t *testing.T, label, s string) {
	t.Helper()
	for _, secret := range []string{fvk3StackSecret, fvk3GlobalSecret, fvk3URLPassword} {
		assert.NotContains(t, s, secret, "%s leaks a planted secret", label)
	}
	// The other side: redaction must not cost the operator the diagnosis.
	assert.Contains(t, s, fvk3Diagnosis, "%s lost compose's diagnosis", label)
	assert.Contains(t, s, fvk3Visible, "%s redacted a non-sensitive value", label)
	assert.Contains(t, s, "https://***@registry.example.com/app", "%s lost the URL host", label)
}

func TestVerifiedLifecycle_RedactsComposeOutput(t *testing.T) {
	cases := []struct {
		name string
		code string
		run  func(*DockerService, models.Stack) (truth.ActionResult, string)
	}{
		{"StartVerified", "1", (*DockerService).StartVerified},
		{"StopVerified", "1", (*DockerService).StopVerified},
		{"DeleteVerified", "1", (*DockerService).DeleteVerified},
		{"PullVerified failed", "1", (*DockerService).PullVerified},
		// Pull is the one verb whose success path also carries the output.
		{"PullVerified succeeded", "0", (*DockerService).PullVerified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, stack := fvk3Service(t, fvk3Script(tc.code))
			ar, out := tc.run(svc, stack)

			assertFvk3Redacted(t, "returned output", out)
			detail, ok := ar.Details["output"].(string)
			require.True(t, ok, "Details[output] missing: %#v", ar.Details)
			assertFvk3Redacted(t, "Details[output]", detail)
		})
	}
}

func TestStatus_NonzeroExitCarriesRedactedStderr(t *testing.T) {
	// Everything to stderr: that is the stream Status now surfaces on failure.
	svc, stack := fvk3Service(t, "{ "+fvk3Script("1")+"; } 1>&2")

	_, _, err := svc.Status(stack)
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "docker compose ps failed: exit status 1")
	assertFvk3Redacted(t, "Status error", msg)
}

func TestRedactComposeOutput_Policy(t *testing.T) {
	secrets := sensitiveEnvValues(`
# comment API_KEY=notread
export APP_SECRET='single quoted value'
DB_PASSWORD=plainpass # trailing comment
SHORT_TOKEN=abc
DB_HOST=keepme
`)
	in := "a single quoted value, plainpass, abc, keepme, git+https://u:p@h/r.git /var/lib/x"
	got := redactComposeOutput(in, secrets)
	assert.Equal(t, "a ***, ***, abc, keepme, git+https://***@h/r.git /var/lib/x", got)
}

func TestIsSensitiveEnvKey(t *testing.T) {
	tests := []struct {
		key      string
		expected bool
	}{
		{"API_KEY", true},
		{"DB_PASSWORD", true},
		{"SECRET_TOKEN", true},
		{"AUTH_SECRET", true},
		{"MY_API_KEY", true},
		{"TEST_API_ENDPOINT", true},
		{"api_key", true},
		{"db_password", true},
		{"export DB_PASSWORD", true},
		{"DB_HOST", false},
		{"API_ENDPOINT", false},
		{"PORT", false},
		{"DEBUG", false},
		{"HOSTNAME", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsSensitiveEnvKey(tt.key))
		})
	}
}

// Redaction must run BEFORE trimOutput's 500-byte cut: a secret straddling the
// cut is no longer a whole match, so trimming first leaks its prefix.
func TestStatus_RedactsBeforeTrimming(t *testing.T) {
	pad := strings.Repeat("x", 490)
	svc, stack := fvk3Service(t, "printf '%s\\n' '"+pad+" "+fvk3StackSecret+"' >&2; exit 1")

	_, _, err := svc.Status(stack)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), fvk3StackSecret[:6], "a secret cut by the trim leaked its prefix")
	assert.Contains(t, err.Error(), pad+" ***")
}
