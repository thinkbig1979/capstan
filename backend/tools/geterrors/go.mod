// A SEPARATE MODULE ON PURPOSE. The analyzer needs golang.org/x/tools
// (go/analysis), and the backend is a production binary: a nested go.mod keeps
// that dependency out of backend/go.mod entirely, so `go build ./...`,
// `go vet ./...` and `go list ./...` in backend/ never see this tree. The cost
// is that every command targeting it needs -C, because the parent module does
// not contain these packages.
//
// THE `toolchain` LINE MUST TRACK backend/go.mod's. A vettool reads the export
// data the compiler wrote for the package under analysis, so the two have to
// be the same Go release. OBSERVED with this module on go 1.25.0 while
// backend/go.mod pinned toolchain go1.26.6: `go vet -vettool` still reported
// all 63 findings, but it also emitted 460 lines of
// "package requires newer Go version go1.26 (application built with go1.25)"
// for the standard library and exited 1 with nothing wrong in the tree -- so
// the gate could never go green. If you bump backend/go.mod's toolchain, bump
// this one in the same commit.
module github.com/thinkbig1979/capstan/backend/tools/geterrors

go 1.25.0

toolchain go1.26.6

require golang.org/x/tools v0.47.0

require (
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
)
