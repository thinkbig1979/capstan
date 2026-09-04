//go:build integration

// Package integrationtest provides helpers for running integration tests
// against a real local Docker daemon. All files in this package carry the
// "integration" build tag so that normal `go build ./...` and `go test ./...`
// compilations are completely unaffected — the package compiles to nothing
// unless -tags=integration is passed.
//
// Runner requirements:
//   - `docker` CLI in PATH (must be able to execute `docker info`)
//   - `docker compose` sub-command available (Compose v2 plugin, not the
//     legacy `docker-compose` binary — matches what the production code uses)
//   - `docker buildx` available for PullPinnedImage / ImageRepoDigests
//   - The test runner must have pull access to docker.io/library/alpine
//     (used as the lightweight test image; no other registry is required)
//
// These conditions are met on standard ubuntu-latest GitHub Actions runners
// and on any developer machine with Docker Desktop or Docker Engine installed.
package integrationtest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// RequireDocker skips t if the Docker daemon is unavailable or the docker CLI
// cannot be found in PATH. It also verifies that `docker compose` (Compose v2
// plugin) is present, since the harness helpers rely on it.
//
// Call this at the top of every integration test function (or in TestMain) to
// produce a clear, actionable skip message instead of an obscure failure.
func RequireDocker(t *testing.T) {
	t.Helper()

	// 1. CLI presence
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not found in PATH — skipping integration test")
	}

	// 2. Daemon reachability (10 s timeout)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("docker daemon not available (docker info: %v — %s) — skipping integration test",
			err, strings.TrimSpace(string(out)))
	}

	// 3. Compose v2 plugin
	cmd2 := exec.CommandContext(ctx, "docker", "compose", "version")
	if err := cmd2.Run(); err != nil {
		t.Skip("docker compose (v2 plugin) not available — skipping integration test")
	}
}

// NewTempStack writes composeYAML to a temporary directory as compose.yaml
// and derives a deterministic-unique project name from the test name.
//
// It registers a cleanup func via t.Cleanup that runs
// `docker compose -p <project> down -v --remove-orphans` followed by
// directory removal. The returned cleanup func may also be called explicitly
// (e.g. to tear down mid-test); subsequent calls are no-ops.
//
//	dir, project, cleanup := NewTempStack(t, yaml)
//	defer cleanup() // belt-and-suspenders; t.Cleanup also runs it
func NewTempStack(t *testing.T, composeYAML string) (dir string, project string, cleanup func()) {
	t.Helper()

	dir = t.TempDir() // t.Cleanup removes the directory when the test ends

	composePath := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(composeYAML), 0o600); err != nil {
		t.Fatalf("NewTempStack: write compose.yaml: %v", err)
	}

	// Build a project name that is:
	//   - unique per test AND per call within a test (avoids collisions when
	//     a single test calls NewTempStack more than once, and when tests
	//     run in parallel)
	//   - valid for Docker Compose (lowercase alphanumeric + hyphens)
	//   - short enough to stay under container-name limits (keep to ≤40 chars
	//     so the combined "<project>-<service>-1" stays under 63 chars)
	project = uniqueProjectName("it-"+t.Name(), nextStackSeq(t.Name()))

	var once sync.Once
	cleanup = func() {
		once.Do(func() {
			// Best-effort: bring the stack down and remove its volumes.
			// Ignore errors because the stack may already be stopped or the
			// test may have torn it down deliberately.
			downCmd := exec.Command(
				"docker", "compose",
				"-p", project,
				"-f", composePath,
				"down", "-v", "--remove-orphans",
			)
			downCmd.Dir = dir
			_ = downCmd.Run()
		})
	}

	t.Cleanup(cleanup)
	return dir, project, cleanup
}

// RunCompose executes `docker compose -p <project> -f <dir>/compose.yaml <args...>`
// with dir as the working directory. It fails the test if the command exits
// non-zero. Combined stdout+stderr is returned on success.
func RunCompose(t *testing.T, dir, project string, args ...string) string {
	t.Helper()

	composePath := filepath.Join(dir, "compose.yaml")
	baseArgs := []string{"compose", "-p", project, "-f", composePath}
	fullArgs := append(baseArgs, args...) //nolint:gocritic // intentional slice extension

	cmd := exec.Command("docker", fullArgs...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("RunCompose %v: %v\nstdout: %s\nstderr: %s",
			args, err, stdout.String(), stderr.String())
	}

	return stdout.String() + stderr.String()
}

// AssertContainerState inspects the running state of <service> in <project>
// and calls t.Fatalf if it does not match wantRunning.
//
// For wantRunning=true it polls up to 15 seconds (every 500 ms) so tests are
// not sensitive to container start-up latency.
func AssertContainerState(t *testing.T, dir, project, service string, wantRunning bool) {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for {
		state := containerState(t, dir, project, service)
		isRunning := strings.EqualFold(state, "running")

		if isRunning == wantRunning {
			return
		}

		if time.Now().After(deadline) {
			if wantRunning {
				t.Fatalf("AssertContainerState: service %q in project %q: want running, got %q (timed out after 15 s)",
					service, project, state)
			} else {
				t.Fatalf("AssertContainerState: service %q in project %q: want stopped/absent, got %q (timed out after 15 s)",
					service, project, state)
			}
		}

		time.Sleep(500 * time.Millisecond)
	}
}

// PullPinnedImage pulls the given image reference (e.g. "alpine:3.21") using
// `docker pull`. Tests that need a specific image pre-pulled before asserting
// digest values should call this. The test is failed if the pull fails.
func PullPinnedImage(t *testing.T, ref string) {
	t.Helper()

	var out bytes.Buffer
	cmd := exec.Command("docker", "pull", ref)
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		t.Fatalf("PullPinnedImage(%q): %v\n%s", ref, err, out.String())
	}
}

// ImageRepoDigests returns the RepoDigests slice for a locally present image
// by running `docker inspect --format {{json .RepoDigests}} <ref>`.
// Returns nil (not a fatal error) if the image is not present locally.
func ImageRepoDigests(t *testing.T, ref string) []string {
	t.Helper()

	cmd := exec.Command("docker", "inspect", "--format", "{{json .RepoDigests}}", ref)
	out, err := cmd.Output()
	if err != nil {
		// Image absent locally — caller decides how to handle this.
		return nil
	}

	raw := strings.TrimSpace(string(out))
	if raw == "null" || raw == "" {
		return nil
	}

	var digests []string
	if jsonErr := json.Unmarshal([]byte(raw), &digests); jsonErr != nil {
		t.Fatalf("ImageRepoDigests(%q): unmarshal %q: %v", ref, raw, jsonErr)
	}
	return digests
}

// containerState returns the State string from `docker compose ps --format json`
// for the named service, or an empty string if the service/project is not found.
func containerState(t *testing.T, dir, project, service string) string {
	t.Helper()

	composePath := filepath.Join(dir, "compose.yaml")
	cmd := exec.Command(
		"docker", "compose",
		"-p", project,
		"-f", composePath,
		"ps", "--format", "json",
	)
	cmd.Dir = dir

	out, err := cmd.Output()
	if err != nil {
		// Project may not exist yet — treat as not running.
		return ""
	}

	// `docker compose ps --format json` emits one JSON object per line (NDJSON),
	// not a JSON array. Parse line by line.
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var entry struct {
			Service string `json:"Service"`
			State   string `json:"State"`
		}
		if jsonErr := json.Unmarshal([]byte(line), &entry); jsonErr != nil {
			continue
		}
		if strings.EqualFold(entry.Service, service) {
			return entry.State
		}
	}
	return ""
}

// sanitizeChars lowercases name and replaces every character outside
// [a-z0-9] with a hyphen, then collapses runs of hyphens and trims leading/
// trailing ones. It performs no length truncation and no uniqueness
// disambiguation — see sanitizeProjectName and uniqueProjectName, which
// build on it for their respective callers.
func sanitizeChars(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			// Replace any non-alphanumeric character with a hyphen.
			b.WriteRune('-')
		}
	}
	s := b.String()
	// Collapse runs of hyphens, trim leading/trailing ones.
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

// sanitizeProjectName converts a test name (which may contain '/', spaces,
// uppercase letters, underscores) into a valid Docker Compose project name:
// lowercase, only [a-z0-9-], collapsed hyphens, truncated to 40 characters.
//
// This is used directly by a handful of tests that build their own project
// names outside of NewTempStack; it is kept byte-for-byte compatible with
// its historical behavior (including the truncation collision it does not
// guard against) since those callers are outside this fix's scope. Callers
// that go through NewTempStack get uniqueProjectName instead, which layers
// collision resistance on top of the same character-sanitization rules.
func sanitizeProjectName(name string) string {
	s := sanitizeChars(name)
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		// Fallback: use a timestamp-derived suffix so we always get a valid name.
		s = fmt.Sprintf("it-%d", time.Now().UnixNano()%1_000_000)
	}
	return s
}

// stackSeqMu and stackSeq give each NewTempStack call within the same test a
// distinct, deterministic sequence number (1, 2, 3, ...), so two stacks
// created by one test — which share t.Name() — don't collide on the same
// compose project. The counter is deterministic given a fixed call order in
// the test's source code, which is fixed across runs.
var (
	stackSeqMu sync.Mutex
	stackSeq   = map[string]int{}
)

func nextStackSeq(testName string) int {
	stackSeqMu.Lock()
	defer stackSeqMu.Unlock()
	stackSeq[testName]++
	return stackSeq[testName]
}

// uniqueProjectName builds a Docker Compose project name for NewTempStack
// that stays valid (lowercase [a-z0-9-] only, <=40 chars so
// "<project>-<service>-1" stays under the 63-char container-name limit —
// see the constraint this cap protects, documented above on NewTempStack)
// while remaining unique in the two ways sanitizeProjectName alone was not:
//
//   - Across different tests whose names agree on their first N sanitized
//     characters: sanitizeProjectName's plain truncate-to-40 made those
//     collide. Here, a short FNV-1a hash of the *untruncated* sanitized name
//     is appended after truncation, so two different full names hash
//     differently even when their truncated prefixes match.
//   - Across multiple NewTempStack calls within the *same* test, which share
//     t.Name() and therefore the same hash too: the caller-supplied seq
//     (from nextStackSeq) is appended alongside the hash to disambiguate.
//
// Both the hash and seq are deterministic given a fixed test suite and a
// fixed call order within each test, so a leaked container's name can still
// be traced back to the test (and call) that created it.
func uniqueProjectName(rawName string, seq int) string {
	full := sanitizeChars(rawName)
	if full == "" {
		full = "it"
	}

	h := fnv.New32a()
	_, _ = h.Write([]byte(full))
	suffix := fmt.Sprintf("-%08x-%d", h.Sum32(), seq)

	baseLimit := 40 - len(suffix)
	if baseLimit < 1 {
		baseLimit = 1
	}
	base := full
	if len(base) > baseLimit {
		base = base[:baseLimit]
	}
	base = strings.Trim(base, "-")
	if base == "" {
		base = "it"
	}

	return base + suffix
}
