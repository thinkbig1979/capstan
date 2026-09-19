package skiptest

import "testing"

// softInTestCode is the SAME shape as softInProductionCode and carries NO want
// comment. Its silence is the assertion.
//
// MEASURED on 6c0819c: with the skip disabled the backend census is 86 rather
// than 63 -- 23 further in-class sites across 14 test files -- and they are
// mostly correct combined assertions, where merging "the call failed" with
// "the answer is wrong" is exactly what the assertion wants. Whether test
// files should be covered at all is a separate question for a later bead.
func TestSoftInTestCode(t *testing.T) {
	v, e := get()
	if e == nil {
		t.Log(v)
	}
}
