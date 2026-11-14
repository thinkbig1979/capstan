package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-olcn: four multi-line shapes in which the locked env views showed
// a secret compose reads. Each content holds SEKRIT inside a sensitive
// variable's value as compose reads it (oracle: the content itself, or for a
// commented-out line the line uncommented). after is a row that must stay
// visible: hiding is not stretched over the rest of the file.
var olcnLeaks = []struct{ name, content, oracle, after string }{
	// 1. A plain body's closing line holds a secret written with a colon,
	// whose key the line parser rejects (`b" API_TOKEN`).
	{"closing line reopens colon secret, tail opener", "TZ=UTC\nNOTES=\"a\" MSG=\"x\nb\" API_TOKEN: 'c\nSEKRIT'\nB=1\n", "", `"key":"B","value":"1"`},
	{"closing line reopens colon secret, first statement", "TZ=UTC\nMSG=\"x\nb\" API_TOKEN: 'c\nSEKRIT'\nB=1\n", "", `"key":"B","value":"1"`},
	{"closing line reopens colon secret, all colon", "TZ=UTC\nNOTES: \"a\" MSG: \"x\nb\" API_TOKEN: 'c\nSEKRIT'\nB=1\n", "", `"key":"B","value":"1"`},
	{"closing line holds colon secret", "TZ=UTC\nMSG=\"x\nb\" API_TOKEN: SEKRIT\nB=1\n", "", `"key":"B","value":"1"`},
	{"closing line reopens colon secret, crlf", "TZ=UTC\r\nMSG=\"x\r\nb\" API_TOKEN: 'c\r\nSEKRIT'\r\nB=1\r\n", "", `"key":"B","value":"1"`},
	{"closing line reopens colon secret, export", "TZ=UTC\nMSG=\"x\nb\" export API_TOKEN: 'c\nSEKRIT'\nB=1\n", "", `"key":"B","value":"1"`},
	// 2. A commented-out line whose second statement is the secret.
	{"commented tail", "TZ=UTC\n# NOTES=\"a\" DB_PASSWORD=SEKRIT\nB=1\n", "NOTES=\"a\" DB_PASSWORD=SEKRIT\n", `"key":"B","value":"1"`},
	{"commented tail colon", "TZ=UTC\n# NOTES: 'a' API_TOKEN: SEKRIT\nB=1\n", "NOTES: 'a' API_TOKEN: SEKRIT\n", `"key":"B","value":"1"`},
	{"commented tail url", "TZ=UTC\n#NOTES=\"a\" BACKUP=sftp:user:SEKRIT@host:/p\nB=1\n", "NOTES=\"a\" BACKUP=sftp:user:SEKRIT@host:/p\n", `"key":"B","value":"1"`},
	// 3. `K='y` folds three lines into one row, hiding the closer `z"` of
	// DB_PASSWORD's body; the walk stopped at `a"` inside X_TOKEN's body and
	// skipped X_TOKEN.
	{"fold inside a tail-opened body", "TZ=UTC\nNOTES=\"a\" DB_PASSWORD=\"x\nK='y\nz\"\nW=1 '\nX_TOKEN='c\na\"\nSEKRIT' B=2\nC=3\n", "", `"key":"C","value":"3"`},
	// 4. A commented-out opener's body (compose never reads it) runs over a
	// real secret's opener, which the walk then skipped.
	{"commented opener swallows a tail opener", "TZ=UTC\n# API_TOKEN=\"\" DB_PASSWORD='c1\nAPI_TOKEN = \"c2\" export SECRET_KEY = 'c3\nSEKRIT\"\nc4'\nB=1\n", "", `"key":"B","value":"1"`},
	{"commented opener swallows an opener", "# export SECRET_KEY=\"\" export SECRET_KEY = \"c1\nB=1\nexport SECRET_KEY = 'c2\nNOTES=\"c3\"\nexport SECRET_KEY = \"c4\"\nSEKRIT\" DB_PASSWORD: 'c5\nB=1\n", "", ""},
}

func TestEnvLocked_olcn_SecretsHiddenWhenLocked(t *testing.T) {
	for _, c := range olcnLeaks {
		t.Run(c.name, func(t *testing.T) {
			oracle := c.oracle
			if oracle == "" {
				oracle = c.content
			}
			vars, err := dotenv.UnmarshalWithLookup(oracle, nil)
			require.NoError(t, err, "compose reads the fixture")
			read := false
			for k, v := range vars {
				read = read || (strings.Contains(v, "SEKRIT") && services.IsSensitiveEnvVar(k, v))
			}
			require.True(t, read, "the oracle: compose reads SEKRIT as a secret, %q", vars)

			router, stackID, token, _ := envQuoteFixture(t, c.content)
			get := func(withToken bool) string {
				req := httptest.NewRequest(http.MethodGet, "/stacks/"+stackID+"/env", nil)
				if withToken {
					req.Header.Set(middleware.EnvUnlockHeader, token)
				}
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				require.Equal(t, http.StatusOK, w.Code)
				return w.Body.String()
			}
			require.Contains(t, get(true), "SEKRIT", "positive side: unlocked shows it")
			locked := get(false)
			assertBodyOmits(t, locked, "SEKRIT")
			if c.after != "" {
				assert.Contains(t, locked, c.after, "rows after the secret stay visible")
			}

			dataDir, grouter := globalEnvTestRouter(t)
			writeGlobalEnvFile(t, dataDir, c.content)
			_, glocked := getGlobalVars(t, grouter, "/locked")
			assertBodyOmits(t, glocked, "SEKRIT")
		})
	}
}
