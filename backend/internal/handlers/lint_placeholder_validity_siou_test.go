package handlers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-siou review r2 (R2-D1): linting with "0" in place of every secret
// refused deployable files whose secret-looking variable feeds a field "0"
// is not valid for. For an unlocked session validity is now decided on the
// REAL values; messages come only from the placeholder run, so no secret
// reaches a response.
func TestComposePut_PlaceholderDoesNotRefuseDeployableFiles_siou(t *testing.T) {
	cases := map[string]struct{ env, compose string }{
		"key file as a volume source": {"TLS_KEY_FILE=/etc/ssl/private/site.key\n",
			"services:\n  web:\n    image: nginx:1.27\n    restart: always\n    volumes:\n      - ${TLS_KEY_FILE}:/certs/key.pem:ro\n"},
		"value holding a secret as a volume source": {"POSTGRES_PASSWORD=postgres\nDATA_DIR=/srv/postgres\n",
			"services:\n  db:\n    image: postgres:16\n    restart: always\n    volumes:\n      - ${DATA_DIR}:/var/lib/postgresql/data\n"},
		"token-named container name": {"APP_TOKEN_NAME=myapp\n",
			"services:\n  web:\n    image: nginx:1.27\n    restart: always\n    container_name: ${APP_TOKEN_NAME}\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h, id, _ := composeStackSiou(t, "services:\n  web:\n    image: nginx:1.27\n", tc.env)
			router := gin.New()
			router.PUT("/stacks/:id/compose", unlockedSiou, h.Put)
			w := doJSONSiou(t, router, http.MethodPut, "/stacks/"+id+"/compose", map[string]string{"content": tc.compose})
			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		})
	}
}

// Unlocked, real values fail, the placeholder passes: one generic error, no
// text from the real run (which would quote the secret). A locked session
// never gets a verdict from the real values (review r3 Q4-1,
// TestComposeLint_LockedVerdictDoesNotDependOnSecret_siou).
func TestComposeLint_RealValuesFailGivesGenericError_siou(t *testing.T) {
	const secret = "Qk8vTz3wPm"
	h, id, _ := composeStackSiou(t, "services:\n  db:\n    image: nginx:1.27\n", "DB_PASSWORD="+secret+"\n")
	router := gin.New()
	router.POST("/stacks/:id/compose/lint", unlockedSiou, h.Lint)
	w := doJSONSiou(t, router, http.MethodPost, "/stacks/"+id+"/compose/lint",
		map[string]string{"content": "services:\n  db:\n    image: nginx:1.27\n    restart: always\n    cpus: ${DB_PASSWORD}\n"})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, `"valid":false`, "the real value does not decode as cpus: %s", body)
	assert.Contains(t, body, "does not load with the stack's environment values", body)
	for i := 0; i+4 <= len(secret); i++ {
		assert.NotContains(t, body, secret[i:i+4])
	}
	assert.False(t, strings.Contains(body, "ParseFloat"), "no compose-go text from the real run: %s", body)
}
