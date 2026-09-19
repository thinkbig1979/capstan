//line forged_test.go:1
package lineforge

func get() (string, error) { return "", nil }

func softened() string {
	v, err := get() // want "is softened"
	if err == nil {
		return v
	}
	return "x"
}
