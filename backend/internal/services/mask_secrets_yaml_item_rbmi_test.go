package services

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The YAML pass masks a sensitive `KEY=value` list item whole, so a value
// that the item's scalar carries onto its next line is hidden too. The line
// pass reads only the first line of a block scalar, so without the YAML
// pass's item rule foldB22zz shows (agent-os-rbmi round-2 review F8: no view
// test pinned that rule).
func TestMaskSecrets_YAMLBlockScalarKeyValueItemMaskedWhole(t *testing.T) {
	for _, header := range []string{">-", "|"} {
		in := "services:\n  a:\n    environment:\n      - " + header + "\n        DB_PASSWORD=foldA11zz\n        foldB22zz\n"
		out, _ := MaskSecrets(in, "compose.yaml", nil)
		require.False(t, strings.Contains(out, "foldA11zz") || strings.Contains(out, "foldB22zz"),
			"block scalar %q item leaked:\n%s", header, out)
	}
}
