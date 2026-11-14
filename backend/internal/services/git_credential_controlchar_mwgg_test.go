package services

import "testing"

func TestHasControlChar_Mwgg(t *testing.T) {
	for _, s := range []string{"a\nb", "a\rb", "a\x00b", "a\tb", "a\x1bb", "a\x7fb"} {
		if !HasControlChar(s) {
			t.Errorf("HasControlChar(%q) = false", s)
		}
	}
	for _, s := range []string{"", "ghp_abc123", "user.name@example.com", "tök-ünï", "a b"} {
		if HasControlChar(s) {
			t.Errorf("HasControlChar(%q) = true", s)
		}
	}
}
