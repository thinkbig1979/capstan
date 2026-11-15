package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// agent-os-3mjw: IsSensitiveEnvVar reads opaque URL credentials and JSON
// with a sensitive field from the value, under any key.
func TestIsSensitiveEnvVar_3mjw_ValueShapes(t *testing.T) {
	for value, want := range map[string]bool{
		`{"password":"x"}`:                   true,
		`{"type":"sa", "private_key": "x"}`:  true,
		"sftp:user:pw@host":                  true,
		"user:pw@host":                       true,
		"user:pw@host:/backups":              true,
		`"ftp:bob:pw@nas.local:/share"`:      true,
		"bob:p/w@[::1]:22":                   true,
		"nginx:1.25@sha256:0123456789abcdef": false, // an image pinned by digest
		"app:2@SHA512:abcdef":                false,
		"user:@host":                         false, // no password
		"db.local:5432":                      false,
		"admin@example.com":                  false,
		"https://host/a:b@c":                 false, // a URL's path, HasURLUserinfo's to judge
		`{"tier":"web"}`:                     false,
		"see me: user@host":                  false,
		"plain":                              false,
		// Accepted over-masks (one unlock each): a mailto with a name before
		// the colon, and any JSON field the key rule matches.
		"mailto:bob@example.com": true,
		`{"key":"public-id"}`:    true,
	} {
		assert.Equal(t, want, IsSensitiveEnvVar("PLAIN_NAME", value), "%q", value)
	}
}
