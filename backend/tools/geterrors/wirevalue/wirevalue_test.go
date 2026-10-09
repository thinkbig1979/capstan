package wirevalue_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/thinkbig1979/capstan/backend/tools/geterrors/wirevalue"
)

// TestAnalyzer runs both directions at once: analysistest fails on a
// diagnostic with no want AND on a want with no diagnostic.
//
//	services/  every carrier kind (local from a seeded result, an interface
//	           method, field assign, composite key, plain-string const,
//	           switch case, call arg to a same-package param), the typed-const
//	           spelling, BackupRun.Status and a cross-package helper param
//	           that must stay silent, the directive forms, the _test.go skip
//	handlers/  a seeded function result, a seeded cross-package parameter,
//	           and a local compared before it reaches a seeded parameter
func TestAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), wirevalue.Analyzer,
		"capstan/internal/services", "capstan/internal/handlers")
}
