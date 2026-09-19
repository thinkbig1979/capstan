// Package parens pins that parentheses are not a suppression channel.
//
// WHY THIS IS A CLASS AND NOT A CURIOSITY. The MERGE arm matches on STRUCTURE
// -- a top-level `||` chain, an operand shaped `<error> != nil` -- and the SOFT
// arm's nil test matches on an identifier in a named position. An
// *ast.ParenExpr sits between the matcher and the thing it matches, so before
// agent-os-qyg7.2's third cycle a single pair of parentheses silenced a finding
// with NO directive, NO reason and NO record. That is the exact opposite of
// what the rest of this analyzer is built to guarantee: a bare
// //geterrors:ignore is itself reported precisely so that a suppression has to
// state why. A silent structural bypass is worse than a bad reason.
//
// ALL SIX FORMS BELOW ARE gofmt-STABLE (`gofmt -l` prints nothing for this
// file), which is what makes them worth pinning. Two further forms are
// deliberately NOT here because gofmt rewrites them, so they cannot survive in
// a formatted tree and a fixture asserting either way would be asserting about
// gofmt rather than about this analyzer: `if (err != nil || v == "")` and
// `if (err == nil)` both lose their parentheses on save.
//
// THREE of the six were OBSERVED SILENT on the pre-fix binary and are the
// regression arms. THREE already fired and are here as guards: they pin that
// the fix did not have to reach them, so a later refactor that "simplifies"
// unparen cannot quietly turn a working form into a hole.
package parens

func get() (string, error) { return "", nil }

// --- the three that were silent: these are the regression arms ---

// mergeOperandParenthesised was SILENT. orOperands flattened the `||` and
// handed isErrNotNil a *ast.ParenExpr, which is not a *ast.BinaryExpr, so the
// operand was counted as a VALUE and the condition looked like two values.
func mergeOperandParenthesised() string {
	v, err := get()
	if (err != nil) || v == "" { // want "merged into a value test"
		return "x"
	}
	return v
}

// mergeSubChainParenthesised was SILENT. orOperands recurses only through
// *ast.BinaryExpr nodes whose Op is `||`, so a parenthesised sub-chain stopped
// the flattening and the error operand inside it was never examined.
func mergeSubChainParenthesised() string {
	v, err := get()
	if (err != nil || v == "") || v == "z" { // want "merged into a value test"
		return "x"
	}
	return v
}

// softVariableParenthesised was SILENT, and by the nastiest route of the three:
// isIdent saw a *ast.ParenExpr rather than the identifier, the nil-comparison
// match fell through, and ast.Inspect then descended into the parentheses and
// counted the bare identifier as a HARD use -- so the site was not merely
// unmatched, it was actively classified as correctly handled.
func softVariableParenthesised() string {
	v, err := get() // want "is softened"
	if (err) == nil {
		return v
	}
	return "x"
}

// --- the three that already fired: guards, not regressions ---

// mergeNilParenthesised fires because go/types records a type for the
// ParenExpr, so the typed nil test sees through it without help.
func mergeNilParenthesised() string {
	v, err := get()
	if err != (nil) || v == "" { // want "merged into a value test"
		return "x"
	}
	return v
}

// softComparisonParenthesised fires because the SOFT scan uses ast.Inspect,
// which descends into a ParenExpr on its own. Contrast
// softVariableParenthesised above: the same pair of parentheses one level
// deeper is a hole, which is why "parentheses are handled" needed measuring
// per form rather than reasoning about once.
func softComparisonParenthesised() string {
	v, err := get() // want "is softened"
	if (err == nil) && v != "" {
		return v
	}
	return "x"
}

// mergeUnparenthesised is the baseline. If this ever stops firing the arm is
// broken in a way none of the parenthesised cases above would reveal.
func mergeUnparenthesised() string {
	v, err := get()
	if err != nil || v == "" { // want "merged into a value test"
		return "x"
	}
	return v
}
