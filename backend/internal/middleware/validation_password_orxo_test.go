package middleware

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// bcrypt (golang.org/x/crypto) refuses a password over 72 BYTES with
// ErrPasswordTooLong, and every path that hashes a user password runs it
// through ValidatePassword first (setup, password change, admin
// reset-password). So the policy cap must sit at 72 bytes: a longer password
// that passed it reached bcrypt and came back as a 500 (agent-os-orxo).
func TestValidatePassword_RejectsOver72BytesOrxo(t *testing.T) {
	const tooLong = "Password must not exceed 72 bytes (accented and non-Latin characters count as 2-4 bytes each)"

	at72 := "Aa1!" + strings.Repeat("x", 68)
	over72 := "Aa1!" + strings.Repeat("x", 69)
	// 29 characters, 79 bytes: a cap that counts characters (runes or UTF-16
	// units) instead of bytes accepts this, and bcrypt then refuses it.
	multibyte := "Aa1!" + strings.Repeat("€", 25)

	if n := len(at72); n != 72 {
		t.Fatalf("fixture: at72 is %d bytes", n)
	}
	if n := len(over72); n != 73 {
		t.Fatalf("fixture: over72 is %d bytes", n)
	}
	if n, c := len(multibyte), utf8.RuneCountInString(multibyte); n != 79 || c != 29 {
		t.Fatalf("fixture: multibyte is %d bytes / %d characters, want 79 / 29", n, c)
	}

	if ok, msg := ValidatePassword(at72); !ok {
		t.Errorf("72-byte password rejected: %q", msg)
	}
	for name, pw := range map[string]string{"73 ASCII bytes": over72, "29 characters / 79 bytes": multibyte} {
		ok, msg := ValidatePassword(pw)
		if ok {
			t.Errorf("%s: accepted, but bcrypt refuses any password over 72 bytes", name)
			continue
		}
		if msg != tooLong {
			t.Errorf("%s: message = %q, want %q", name, msg, tooLong)
		}
	}
}
