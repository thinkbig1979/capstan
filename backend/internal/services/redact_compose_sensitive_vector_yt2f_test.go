package services

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-yt2f: the frontend kept its own copies of the secret-name rule and
// both differed from this one. testdata/sensitive_env_keys.json is the one
// vector both sides are tested against; the frontend half is
// sensitiveKey.yt2f.test.ts.
func TestSensitiveKey_Yt2f_SharedVector(t *testing.T) {
	data, err := os.ReadFile("testdata/sensitive_env_keys.json")
	require.NoError(t, err)
	var vector struct {
		Sensitive    []string `json:"sensitive"`
		NotSensitive []string `json:"notSensitive"`
	}
	require.NoError(t, json.Unmarshal(data, &vector))
	require.NotEmpty(t, vector.Sensitive)
	require.NotEmpty(t, vector.NotSensitive)

	for _, k := range vector.Sensitive {
		assert.True(t, IsSensitiveEnvKey(k), "%q must be sensitive", k)
	}
	for _, k := range vector.NotSensitive {
		assert.False(t, IsSensitiveEnvKey(k), "%q must not be sensitive", k)
	}
}

// A value carrying a URL credential is sensitive whatever its key is called.
func TestSensitiveVar_Yt2f_URLUserinfo(t *testing.T) {
	for _, v := range []string{
		"postgres://u:pw@db/app", "redis://:pw@cache:6379", "https://ghp_token@github.com/o/r.git",
		"jdbc:mysql://u:pw@h/db",
	} {
		assert.True(t, IsSensitiveEnvVar("DATABASE_URL", v), "%q carries a credential", v)
	}
	for _, v := range []string{
		"postgres://db/app", "https://example.com/a@b", "user@example.com", "Europe/Amsterdam", "",
	} {
		assert.False(t, IsSensitiveEnvVar("DATABASE_URL", v), "%q carries no credential", v)
	}
}

// The frontend test cannot read backend/ (vitest's fs.allow), so it reads its
// own copy of the vector. The copies must stay identical, or the two rules
// are tested against different cases.
func TestSensitiveKey_Yt2f_VectorCopiesMatch(t *testing.T) {
	backendCopy, err := os.ReadFile("testdata/sensitive_env_keys.json")
	require.NoError(t, err)
	frontendCopy, err := os.ReadFile("../../../frontend/src/components/stack/__tests__/sensitive_env_keys.json")
	require.NoError(t, err)
	assert.Equal(t, string(backendCopy), string(frontendCopy),
		"copy backend/internal/services/testdata/sensitive_env_keys.json over the frontend copy")
}
