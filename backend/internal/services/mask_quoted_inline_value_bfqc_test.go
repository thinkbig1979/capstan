package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// agent-os-bfqc: a quoted value was read up to its first stop, and the
// opening quote is one, so --requirepass='pw' masked nothing anywhere, a
// quoted KEY="v" token in a compose command showed (only the env-file tail
// pass covered it on the other paths), and --password "a b" masked only "a".
// The body of a quoted value is masked to its closing quote, or to the
// line's end when it does not close; its quotes stay.
func TestMaskSecrets_bfqc_QuotedValueAfterSensitiveKey(t *testing.T) {
	cases := []struct{ line, secret, keep string }{
		{`x --requirepass='SEKf1' y`, "SEKf1", `--requirepass='***' y`},
		{`x --requirepass="SEKf2" y`, "SEKf2", `--requirepass="***" y`},
		{`x --db-password="SEKf5"`, "SEKf5", `--db-password="***"`},
		{`x --password="SEKf6 two" y`, "two", `--password="***" y`},
		{`x --requirepass="SEKf7 open`, "SEKf7", `--requirepass="***`},
		{`x DB_PASSWORD="SEKf8 two" y`, "two", `DB_PASSWORD="***" y`},
		{`x --api-key='SEKf12'`, "SEKf12", `--api-key='***'`},
		{`x --password "a b SEKf10" y`, "SEKf10", `--password "***" y`},
		{`x --requirepass 'a SEKf13' y`, "SEKf13", `--requirepass '***' y`},
	}
	for _, c := range cases {
		for _, path := range []string{"", ".env", "docker-compose.yml"} {
			text := c.line
			if path == "docker-compose.yml" {
				text = "services:\n  a:\n    command: " + c.line
			}
			out, _ := MaskSecrets(text, path, nil)
			assert.NotContains(t, out, c.secret, "path %q", path)
			assert.Contains(t, out, c.keep, "path %q", path)
		}
	}
}

// bfqc controls: a quoted value under an ordinary key or flag, and a
// reference in quotes, stay as they are.
func TestMaskSecrets_bfqc_QuotedOrdinaryValueUnchanged(t *testing.T) {
	for _, line := range []string{`x --name="web 1" y`, `x TZ='Europe/Amsterdam' y`, `x --password="${DB_PW}" y`, `x --log-level "debug info" y`} {
		for _, path := range []string{"", ".env"} {
			out, n := MaskSecrets(line, path, nil)
			assert.Equal(t, line, out, "path %q", path)
			assert.Equal(t, 0, n, "path %q", path)
		}
	}
}
