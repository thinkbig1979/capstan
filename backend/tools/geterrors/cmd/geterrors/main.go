// The geterrors command is the go vet driver for the geterrors analyzer.
//
//	go build -C tools/geterrors -o "$TMPDIR/geterrors" ./cmd/geterrors
//	go vet -vettool="$TMPDIR/geterrors" ./...      # run from backend/
//
// The -C is not optional. tools/geterrors carries its own go.mod, so from
// backend/ the form `go build -o X ./tools/geterrors/cmd/geterrors` exits 1
// with "main module does not contain package"; that nesting is what keeps
// golang.org/x/tools out of backend/go.mod.
//
// unitchecker rather than singlechecker: this binary exists only to be driven
// by `go vet -vettool`, and unitchecker is exactly that protocol with no
// go/packages loader attached.
package main

import (
	"golang.org/x/tools/go/analysis/unitchecker"

	"github.com/thinkbig1979/capstan/backend/tools/geterrors"
)

func main() { unitchecker.Main(geterrors.Analyzer) }
