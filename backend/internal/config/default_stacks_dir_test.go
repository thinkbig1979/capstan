package config

import (
	"os"
	"path/filepath"
	"testing"
)

func mkRoots(t *testing.T, names ...string) []string {
	t.Helper()
	base := t.TempDir()
	roots := make([]string, 0, len(names))
	for _, n := range names {
		dir := filepath.Join(base, n)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		roots = append(roots, dir)
	}
	return roots
}

// agent-os-a1ye.6 (b): a persisted default naming an extra root becomes the
// primary root at boot, and the env STACKS_DIR root stays in the set.
func TestApplyPersistedDefaultStacksDir_ReordersRoots(t *testing.T) {
	roots := mkRoots(t, "stacks", "more", "other")
	env, extra, other := roots[0], roots[1], roots[2]
	cfg := &Config{StacksDir: env, ExtraStacksDirs: []string{extra, other}}

	ApplyPersistedDefaultStacksDir(cfg, extra)

	if cfg.StacksDir != extra {
		t.Fatalf("StacksDir = %q, want the persisted %q", cfg.StacksDir, extra)
	}
	got := cfg.GetAllStacksDirs()
	want := []string{extra, env, other}
	if len(got) != len(want) {
		t.Fatalf("roots = %v, want %v (same set, no duplicates)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("roots = %v, want %v", got, want)
		}
	}
}

// A persisted value reached through a symlink still matches its root, and is
// stored back as the configured spelling.
func TestApplyPersistedDefaultStacksDir_MatchesThroughSymlink(t *testing.T) {
	roots := mkRoots(t, "stacks", "more")
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(roots[1], link); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{StacksDir: roots[0], ExtraStacksDirs: []string{roots[1]}}

	ApplyPersistedDefaultStacksDir(cfg, link)

	if cfg.StacksDir != roots[1] {
		t.Fatalf("StacksDir = %q, want %q", cfg.StacksDir, roots[1])
	}
}

// Values that must leave the env configuration untouched: empty (the seed),
// the current primary, a subdirectory of a root, and a path outside every root
// (the env changed since the value was saved).
func TestApplyPersistedDefaultStacksDir_KeepsEnv(t *testing.T) {
	roots := mkRoots(t, "stacks", "more")
	cases := map[string]string{
		"empty":         "",
		"current":       roots[0],
		"subdirectory":  filepath.Join(roots[1], "sub"),
		"outside roots": t.TempDir(),
	}
	for name, persisted := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := &Config{StacksDir: roots[0], ExtraStacksDirs: []string{roots[1]}}
			ApplyPersistedDefaultStacksDir(cfg, persisted)
			if cfg.StacksDir != roots[0] || len(cfg.ExtraStacksDirs) != 1 || cfg.ExtraStacksDirs[0] != roots[1] {
				t.Fatalf("config changed to %v", cfg.GetAllStacksDirs())
			}
		})
	}
}
