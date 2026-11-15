package services

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-wlfc: nested URL credentials in the other one-level encodings,
// and a "scheme://" inside a password, through every free-text consumer: the
// compose output/lint redactor (RedactURLUserinfoInText), the locked compose
// view and the locked log line (MaskSecrets).
func TestRedactNestedURL_wlfc_Shapes(t *testing.T) {
	rows := []struct{ name, text, secret, keep string }{
		{"Python quote()", "see https://p/?u=https%3A//user%3ASEKRITw1%40host/x ok", "SEKRITw1", "host/x ok"},
		{"Go PathEscape", "see https://p/?u=https:%2F%2Fuser:SEKRITw2@host%2Fx ok", "SEKRITw2", "host%2Fx ok"},
		{"mixed slashes", "see https://p/?u=https:/%2Fuser:SEKRITw3@host&v=1", "SEKRITw3", "&v=1"},
		{"scheme inside a password", "x https://p/?u=https://user:SEKRITpart://rest@host/x y", "SEKRITpart", "host/x y"},
	}
	for _, r := range rows {
		got := RedactURLUserinfoInText(r.text)
		assert.NotContains(t, got, r.secret, "%s, RedactURLUserinfoInText: %q", r.name, got)
		assert.Contains(t, got, r.keep, "%s, RedactURLUserinfoInText: %q", r.name, got)
		assert.True(t, HasURLUserinfo(r.text), r.name)
		for _, path := range []string{"docker-compose.yml", ""} {
			out, _ := MaskSecrets(r.text, path, nil)
			assert.NotContains(t, out, r.secret, "%s, MaskSecrets %q: %q", r.name, path, out)
		}
	}
	// Unchanged where nothing is a credential: a plain encoded URL, and
	// text after an encoded ":" that is no URL.
	for _, s := range []string{"https://p/?u=https%3A//host/x", "https://p/?u=a:%2Fb@c", "https://p/?q=%3a%2f%2fhost"} {
		require.Equal(t, s, RedactURLUserinfoInText(s))
	}
}

// A JSON field name written with \u escapes is read decoded. The escapes are
// built from parts so no tool between the editor and the file decodes them.
func TestHasSensitiveJSONField_wlfc_UnicodeEscapedName(t *testing.T) {
	u := `\` + "u00"
	escPassword := u + "70assword" // "password", its p escaped
	escName := u + "6eame"         // "name", its n escaped
	require.Len(t, escPassword, 13)
	require.True(t, IsSensitiveEnvVar("CONFIG", `{"`+escPassword+`":"x"}`))
	require.True(t, IsSensitiveEnvVar("CONFIG", `{\"`+escPassword+`\":\"x\"}`))
	require.False(t, IsSensitiveEnvVar("CONFIG", `{"`+escName+`":"x"}`))
	out, _ := MaskSecrets(`CONFIG={"`+escPassword+`":"SEKRITu1"}`, "", nil)
	require.NotContains(t, out, "SEKRITu1")
}

// The log, error and display sites star a nested credential too; the
// settings fields written back (the repository and remote inputs) do not
// (agent-os-zzhs lockout): RedactURLUserinfo itself is unchanged.
func TestRedactURLForDisplay_wlfc_NestedCredential(t *testing.T) {
	repo := "rest:https://proxy/?u=https://u:SEKRITr1@host/x"
	require.NotContains(t, redactURLForDisplay(repo), "SEKRITr1")
	require.Equal(t, RedactURLUserinfo("s3:https://KEY:SEC@host/b"), redactURLForDisplay("s3:https://KEY:SEC@host/b"))
	require.Equal(t, repo, RedactURLUserinfo(repo), "the write-back form stays as it was")

	err := redactErrPath(errors.New("mkdir "+repo+": denied"), repo)
	require.NotContains(t, err.Error(), "SEKRITr1")
	require.True(t, strings.HasPrefix(err.Error(), "mkdir rest:https://proxy/"))

	require.NotContains(t, rcloneTargetForDisplay("https://p/?u=https://u:SEKRITr2@h", "path"), "SEKRITr2")
}
