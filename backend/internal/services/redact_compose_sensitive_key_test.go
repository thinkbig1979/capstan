package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// agent-os-t61y: IsSensitiveEnvKey matched only the suffixes _KEY, _SECRET,
// _PASSWORD and _TOKEN, so a numbered (DB_PASSWORD2), mid-name (PASSWORD_DB)
// or bare (PASSWORD) secret was shown to a locked session and never learned
// by compose-output redaction. The rule is now by word: see IsSensitiveEnvKey.
func TestSensitiveKey_T61y_NumberedMidNameAndBare(t *testing.T) {
	for _, key := range []string{
		"DB_PASSWORD2", "PASSWORD_DB", "DB_PASSWORD_OLD", "PASSWORD", "SECRET",
		"TOKEN", "KEY", "KEY2", "db.password", "DB-PASSWD", "DBPASSWORD",
		"CLIENTSECRET", "AUTHTOKEN", "MY_APIKEY", "SSH_PRIVATEKEY",
		"export DB_PASSWORD2", "export\tPASSWORD_DB",
		// Masked although harmless: the price of matching by word. Edwin
		// owns this policy; these pin what it costs today.
		"PASSWORD_MIN_LENGTH", "TOKEN_EXPIRY", "AWS_ACCESS_KEY_ID",
	} {
		assert.True(t, IsSensitiveEnvKey(key), "%q must be sensitive", key)
	}
}

// The other side: words that only contain a secret word stay visible, or the
// locked view would hide ordinary configuration.
func TestSensitiveKey_T61y_LookalikesStayVisible(t *testing.T) {
	for _, key := range []string{
		"MONKEY", "HOTKEY", "KEYCLOAK_URL", "TOKENIZER_MODEL", "SECRETARY_NAME",
		"PASSPORT_ID", "PASS_THROUGH", "PWD", "DB_HOST", "PORT", "API_ENDPOINT",
		"export TZ",
	} {
		assert.False(t, IsSensitiveEnvKey(key), "%q must not be sensitive", key)
	}
}
