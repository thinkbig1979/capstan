//go:build integration

package integrationtest

import (
	"strings"
	"testing"
)

// These tests guard against agent-os-7as8: NewTempStack derived its compose
// project name from t.Name() alone, so (1) two calls within the same test
// collided on one project/container, and (2) two different tests whose
// names agreed on their first 40 sanitized characters collided too — with
// each test's t.Cleanup ("docker compose down -v") liable to tear down the
// other's still-running containers.
//
// Neither test below touches Docker: NewTempStack only writes compose.yaml
// and computes a name, so these run fast and skip nothing.

// naiveSanitize mirrors the character-cleaning half of the historical
// sanitizeProjectName (lowercase, non-alnum -> hyphen, collapse, trim) well
// enough to let TestNewTempStack_DistinctAcrossLongNameCollision prove its
// own premise below. It is intentionally independent of harness.go's actual
// implementation so this file compiles unchanged whether or not the fix is
// in place.
func naiveSanitize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := b.String()
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return strings.Trim(out, "-")
}

// isValidComposeProjectName reports whether name is lowercase-alphanumeric-
// plus-hyphen only and non-empty, matching Docker Compose project naming
// rules and mirroring (independently) what sanitizeProjectName is supposed
// to guarantee.
func isValidComposeProjectName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
			return false
		}
	}
	return true
}

// TestNewTempStack_DistinctProjectsPerCall reproduces defect 1: two
// NewTempStack calls within one test must not resolve to the same compose
// project (and therefore the same "<project>-<service>-1" container).
func TestNewTempStack_DistinctProjectsPerCall(t *testing.T) {
	_, projA, _ := NewTempStack(t, "services: {}\n")
	_, projB, _ := NewTempStack(t, "services: {}\n")

	if projA == projB {
		t.Fatalf("expected distinct project names for two NewTempStack calls in the same test, got %q both times", projA)
	}
}

// TestNewTempStack_TwoDifferentLongTestNamesThatCollideOnFirst40SanitizedCharsGetDistinctProjects
// reproduces defect 2: the old 40-char truncation made two *different* test
// names collide whenever their sanitized forms agreed on the first 40
// characters — dangerous because each test's t.Cleanup destroys the other's
// containers mid-run. This function's own name is long enough (>40 chars
// once sanitized) that both subtests below share an identical 40-char
// prefix purely from the parent name, regardless of their distinct suffixes.
func TestNewTempStack_TwoDifferentLongTestNamesThatCollideOnFirst40SanitizedCharsGetDistinctProjects(t *testing.T) {
	var fullA, fullB string
	var projA, projB string

	t.Run("suffix-one", func(t *testing.T) {
		fullA = naiveSanitize("it-" + t.Name())
		_, proj, _ := NewTempStack(t, "services: {}\n")
		projA = proj
	})
	t.Run("suffix-two-different", func(t *testing.T) {
		fullB = naiveSanitize("it-" + t.Name())
		_, proj, _ := NewTempStack(t, "services: {}\n")
		projB = proj
	})

	// Setup sanity (positive control on the test's own premise): confirm
	// this scenario really does collide within the historical 40-char
	// truncation window. If this fails, the test below would pass or fail
	// for the wrong reason.
	if len(fullA) < 40 || len(fullB) < 40 || fullA[:40] != fullB[:40] {
		t.Fatalf("test setup invalid: names do not collide within 40 sanitized chars: %q vs %q", fullA, fullB)
	}

	if projA == projB {
		t.Fatalf("expected distinct projects for two long, colliding test names; got %q both times", projA)
	}
}

// TestNewTempStack_ProjectNameStaysValidAndBounded is the two-sided control
// for the two tests above: a fix that achieves uniqueness by producing an
// invalid or over-long project name would pass both of them while breaking
// every real integration test, so this asserts the constraints the old code
// enforced still hold.
func TestNewTempStack_ProjectNameStaysValidAndBounded(t *testing.T) {
	// Positive control: prove isValidComposeProjectName actually rejects an
	// invalid name, so the assertions below are not vacuous.
	if isValidComposeProjectName("Not_Valid!") {
		t.Fatal("positive control failed: isValidComposeProjectName should reject uppercase/underscore/bang")
	}
	if isValidComposeProjectName("") {
		t.Fatal("positive control failed: isValidComposeProjectName should reject the empty string")
	}

	_, project, _ := NewTempStack(t, "services: {}\n")

	if !isValidComposeProjectName(project) {
		t.Errorf("NewTempStack project %q is not lowercase-alphanumeric-plus-hyphen only", project)
	}
	if len(project) > 40 {
		t.Errorf("NewTempStack project %q is %d chars, exceeds the 40-char cap", project, len(project))
	}
	// The longest service name used anywhere in this package's compose
	// fixtures is well under 20 chars; "-service-1" (10 chars) is a
	// conservative stand-in. <project>-<service>-1 must stay under Docker's
	// 63-char container name limit.
	if combined := len(project) + len("-service-1"); combined >= 63 {
		t.Errorf("project %q combined with a service suffix would be %d chars, too close to the 63-char container-name limit", project, combined)
	}
}
