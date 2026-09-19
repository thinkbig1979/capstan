package geterrors_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/thinkbig1979/capstan/backend/tools/geterrors"
)

// TestAnalyzer is two-sided by construction, which is the property the
// predecessor's --self-test existed to provide and the reason it ran before
// every verdict: a detector that has silently stopped firing looks exactly
// like a clean tree, and a close reason will cite its zero.
//
//	fire/     every planted site is asserted BY LINE, one per evaded anchor
//	clean/    same callees, same variable names, same `== nil` and `!= nil`
//	          text, none of it in class -- so a pattern that matches
//	          SOMETHING is still not one that covers the class
//	typed/    the shapes with a nillable non-error second value, which only
//	          go/types can decline, plus a real-error control that must fire
//	suppress/ where //geterrors:ignore applies, where it does not, and the two
//	          forms that read as suppression while suppressing nothing
//	skiptest/ the _test.go skip, with a production-file control
//
// analysistest fails on a diagnostic with no want AND on a want with no
// diagnostic, so each package is checked in both directions without a separate
// negative arm having to be written.
func TestAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), geterrors.Analyzer,
		"fire", "clean", "typed", "suppress", "skiptest")
}
