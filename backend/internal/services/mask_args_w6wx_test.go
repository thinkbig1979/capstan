package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MaskArgs: an argv, for container inspect (sec D3).
func TestMaskArgs_w6wx(t *testing.T) {
	got := MaskArgs([]string{"serve", "--db-password=hunter2XYZ", "--token", "tok_live_ABCD", "-p", "8080", "CONN=x-verbatimSECRET-y", "sh -c 'app --password abcdef12'"},
		[]string{"verbatimSECRET"})
	require.Len(t, got, 8)
	assert.Equal(t, []string{"serve", "--db-password=***", "--token", "***", "-p", "8080", "CONN=x-***-y", "sh -c 'app --password ***'"}, got)
	assert.Nil(t, MaskArgs(nil, nil))
	// A flag right after a sensitive flag is not its value; the value after
	// it is masked.
	assert.Equal(t, []string{"app", "--token", "--password", "***"}, MaskArgs([]string{"app", "--token", "--password", "x9y8z7"}, nil))
}
