package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A client echoes the rows it was sent, flags included, and a row equal to the
// file's own is written as the file's text byte for byte (sameEnvRow). The
// flags are not part of that comparison, so an echoed row still matches. A
// fixture only proves it when a rewrite would differ from the file, so each
// one carries an indented line (serializeEnvFile trims it) and the test checks
// that first. Both shapes the walk flags are here: the PEM body rows, and the
// #755 opener whose closing line carries a second statement (openerLeakFile).
func TestEnvBodyLine_9fsu_FlagsDoNotChangeWhatASaveWrites(t *testing.T) {
	for _, c := range []struct{ name, content string }{
		{"PEM body rows", "  PORT=8080\n" + bodyLineFile},
		{"opener with a second statement", "  PORT=8080\n" + openerLeakFile},
	} {
		t.Run(c.name, func(t *testing.T) {
			file := (&EnvHandler{}).parseEnvFile(c.content)
			// Control: a plain rewrite of these rows is NOT the file, so only the
			// verbatim path can return it.
			require.NotEqual(t, c.content, serializeEnvFile(file))

			marked := make([]EnvEntry, len(file))
			copy(marked, file)
			markMultilineBodies(marked, c.content)
			flagged := 0
			for i := range marked {
				if marked[i].Sensitive != file[i].Sensitive || marked[i].BodyLine {
					flagged++
				}
			}
			require.Positive(t, flagged, "the walk flags something in this fixture")

			everything := make([]EnvEntry, len(file))
			copy(everything, file)
			for i := range everything {
				everything[i].Sensitive = true
				everything[i].BodyLine = true
			}
			for name, rows := range map[string][]EnvEntry{"as the GET marks them": marked, "every flag set": everything} {
				out, err := serializeEnvFileOver(c.content, rows)
				require.NoError(t, err, name)
				assert.Equal(t, c.content, out, name)
			}
		})
	}
}
