package handlers

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
)

func TestValidateStackPath_AllowsPathInsideRoot(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{StacksDir: root}

	inside := filepath.Join(root, "myapp", "compose.yaml")
	if err := validateStackPath(inside, cfg); err != nil {
		t.Fatalf("expected path inside root to be allowed, got %v", err)
	}
}

func TestValidateStackPath_RejectsTraversal(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{StacksDir: root}

	outside := filepath.Join(root, "..", "etc", "passwd")
	if err := validateStackPath(outside, cfg); err == nil {
		t.Fatalf("expected traversal path %q to be rejected", outside)
	}
}

// TestValidateStackPath_RejectsSymlinkEscape is the regression test for M2: a
// symlink inside the stacks root pointing outside it must not let a compose/env
// read or write follow the symlink to a host file.
func TestValidateStackPath_RejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	base := t.TempDir()
	root := filepath.Join(base, "stacks")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{root, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// stacks/escape -> outside
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	cfg := &config.Config{StacksDir: root}
	// Lexically "inside" root, but really resolves to outside/.env.
	target := filepath.Join(root, "escape", ".env")
	if err := validateStackPath(target, cfg); err == nil {
		t.Fatalf("symlink-escaping path %q must be rejected", target)
	}
}
