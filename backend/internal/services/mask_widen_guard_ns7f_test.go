package services

import (
	_ "embed"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

//go:embed mask_secret_lines.go
var maskSecretLinesSrc string

// agent-os-ns7f guard: every mask widened to a needle goes through
// widenForMask, which hides the rest of the line when a needle runs on into
// the next token. widenToRuns is its definition plus that one call.
func TestMaskSecretLines_ns7f_WideningOnlyThroughWidenForMask(t *testing.T) {
	assert.Equal(t, 2, strings.Count(maskSecretLinesSrc, "widenToRuns("), "a widenToRuns call outside widenForMask")
}
