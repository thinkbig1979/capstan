package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-t61y: a numbered or mid-name password reached a locked env GET
// because IsSensitiveEnvKey matched suffixes only.
func TestEnvLocked_T61y_MasksNumberedAndMidNamePasswords(t *testing.T) {
	const content = "DB_PASSWORD2=secretnumbered\n" +
		"PASSWORD_DB=secretmidname\n" +
		"export DB_PASSWORD_OLD=secretexported\n" +
		"TZ=Europe/Amsterdam\n"
	secrets := []string{"secretnumbered", "secretmidname", "secretexported"}
	router, stackID, token, _ := envQuoteFixture(t, content)

	unlocked := getEnvBody(t, router, stackID, token)
	for _, s := range secrets {
		require.Contains(t, unlocked, s)
	}

	locked := getEnvBody(t, router, stackID, "")
	for _, s := range secrets {
		assertBodyOmits(t, locked, s)
	}
	assert.Contains(t, locked, "Europe/Amsterdam")
}
