package handlers

import "github.com/thinkbig1979/capstan/backend/internal/services"

// globalEnvVarSensitive is the one rule for whether a global env row is a
// secret (agent-os-ruuw): redacted is the row's entry after redactEnvResponse
// and v is the row's unredacted {key, value}. A locked GET blanks the value of
// such a row (redactGlobalEnvVars), and every GET flags it, so the page masks
// it for an unlocked session too.
func globalEnvVarSensitive(redacted EnvEntry, v map[string]string) bool {
	return redacted.Sensitive || services.IsSensitiveEnvVar(v["key"], v["value"])
}
