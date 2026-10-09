package timebind_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/thinkbig1979/capstan/backend/tools/geterrors/timebind"
)

// TestAnalyzer runs both directions at once: analysistest fails on a
// diagnostic with no want AND on a want with no diagnostic.
//
//	dbpkg/    every bind method and receiver, each time type, the append arm,
//	          a same-named non-sql method, Scan destinations, the directive
//	other/    outside package database: bind arm fires, append arm silent
//	skiptest/ the _test.go skip, with a production-file control
func TestAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), timebind.Analyzer, "dbpkg", "other", "skiptest")
}
