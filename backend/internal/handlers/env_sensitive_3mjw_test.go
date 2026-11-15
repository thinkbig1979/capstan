package handlers

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-3mjw: a one-line JSON value holding a password or private key, and
// an opaque URL credential (sftp:user:pw@host, user:pw@host), were neither
// flagged nor blanked in the locked stack view or the global env page.

const envSecretShapes3mjw = `JSONCFG={"password":"SEKRIT3mjwA"}
GCP={"type":"service_account","private_key":"SEKRIT3mjwB"}
BACKUP=sftp:user:SEKRIT3mjwC@host
REMOTE=user:SEKRIT3mjwD@host
QUOTED="ftp:bob:SEKRIT3mjwE@nas.local:/share"
NOTES="a" MIRROR=user:SEKRIT3mjwF@host
`

func TestRedactEnvResponse_3mjw_JSONAndOpaqueCredentialsBlanked(t *testing.T) {
	resp := EnvResponse{Raw: envSecretShapes3mjw, Entries: (&EnvHandler{}).parseEnvFile(envSecretShapes3mjw)}
	redactEnvResponse(&resp)
	seen := 0
	for _, e := range resp.Entries {
		if e.Key == "" {
			continue
		}
		seen++
		assert.True(t, e.Sensitive, "row %q is flagged", e.Key)
		assert.NotContains(t, e.Value+e.Quote+e.InlineComment, "SEKRIT", "row %q is blanked", e.Key)
	}
	require.Equal(t, 6, seen)
}

func TestRedactGlobalEnvVars_3mjw_JSONAndOpaqueCredentialsBlanked(t *testing.T) {
	vars, _ := globalEnvVars((&EnvHandler{}).parseEnvFile(envSecretShapes3mjw))
	require.Len(t, vars, 6)
	redactGlobalEnvVars(vars, envSecretShapes3mjw)
	for _, v := range vars {
		assert.NotEmpty(t, v["key"])
		assert.NotContains(t, v["value"], "SEKRIT", "row %q", v["key"])
	}
}

// What the detector must leave visible: an image pinned by digest (its
// "name:tag@sha256:..." is the user:pw@host shape), a plain host:port, and
// JSON with no sensitive field.
func TestRedactEnvResponse_3mjw_LookalikesStayVisible(t *testing.T) {
	raw := strings.Join([]string{
		"IMAGE=nginx:1.25@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"DIGEST=registry.local:5000/app:2@sha512:abcdef",
		"DB_HOST=db.local:5432",
		`LABELS={"tier":"web","team":"ops"}`,
		"ADMIN=admin@example.com",
	}, "\n") + "\n"
	resp := EnvResponse{Raw: raw, Entries: (&EnvHandler{}).parseEnvFile(raw)}
	redactEnvResponse(&resp)
	for _, e := range resp.Entries {
		if e.Key == "" {
			continue
		}
		assert.False(t, e.Sensitive, "row %q", e.Key)
		assert.NotEmpty(t, e.Value, "row %q", e.Key)
	}
}
