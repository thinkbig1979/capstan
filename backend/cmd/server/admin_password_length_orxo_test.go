package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The offline reset hashes with bcrypt, which refuses passwords over 72 bytes.
// It must refuse such a password through the shared policy, with the policy's
// message, before it reaches bcrypt (agent-os-orxo).
func TestResetPassword_PasswordOver72Bytes_RefusedByPolicyOrxo(t *testing.T) {
	dataDir, userID := seedInstance(t, "admin")
	before := storedHash(t, dataDir, "admin")

	code, output := runAdmin(dataDir, "Aa1!"+strings.Repeat("x", 69)+"\n")
	require.Equal(t, 1, code, "output: %s", output)
	assert.Contains(t, output, "refusing to set this password: Password must not exceed 72 bytes")
	assert.Equal(t, before, storedHash(t, dataDir, "admin"))
	assert.Equal(t, 2, sessionCount(t, dataDir, userID))

	code, output = runAdmin(dataDir, "Aa1!"+strings.Repeat("x", 68)+"\n")
	require.Equal(t, 0, code, "72 bytes must be accepted; output: %s", output)
}
