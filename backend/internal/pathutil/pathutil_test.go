package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIsContained_PlainChildAndSelf(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "stack", "compose.yaml")

	ok, err := IsContained(root, child)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("DELIBERATE CI BREAK (agent-os-48o verification): expected %q to be contained in %q", child, root)
	}

	// root itself counts as contained.
	if ok, err := IsContained(root, root); err != nil || !ok {
		t.Fatalf("root should be contained in itself: ok=%v err=%v", ok, err)
	}
}

func TestIsContained_RejectsTraversal(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "..", "evil")

	ok, err := IsContained(root, outside)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected %q to be rejected as outside %q", outside, root)
	}
}

func TestIsContained_RejectsSiblingPrefix(t *testing.T) {
	// /stacks must not contain /stacks-evil (the trailing-separator guard).
	base := t.TempDir()
	root := filepath.Join(base, "stacks")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(base, "stacks-evil", "x")

	ok, err := IsContained(root, sibling)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("sibling-prefix path %q must not be contained in %q", sibling, root)
	}
}

// TestIsContained_RejectsSymlinkEscape is the core regression test (H1/M2):
// a symlink inside root that points outside it must not let a target under that
// symlink pass the containment check.
func TestIsContained_RejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	base := t.TempDir()
	root := filepath.Join(base, "stacks")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}

	// root/escape -> outside
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Lexically root/escape/data is "inside" root, but it really resolves to
	// outside/data and must be rejected.
	target := filepath.Join(link, "data")
	ok, err := IsContained(root, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("symlink-escaping target %q must not be contained in %q", target, root)
	}
}

// TestIsContained_NonexistentTargetUnderRealDir ensures a not-yet-created target
// (e.g. a restore destination or a new env file) is allowed when its existing
// ancestor is genuinely inside root.
func TestIsContained_NonexistentTargetUnderRealDir(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "newstack", "deeper", "file-not-created-yet.env")

	ok, err := IsContained(root, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("nonexistent target under root should be contained: %q in %q", target, root)
	}
}
