package services

import (
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-w4p2: four credential shapes no line-pass rule read, in the
// locked compose view and the locked log/inspect line alike: query
// parameters, compact JSON fields, a --requirepass style flag, an
// Authorization header. Each row runs through both paths and through the
// runtime list (a container whose command holds it, the value printed alone).
func TestMaskSecrets_w4p2_Shapes(t *testing.T) {
	rows := []struct {
		name, logLine, viewLine string
		secrets, visible        []string
	}{
		{"query string", "jdbc:x://h/db?user=a&password=e1Pw77qq&token=e2Tok77qq;sslmode=off",
			"    command: app --url jdbc:x://h/db?user=a&password=e1Pw77qq&token=e2Tok77qq;sslmode=off",
			[]string{"e1Pw77qq", "e2Tok77qq"}, []string{"user=a", "sslmode=off"}},
		{"semicolon params", "jdbc:sqlserver://h;user=a;password=e3Pw77qq;encrypt=true",
			"    command: app jdbc:sqlserver://h;user=a;password=e3Pw77qq;encrypt=true",
			[]string{"e3Pw77qq"}, []string{"encrypt=true"}},
		{"compact JSON", `{"db":{"api_key":"f2Key88qq"},"n":1}`,
			`    command: ["app", "--cfg", "{\"db\":{\"api_key\":\"f2Key88qq\"}}"]`,
			[]string{"f2Key88qq"}, nil},
		{"compact JSON at line start", `"api_key":"f3Key88qq",`,
			`    healthcheck: {"api_key":"f3Key88qq"}`,
			[]string{"f3Key88qq"}, nil},
		{"requirepass flag", "redis-server --requirepass k11Pw12q --port 6379",
			"    command: redis-server --requirepass k11Pw12q --port 6379",
			[]string{"k11Pw12q"}, []string{"6379"}},
		{"requirepass= flag", "redis-server --masterauth=k12Pw12q",
			"    command: redis-server --masterauth=k12Pw12q",
			[]string{"k12Pw12q"}, nil},
		{"Authorization header", "curl -H 'Authorization: Bearer sc11Tok12' http://x",
			"    command: curl -H 'Authorization: Bearer sc11Tok12' http://x",
			[]string{"sc11Tok12"}, []string{"Bearer", "http://x"}},
		{"Authorization header unquoted", "proxy-authorization: Basic dXNlcjpwYXNzd29yZA==",
			"    command: echo proxy-authorization: Basic dXNlcjpwYXNzd29yZA==",
			[]string{"dXNlcjpwYXNzd29yZA=="}, nil},
	}
	for _, r := range rows {
		view := "services:\n  a:\n" + r.viewLine + "\n"
		paths := map[string]string{"log": r.logLine, "view": view, "view unparsed": view + "  : [\n"}
		for name, text := range paths {
			t.Run(r.name+"/"+name, func(t *testing.T) {
				path := ""
				if strings.HasPrefix(name, "view") {
					path = "docker-compose.yml"
				}
				out, _ := MaskSecrets(text, path, nil)
				for _, s := range r.secrets {
					assert.NotContains(t, out, s, "%q", out)
				}
				for _, v := range r.visible {
					assert.Contains(t, out, v, "%q", out)
				}
			})
		}
		// A container whose command holds the shape: each value printed
		// alone in a log line is masked by the runtime list.
		t.Run(r.name+"/runtime list", func(t *testing.T) {
			values := containerSecretValues(container.InspectResponse{Config: &container.Config{Cmd: []string{"sh", "-c", r.logLine}}})
			for _, s := range r.secrets {
				out, _ := MaskSecrets("log: "+s+" done", "", values)
				assert.NotContains(t, out, s, "%q", values)
			}
		})
	}
}

// agent-os-w4p2 (R4-1): a URL password is a needle as printed encoded too.
func TestUrlUserinfoPassword_w4p2_RawFormToo(t *testing.T) {
	got := urlUserinfoPassword("postgres://u:sc12P%40ss1@db/x")
	require.Contains(t, got, "sc12P@ss1")
	require.Contains(t, got, "sc12P%40ss1")
}
