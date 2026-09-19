// Package skiptest pins the _test.go skip, which is a decision and not an
// omission: go vet analyses test files and offers no flag to stop it, so the
// filter lives inside the analyzer.
//
// This file is the CONTROL. Without it, skiptest_test.go's silence would be
// indistinguishable from the analyzer never running on this package.
package skiptest

func get() (string, error) { return "", nil }

func softInProductionCode() string {
	v, e := get() // want "is softened"
	if e == nil {
		return "default"
	}
	return v
}
