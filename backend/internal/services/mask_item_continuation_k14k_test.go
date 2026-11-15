package services

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// agent-os-k14k: a plain `- KEY=value` sequence item whose value goes on to
// the next line showed that line to a locked session: the YAML pass masked
// the item's first line only. The continuation is masked like the map form's.
func TestMaskSecrets_k14k_ListItemContinuationMasked(t *testing.T) {
	compose := "services:\n  a:\n    environment:\n      - DB_PASSWORD=plA11zz\n        plB22zz\n      - TZ=UTC\n"
	for name, path := range map[string]string{"compose view": "docker-compose.yml", "log/inspect line pass": ""} {
		out, n := MaskSecrets(compose, path, nil)
		require.NotContains(t, out, "plA11zz", name)
		require.NotContains(t, out, "plB22zz", name)
		require.Contains(t, out, "- TZ=UTC", name+": the next item stays visible")
		require.Equal(t, strings.Count(compose, "\n"), strings.Count(out, "\n"), name)
		require.Positive(t, n, name)
	}

	// The history diff masks each hunk side through the same pass.
	diff := "diff --git a/docker-compose.yml b/docker-compose.yml\n--- a/docker-compose.yml\n+++ b/docker-compose.yml\n@@ -1,3 +1,6 @@\n services:\n   a:\n     environment:\n+      - DB_PASSWORD=plA11zz\n+        plB22zz\n+      - TZ=UTC"
	out := MaskFullContextDiff(diff, nil)
	require.NotContains(t, out, "plB22zz", "history diff")
	require.Contains(t, out, "+      - TZ=UTC", "history diff")
}
