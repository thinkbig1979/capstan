package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvBodyLine_9fsu_FlagsDoNotChangeWhatASaveWrites(t *testing.T) {
	// A client echoes the rows it was sent, flags included. sameEnvRow ignores
	// them, so an unchanged row is still written as the file's own text.
	file := (&EnvHandler{}).parseEnvFile(bodyLineFile)
	echoed := make([]EnvEntry, len(file))
	copy(echoed, file)
	for i := range echoed {
		echoed[i].Sensitive = true
		echoed[i].BodyLine = true
	}
	out, err := serializeEnvFileOver(bodyLineFile, echoed)
	require.NoError(t, err)
	assert.Equal(t, bodyLineFile, out)
}
