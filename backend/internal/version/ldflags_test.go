package version

import (
	"os"
	"reflect"
	"regexp"
	"testing"
)

// dockerfilePath is relative to this package directory (backend/internal/version).
const dockerfilePath = "../../../docker/Dockerfile"

// ldflagsX matches a single `-X <import/path>.<Symbol>=<value>` entry.
var ldflagsX = regexp.MustCompile(`-X\s+'?([^\s'"=]+)\.([A-Za-z_][A-Za-z0-9_]*)=`)

// TestDockerfileLdflagsResolveToThisPackage is the guard against the failure mode
// that makes this whole feature untrustworthy: a typo in an -X symbol path is
// accepted silently by the linker, the default value survives, and the build
// succeeds. Nothing at build time complains, so the image ships reporting "dev".
//
// The test pins the Dockerfile's -X paths to the symbols actually declared here.
// Renaming a symbol breaks compilation of the reference block below; moving the
// package or mistyping the path breaks this comparison.
func TestDockerfileLdflagsResolveToThisPackage(t *testing.T) {
	// Compile-time proof that these symbols exist with these names and are
	// settable strings. -X only works on string vars. The explicit `string`
	// type is load-bearing here, not redundant: dropping it (per staticcheck
	// QF1011) would infer the type from Version/Commit/BuildDate themselves
	// and silently accept a defined type with underlying type string,
	// weakening exactly the assertion this test exists to make.
	var _ string = Version   //nolint:staticcheck // explicit type is the assertion under test
	var _ string = Commit    //nolint:staticcheck // explicit type is the assertion under test
	var _ string = BuildDate //nolint:staticcheck // explicit type is the assertion under test

	pkgPath := reflect.TypeFor[Info]().PkgPath()
	if pkgPath == "" {
		t.Fatal("could not determine this package's import path")
	}

	content, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("reading %s: %v", dockerfilePath, err)
	}

	matches := ldflagsX.FindAllStringSubmatch(string(content), -1)
	if len(matches) == 0 {
		t.Fatalf("no -X ldflags entries found in %s; the build no longer stamps build identity", dockerfilePath)
	}

	want := map[string]bool{"Version": false, "Commit": false, "BuildDate": false}

	for _, m := range matches {
		gotPath, gotSymbol := m[1], m[2]
		if gotPath != pkgPath {
			t.Errorf("-X %s.%s targets package %q, but the version package is %q; "+
				"the linker accepts this silently and the default value survives",
				gotPath, gotSymbol, gotPath, pkgPath)
			continue
		}
		seen, known := want[gotSymbol]
		if !known {
			t.Errorf("-X %s.%s targets a symbol not declared in this package", gotPath, gotSymbol)
			continue
		}
		if seen {
			t.Errorf("-X %s.%s appears more than once", gotPath, gotSymbol)
		}
		want[gotSymbol] = true
	}

	for symbol, seen := range want {
		if !seen {
			t.Errorf("%s is never stamped: no `-X %s.%s=` entry in %s", symbol, pkgPath, symbol, dockerfilePath)
		}
	}
}

// TestDockerfileArgsDefaultToDev asserts the Dockerfile declares defaults for the
// build args, so a plain `docker build` with no --build-arg yields "dev" rather
// than an empty string. An undeclared-default ARG expands to "" and the -X then
// stamps an empty value, which reads as a bug in the UI and the logs.
func TestDockerfileArgsDefaultToDev(t *testing.T) {
	content, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("reading %s: %v", dockerfilePath, err)
	}

	for _, want := range []string{"ARG VERSION=dev", "ARG COMMIT=unknown", "ARG BUILD_DATE=unknown"} {
		if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(want) + `\s*$`).Match(content) {
			t.Errorf("%s not found in %s; a plain docker build would stamp an empty string", want, dockerfilePath)
		}
	}
}
