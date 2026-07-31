// Package version carries the build identity stamped into the binary at link
// time.
//
// The values below are overridden by -ldflags "-X <import path>.Version=..." in
// docker/Dockerfile's backend build stage. The defaults are deliberately
// non-empty: an unstamped binary (a plain `go build`, or a local developer run)
// reports "dev" rather than an empty string, which reads as a bug rather than as
// "this was not built from a release tag".
//
// A typo in an -X symbol path fails silently — the linker neither errors nor
// warns, and the default survives. ldflags_test.go guards against that by
// asserting the Dockerfile's -X paths match the symbols declared here.
package version

// These are var, not const, precisely so -X can rewrite them at link time.
var (
	// Version is the release version, e.g. "1.4.0". "dev" when unstamped.
	Version = "dev"
	// Commit is the full git SHA the image was built from.
	Commit = "unknown"
	// BuildDate is the RFC3339 build timestamp.
	BuildDate = "unknown"
)

// Info is the build identity as served by GET /api/v1/version.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"buildDate"`
}

// Get returns the current build identity.
func Get() Info {
	return Info{Version: Version, Commit: Commit, BuildDate: BuildDate}
}
