//go:build integration

package integrationtest

import (
	"testing"
)

// These tests guard against agent-os-whs2: sanitizeProjectName's historical
// plain truncate-to-40 gave two names agreeing on their first 40 sanitized
// characters the same project — with each test's cleanup running
// `docker compose -p <project> down -v`, one test then destroys the other's
// containers mid-run with no error raised. The two tests in resources_test.go
// still call sanitizeProjectName directly; the lifecycle tests now take their
// name from NewTempStack (agent-os-6e64).
//
// None of these tests touch Docker: they call only the pure name helpers.

// legacyTruncate40 is a test-local copy of the OLD sanitizeProjectName logic
// (naiveSanitize, then hard-cut at 40 chars). It exists so the collision test
// can prove its own premise without depending on the fixed helper.
func legacyTruncate40(name string) string {
	s := naiveSanitize(name)
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// directCallSiteInputs mirrors the raw strings the two direct call sites in
// resources_test.go pass to sanitizeProjectName (a literal prefix + a
// timestamp of the shape that file generates). Keep in step with those sites.
var directCallSiteInputs = []string{
	"it-b3-crash-123456",
	"it-b3-healthy-123456",
}

// TestSanitizeProjectName_DistinctForNamesCollidingOnFirst40Chars is the
// failing arm: two different raw names whose sanitized forms agree on their
// first 40 characters (a long enough test name is enough) must map to
// different projects.
func TestSanitizeProjectName_DistinctForNamesCollidingOnFirst40Chars(t *testing.T) {
	a := "it-lifecycle-start-" + "Test_Lifecycle_Start_Success"
	b := "it-lifecycle-start-" + "Test_Lifecycle_Start_SuccessAgain"

	// Setup sanity (positive control on the premise): under the OLD logic
	// these two inputs really do collide. Without this the assertion below
	// could pass vacuously on inputs that never collided.
	if la, lb := legacyTruncate40(a), legacyTruncate40(b); la != lb || len(la) != 40 {
		t.Fatalf("test setup invalid: inputs do not collide under legacy truncation: %q vs %q", la, lb)
	}

	pa, pb := sanitizeProjectName(a), sanitizeProjectName(b)
	if pa == pb {
		t.Fatalf("expected distinct projects for two names colliding on their first 40 sanitized chars; got %q both times", pa)
	}
}

// TestSanitizeProjectName_StaysValidAndBounded is the validity control on
// the same instrument (shape of TestNewTempStack_ProjectNameStaysValidAndBounded):
// a fix that buys uniqueness with an invalid or over-long name would pass the
// test above while breaking every direct call site.
func TestSanitizeProjectName_StaysValidAndBounded(t *testing.T) {
	// Positive control: the validity predicate must actually reject an
	// invalid name, or the assertions below are vacuous.
	if isValidComposeProjectName("Not_Valid!") {
		t.Fatal("positive control failed: isValidComposeProjectName should reject uppercase/underscore/bang")
	}
	if isValidComposeProjectName("") {
		t.Fatal("positive control failed: isValidComposeProjectName should reject the empty string")
	}

	// The longest service name in the integration fixtures is "slowcrasher"
	// (11 chars, lifecycle_test.go), so "-slowcrasher-1" (14) is the real worst case.
	const serviceSuffix = "-slowcrasher-1"

	for _, in := range append(directCallSiteInputs, "", "!!!") {
		project := sanitizeProjectName(in)
		if !isValidComposeProjectName(project) {
			t.Errorf("sanitizeProjectName(%q) = %q is not lowercase-alphanumeric-plus-hyphen only", in, project)
		}
		if len(project) > 40 {
			t.Errorf("sanitizeProjectName(%q) = %q is %d chars, exceeds the 40-char cap", in, project, len(project))
		}
		if combined := len(project) + len(serviceSuffix); combined >= 63 {
			t.Errorf("sanitizeProjectName(%q) = %q with %q would be %d chars, too close to the 63-char container-name limit", in, project, serviceSuffix, combined)
		}
	}
}

// TestSanitizeProjectName_Deterministic pins one representative name (the
// pure function's output, not a live call site) so a change that renames
// containers is deliberate, and checks every direct call-site input maps to
// the same value on a second call.
func TestSanitizeProjectName_Deterministic(t *testing.T) {
	const in = "it-lifecycle-start-Test_Lifecycle_Start_Success"
	const golden = "it-lifecycle-start-test-lifecyc-35bd36c5"
	if got := sanitizeProjectName(in); got != golden {
		t.Errorf("sanitizeProjectName(%q) = %q, want pinned %q (a change here renames containers; update deliberately)", in, got, golden)
	}

	for _, in := range directCallSiteInputs {
		first, second := sanitizeProjectName(in), sanitizeProjectName(in)
		if first != second {
			t.Errorf("sanitizeProjectName(%q) not stable within a process: %q then %q", in, first, second)
		}
		t.Logf("%-60q -> %q (%d)", in, first, len(first))
	}
}
