package dockerenv

import (
	"strings"
	"testing"
)

// TestEnv_OnlyAllowlistedVarsPassThrough proves Env itself filters
// correctly, mirroring the equivalent test that lived in
// internal/services/exec_env_test.go before the allowlist moved here
// (agent-os-3ux). It does not, on its own, prove any call site actually uses
// Env — the call-site tests in internal/handlers and internal/truth do that.
func TestEnv_OnlyAllowlistedVarsPassThrough(t *testing.T) {
	t.Setenv("JWT_SECRET", "sentinel-jwt")
	t.Setenv("STORAGE_KEY", "sentinel-storage")
	t.Setenv("GIT_HTTPS_TOKEN", "sentinel-git-token")
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/home/capstan")
	t.Setenv("DOCKER_HOST", "tcp://remote-daemon:2376")
	t.Setenv("SOME_RANDOM_APP_VAR", "should-not-appear")

	env := Env()
	joined := strings.Join(env, "\n")

	if strings.Contains(joined, "sentinel-jwt") {
		t.Errorf("Env() leaked JWT_SECRET: %v", env)
	}
	if strings.Contains(joined, "sentinel-storage") {
		t.Errorf("Env() leaked STORAGE_KEY: %v", env)
	}
	if strings.Contains(joined, "sentinel-git-token") {
		t.Errorf("Env() leaked GIT_HTTPS_TOKEN: %v", env)
	}
	if strings.Contains(joined, "SOME_RANDOM_APP_VAR") {
		t.Errorf("Env() leaked a non-allowlisted var: %v", env)
	}

	want := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/home/capstan",
		"DOCKER_HOST=tcp://remote-daemon:2376",
	}
	for _, w := range want {
		found := false
		for _, kv := range env {
			if kv == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Env() missing expected entry %q, got: %v", w, env)
		}
	}
}
