package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// agent-os-qmw2: a JSON-quoted Authorization header, common in structured
// access logs, showed its credential: the header rule needed a separator
// after the name, and a key's closing quote before the colon is not one.
// The scheme word stays visible, as for the unquoted header.
func TestMaskSecrets_qmw2_JSONQuotedAuthorizationHeader(t *testing.T) {
	cases := []struct{ line, secret, keep string }{
		{`{"headers":{"authorization":"Bearer SEKq1"}}`, "SEKq1", "Bearer"},
		{`{"headers": {"Authorization": "Bearer SEKq2"}, "status": 200}`, "SEKq2", `"status": 200`},
		{`{"authorization":"Bearer SEKq3"}`, "SEKq3", "Bearer"},
		{`{"Proxy-Authorization": "Basic SEKq4", "x": 1}`, "SEKq4", `"x": 1`},
		{`{'authorization': 'Bearer SEKq5'}`, "SEKq5", "Bearer"},
		{`{"authorization":"SEKq6"}`, "SEKq6", `{"authorization":"`},
		{`{"AUTHORIZATION": "Digest u=\"a\", r=\"SEKq7\""}`, "SEKq7", "Digest"},
		// Cookies: the whole value, no scheme word.
		{`{"headers":{"cookie":"sid=SEKq8; theme=dark"}}`, "SEKq8", `{"headers":{"cookie":"`},
		{`{"headers": {"Set-Cookie": "sid=SEKq9; HttpOnly"}, "status": 200}`, "SEKq9", `"status": 200`},
		{`GET / HTTP/1.1 Cookie: sid=SEKq10; theme=dark`, "SEKq10", "GET / HTTP/1.1 Cookie: "},
		{`x "set-cookie: sid=SEKq11" y`, "SEKq11", `" y`},
		{`{'cookie': 'sid=SEKq12'}`, "SEKq12", `{'cookie': '`},
		{`{"cookie":"SEKq13 lang=en"}`, "SEKq13", `{"cookie":"`},
		// Review F1, F3: Go's http.Header as JSON (array values) and an
		// object value.
		{`{"Authorization":["Bearer SEKq16"]}`, "SEKq16", `{"Authorization":[`},
		{`{"headers":{"Cookie":["sid=SEKq17"],"X-Id":["7"]}}`, "SEKq17", `"X-Id":["7"]`},
		{`{"Authorization": ["Bearer SEKq18a", "Bearer SEKq18b"], "status": 200}`, "SEKq18", `"status": 200`},
		{`{"cookie": {"secure": true, "sid": "SEKq19"}, "x": 1}`, "SEKq19", `"x": 1`},
		{`{"cookie": {"a": {"b": 1}, "sid": "SEKq20"}, "x": 1}`, "SEKq20", `{"cookie": {`},
		{`{"Authorization":["Bearer SEKq21"`, "SEKq21", `{"Authorization":[`},
		// From a fuzz of these shapes: a header inside a quoted header's
		// value, and a quoted name with an unquoted value.
		{`"Cookie=Basic "qv"'Cookie=Basic ${R} proxy-authorization: ${R} & --password=x ? {'a'=SEKq14`, "SEKq14", `"Cookie=`},
		{`, {"Proxy-Authorization": q;r} --auth=a&b {'Authorization': "Bearer SEKq15", b: c}`, "SEKq15", `{"Proxy-Authorization": `},
	}
	for _, c := range cases {
		for _, path := range []string{"", ".env", "docker-compose.yml"} {
			text := c.line
			if path == "docker-compose.yml" {
				text = "services:\n  a:\n    command: " + c.line
			}
			out, _ := MaskSecrets(text, path, nil)
			assert.NotContains(t, out, c.secret, "path %q", path)
			if path != "docker-compose.yml" {
				assert.Contains(t, out, c.keep, "path %q", path)
			}
		}
	}
}

// qmw2 controls: "cookie" as part of a key, or as a word with no ":" or "="
// after it, is not a header.
func TestMaskSecrets_qmw2_CookieWordIsNotAHeader(t *testing.T) {
	for _, line := range []string{
		"COOKIE_PATH=/app/data",
		`{"cookie_path":"/app/data"}`,
		"the cookie banner was shown to user42",
		"set cookie consent then reload",
	} {
		for _, path := range []string{"", ".env"} {
			out, n := MaskSecrets(line, path, nil)
			assert.Equal(t, line, out, "path %q", path)
			assert.Equal(t, 0, n, "path %q", path)
		}
	}
}
