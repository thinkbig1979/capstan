package services

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-vbkt: LintEnv.secretValues added a multi-line sensitive value
// whole, so a lint message that echoes one line of it showed that line. The
// stack's own files reach the masker line by line (StackSecretValues, since
// agent-os-0c9k); a value known only resolved, in Vars, did not.

const pemLineVbkt = "MIIEvQIBADANBgkqhkiG9w0BAQEFAASCVBKTsecret"

// lintMessagesVbkt lints compose content that spells out one line of the
// secret, where compose-go quotes it back (cpus does not decode it).
func lintMessagesVbkt(t *testing.T, env LintEnv) []string {
	t.Helper()
	compose := "services:\n  db:\n    image: postgres:16\n    cpus: " + pemLineVbkt + "\n"
	results, err := NewLinterService().LintCompose(compose, t.TempDir(), env, true)
	require.NoError(t, err)
	var msgs []string
	for _, r := range results {
		msgs = append(msgs, r.Message)
	}
	require.NotEmpty(t, msgs)
	return msgs
}

func assertLineMaskedVbkt(t *testing.T, msgs []string) {
	t.Helper()
	joined := strings.Join(msgs, "\n")
	assert.NotContains(t, joined, pemLineVbkt, "a line of the multi-line secret is shown")
	assert.Contains(t, joined, "***", "the message no longer quotes the value: %q", joined)
}

// A multi-line sensitive value present only in Vars (as the process
// environment or a resolved value reaches it).
func TestLintComposeVbkt_MultiLineVarsSecretMaskedPerLine(t *testing.T) {
	pem := "-----BEGIN PRIVATE KEY-----\n" + pemLineVbkt + "\n-----END PRIVATE KEY-----"
	assertLineMaskedVbkt(t, lintMessagesVbkt(t, LintEnv{Vars: map[string]string{"TLS_PRIVATE_KEY": pem}}))
}

// The production route for a request's env text: a PEM written on one line
// with \n escapes, which dotenv expands into a multi-line value. The raw-text
// reader (sensitiveEnvValues) sees one line, so only the resolved value holds
// the lines.
func TestLintComposeVbkt_EscapedNewlineEnvTextMaskedPerLine(t *testing.T) {
	envText := `TLS_PRIVATE_KEY="-----BEGIN PRIVATE KEY-----\n` + pemLineVbkt + `\n-----END PRIVATE KEY-----"` + "\n"
	env := NewLintEnv("", envText)
	require.Contains(t, env.Vars["TLS_PRIVATE_KEY"], "\n"+pemLineVbkt+"\n", "dotenv expands the escapes")
	assertLineMaskedVbkt(t, lintMessagesVbkt(t, env))
}

// Superset over overlap fixtures (agent-os-buwq shape included): the lint
// output hides at least what the single whole-value pass hid. base is the
// base SHA's redaction, recomputed here from the raw message (a lint run with
// no secrets); every text the output shows must appear, in order, in base.
// Uses only functions present before agent-os-vbkt, so it runs on both SHAs.
func TestLintComposeVbkt_OutputHidesSupersetOfWholeValuePass(t *testing.T) {
	cases := []struct {
		name, literal string
		vars          map[string]string
		secrets       []string
	}{
		{"buwq overlap: a line and a one-line secret", "abcdefgh",
			map[string]string{"TLS_KEY": "xxxx\nabcdef\nyyyy"}, []string{"defgh"}},
		{"a line that holds a one-line secret", "AAAAsecretBBBB",
			map[string]string{"TLS_KEY": "xxxx\nAAAAsecretBBBB\nyyyy"}, []string{"secret"}},
		{"two lines overlapping", "abcdefghij",
			map[string]string{"TLS_KEY": "abcdefgh\nxxxx", "SSH_KEY": "efghij\nyyyy"}, nil},
		{"a line echoed alone", pemLineVbkt,
			map[string]string{"TLS_KEY": "-----BEGIN-----\n" + pemLineVbkt + "\n-----END-----"}, nil},
		{"a whole one-line secret", "plainSECRETvalue",
			map[string]string{"DB_PASSWORD": "plainSECRETvalue"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			compose := "services:\n  db:\n    image: postgres:16\n    cpus: " + c.literal + "\n"
			lint := func(env LintEnv) string {
				results, err := NewLinterService().LintCompose(compose, t.TempDir(), env, true)
				require.NoError(t, err)
				var msgs []string
				for _, r := range results {
					msgs = append(msgs, r.Message)
				}
				return strings.Join(msgs, "\n")
			}
			raw := lint(LintEnv{Vars: map[string]string{}})
			require.Contains(t, raw, c.literal, "the raw message echoes the literal")
			env := LintEnv{Vars: c.vars, Secrets: c.secrets}
			base := redactComposeOutput(raw, env.secretValues())
			got := lint(env)
			t.Logf("base=%q got=%q", base, got)
			rest := base
			for _, seg := range strings.Split(got, userinfoPlaceholder) {
				i := strings.Index(rest, seg)
				require.GreaterOrEqual(t, i, 0, "output shows %q, which the whole-value pass hid: base=%q got=%q", seg, base, got)
				rest = rest[i+len(seg):]
			}
		})
	}
}
