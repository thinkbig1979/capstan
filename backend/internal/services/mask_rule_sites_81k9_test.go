package services

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// agent-os-81k9: the line maskers judged a value secret by its key or a URL
// credential only, so a one-line JSON password and an opaque user:pw@host
// credential under an ordinary key showed. Each site now asks
// IsSensitiveEnvVar, the env views' rule. One row per site; each runs through
// the compose view and the log/inspect line pass.
func TestMaskSecrets_81k9_EverySiteUsesTheValueRule(t *testing.T) {
	rows := []struct{ site, text, secret string }{
		{"maskLeadKey opaque", "CONFIG=user:SEKRITb1@host:/p", "SEKRITb1"},
		{"maskLeadKey escaped JSON", `CONFIG={\"password\":\"SEKRITb3\"}`, "SEKRITb3"},
		{"maskLeadKey quoted escaped JSON", `CONFIG="{\"password\":\"SEKRITb2\"}"`, "SEKRITb2"},
		{"maskLeadKey colon JSON", `CONFIG: {"password":"SEKRITb4"}`, "SEKRITb4"},
		{"maskInlineKV opaque", "run CONFIG=user:SEKRITc1@host", "SEKRITc1"},
		{"maskFlowTokens opaque", "x: [CONFIG=user:SEKRITd1@host]", "SEKRITd1"},
		{"maskFlowTokens open quote", `x: [CONFIG="user:SEKRITg1@host`, "SEKRITg1"},
		// Rows only the flow-token site reads: no "=" for maskInlineKV, no
		// key at the line's start for maskLeadKey.
		{"maskFlowTokens colon token", "x: {CONFIG: user:SEKRITd2@host}", "SEKRITd2"},
		{"maskFlowTokens open quote alone", `run [CONFIG="u:SEKRITg2@host`, "SEKRITg2"},
		{"maskStatements opaque", `NOTES="a" CONFIG=user:SEKRITe1@host`, "SEKRITe1"},
		{"maskStatements JSON", `NOTES="a" CONFIG='{\"password\":\"SEKRITe2\"}'`, "SEKRITe2"},
	}
	for _, r := range rows {
		for _, path := range []string{"docker-compose.yml", ".env", ""} {
			out, n := MaskSecrets(r.text, path, nil)
			require.NotContains(t, out, r.secret, "%s, path %q: %q", r.site, path, out)
			require.Positive(t, n, "%s, path %q", r.site, path)
		}
	}
}

// The YAML pass's mapping arm (walkSensitiveYAML) is the list's rule too, so
// a value the view hides under an ordinary key is in the stack's secret list,
// which masks logs and inspect (view and list agree).
func TestWalkSensitiveYAML_81k9_MappingValueRule(t *testing.T) {
	compose := "services:\n  a:\n    environment:\n      CONFIG: user:SEKRITf1@host\n      JSONCFG: '{\"password\":\"SEKRITf2\"}'\n      TZ: UTC\n"
	out, _ := MaskSecrets(compose, "docker-compose.yml", nil)
	require.NotContains(t, out, "SEKRITf1")
	require.NotContains(t, out, "SEKRITf2")
	require.Contains(t, out, "TZ: UTC")
	list := composeYAMLSecretValues([]byte(compose))
	require.Contains(t, list, "user:SEKRITf1@host")
	require.Contains(t, list, `{"password":"SEKRITf2"}`)
	require.NotContains(t, list, "UTC")
}

// envQuotePass counted an open sensitive value twice: once itself and once in
// maskFrom (rv-mask2 INFO).
func TestMaskSecrets_81k9_OpenValueCountedOnce(t *testing.T) {
	_, n := MaskSecrets("NOTES=\"{\\\"password\\\":\\\"x\nSEKRITh1\\\"}\"\n", ".env", nil)
	require.Equal(t, 1, n)
}

// A line pass that masks part of a secret-list value masks all of it: the
// part left no longer matched the needle and showed (found by the superset
// corpus once the sites above read the value rule).
func TestMaskSecrets_81k9_PartialMaskCoversTheWholeNeedle(t *testing.T) {
	line := "IMAGE: user:SEKRITh8@host # OLD_SECRET: 'SEKRITc3'"
	secrets := []string{"user:SEKRITh8@host # OLD_SECRET: 'SEKRITc3'"}
	for _, path := range []string{"docker-compose.yml", ".env", ""} {
		out, _ := MaskSecrets(line, path, secrets)
		require.NotContains(t, out, "SEKRITc3", "path %q: %q", path, out)
		require.NotContains(t, out, "SEKRITh8", "path %q: %q", path, out)
	}
}

// A line inside a sensitive quoted value that runs over lines is the value,
// key-like text included, even when an earlier pass masked part of it.
func TestMaskSecrets_81k9_OpenValueBodyLineMaskedWhole(t *testing.T) {
	out, _ := MaskSecrets("DB_PASSWORD:'a\nIMAGE={\"password\":\"SEKRITi9\"}\nb'\n", ".env", nil)
	require.Equal(t, "***", strings.Split(out, "\n")[1], "%q", out)
}

// A URL credential the YAML pass masks first still marks the quoted value
// before it on the line, as it did before agent-os-81k9 (origLeadValue; the
// superset corpus line, review round 2 F4).
func TestMaskSecrets_81k9_URLCueReadOnTheOriginalLine(t *testing.T) {
	line := `TZ="SEKRIT\"j10" IMAGE: https://p/?url=https%3A%2F%2Fu%3ASEKRITf6%40h`
	out, _ := MaskSecrets(line+"\n", "docker-compose.yml", nil)
	require.NotContains(t, out, "j10", "%q", out)
	require.NotContains(t, out, "SEKRITf6", "%q", out)
}
