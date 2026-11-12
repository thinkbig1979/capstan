package services

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-1106: redaction and host space resolve a stack's compose env with
// one helper, resolveComposeEnv, in two documented modes. These pins go
// through the CALLERS, composeSecrets and composeStackReferences, so a caller
// handed the other mode goes red; a test of the helper alone could not see
// that.

// The resolved value, "abcdwxyz", is only learned by resolving .env: the raw
// fallback parser sees "${PART}wxyz". So the superset mode's .env-beside-an-
// EnvFile arm is what masks it.
func TestComposeEnv_1106_RedactionReadsDotEnvBesideEnvFile(t *testing.T) {
	svc, stack := stubbedStatusService(t)
	require.NoError(t, os.WriteFile(filepath.Join(stack.Directory, ".env"),
		[]byte("PART=abcd\nDB_PASSWORD=${PART}wxyz\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(stack.Directory, "app.env"),
		[]byte("APP_TOKEN=apptokenvalue\n"), 0o600))
	stack.EnvFile = "app.env"

	secrets := svc.composeSecrets(stack)
	assert.Contains(t, secrets, "abcdwxyz", "redaction must resolve .env even when an EnvFile is set")
	assert.Contains(t, secrets, "apptokenvalue")
}

// Host space reads exactly what compose reads: with an EnvFile passed as
// --env-file, compose does not read .env, so .env must not decide a name.
func TestComposeEnv_1106_HostSpaceIgnoresDotEnvBesideEnvFile(t *testing.T) {
	shop := writeStack(t, "shop", map[string]string{
		"compose.yaml": "services:\n  db:\n    image: x\nvolumes:\n  data:\n    name: ${VOL:?set it}\n",
		".env":         "VOL=fromdotenv\n",
		"app.env":      "VOL=fromenvfile\n",
	})
	shop.EnvFile = "app.env"

	refs := composeStackReferences("")(context.Background(), []models.Stack{shop})
	assert.Empty(t, refs.withheld)
	assert.True(t, refs.volumes["fromenvfile"], "the name comes from the EnvFile compose is handed")
	assert.False(t, refs.volumes["fromdotenv"], ".env is not read when an --env-file is passed")
}

// A file compose-go rejects: host space withholds the stack (exact fails),
// redaction still learns what was resolved before the fault (superset keeps
// the partial map).
func TestComposeEnv_1106_FaultWithholdsHostSpaceButRedactionKeepsPartial(t *testing.T) {
	const broken = "PART=abcd\nDB_PASSWORD=${PART}wxyz\nNAME=\"unterminated\n"

	shop := writeStack(t, "shop", map[string]string{
		"compose.yaml": "services:\n  db:\n    image: x\n",
		"app.env":      broken,
	})
	shop.EnvFile = "app.env"
	refs := composeStackReferences("")(context.Background(), []models.Stack{shop})
	assert.Equal(t, []string{"shop"}, refs.withheld, "an env file compose cannot read withholds the stack")

	svc, stack := stubbedStatusService(t)
	require.NoError(t, os.WriteFile(filepath.Join(stack.Directory, "app.env"), []byte(broken), 0o600))
	stack.EnvFile = "app.env"
	assert.Contains(t, svc.composeSecrets(stack), "abcdwxyz", "the values resolved before the fault are still masked")
}
