package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-siou review r3 (Q4-1): with validity decided on the real values,
// a locked session could bisect a numeric secret through a host port range
// ("<guess>-${PIN_TOKEN}" is valid only when the secret >= guess) and read
// PIN_TOKEN=4821 back in 16 lint calls. Locked, the placeholder run alone
// now decides, so two stacks whose secrets differ answer every probe alike.

const lockedPlaceholderNoteSiou = "Some values are hidden while locked, so lint checked this file with stand-ins for them. If an error points at a field that uses one of those values, unlock to check it with the real values."

// portsProbeSiou is the reviewer's probe. No guess equals either secret.
func portsProbeSiou(guess int) string {
	return fmt.Sprintf("services:\n  web:\n    image: nginx:1.27\n    restart: always\n    ports:\n      - \"%d-${PIN_TOKEN}:80\"\n", guess)
}

var oracleGuessesSiou = []int{1, 1000, 4000, 5000, 9000, 32768, 65535}

func TestComposeLint_LockedVerdictDoesNotDependOnSecret_siou(t *testing.T) {
	lintFor := func(pin string) func(string) (int, string) {
		h, id, _ := composeStackSiou(t, "services:\n  web:\n    image: nginx:1.27\n", "PIN_TOKEN="+pin+"\n")
		router := gin.New()
		router.POST("/stacks/:id/compose/lint", h.Lint) // no unlock: locked
		return func(compose string) (int, string) {
			w := doJSONSiou(t, router, http.MethodPost, "/stacks/"+id+"/compose/lint", map[string]string{"content": compose})
			return w.Code, w.Body.String()
		}
	}
	lintA, lintB := lintFor("4821"), lintFor("9999")
	for _, g := range oracleGuessesSiou {
		codeA, bodyA := lintA(portsProbeSiou(g))
		codeB, bodyB := lintB(portsProbeSiou(g))
		require.Equal(t, http.StatusOK, codeA, bodyA)
		assert.Equal(t, codeA, codeB, "guess %d", g)
		assert.Equal(t, bodyA, bodyB, "guess %d: the locked answer depends on the secret", g)
		assert.NotContains(t, bodyA, "4821", "guess %d", g)
		assert.NotContains(t, bodyB, "9999", "guess %d", g)
		if strings.Contains(bodyA, `"valid":false`) {
			assert.Contains(t, bodyA, lockedPlaceholderNoteSiou, "guess %d: an invalid locked verdict says why", g)
		}
	}
}

func TestComposePut_LockedVerdictDoesNotDependOnSecret_siou(t *testing.T) {
	putFor := func(pin string) func(string) (int, string) {
		h, id, _ := composeStackSiou(t, "services:\n  web:\n    image: nginx:1.27\n", "PIN_TOKEN="+pin+"\n")
		router := gin.New()
		router.PUT("/stacks/:id/compose", h.Put) // no unlock: locked
		return func(compose string) (int, string) {
			w := doJSONSiou(t, router, http.MethodPut, "/stacks/"+id+"/compose", map[string]string{"content": compose})
			return w.Code, w.Body.String()
		}
	}
	putA, putB := putFor("4821"), putFor("9999")
	for _, g := range oracleGuessesSiou {
		codeA, bodyA := putA(portsProbeSiou(g))
		codeB, bodyB := putB(portsProbeSiou(g))
		assert.Equal(t, codeA, codeB, "guess %d: the locked save's status depends on the secret", g)
		assert.Equal(t, bodyA, bodyB, "guess %d", g)
		assert.NotContains(t, bodyA, "4821", "guess %d", g)
		assert.NotContains(t, bodyB, "9999", "guess %d", g)
	}
}

// The R2-D1 input, locked: "0" is not a volume source, so the placeholder
// run fails and a locked session is told to unlock rather than given a
// verdict from the real value. Unlocked, the same file saves (R2-D1's
// TestComposePut_PlaceholderDoesNotRefuseDeployableFiles_siou).
func TestComposePut_LockedPlaceholderFailureSaysUnlock_siou(t *testing.T) {
	const keyPath = "/etc/ssl/private/site.key"
	h, id, _ := composeStackSiou(t, "services:\n  web:\n    image: nginx:1.27\n", "TLS_KEY_FILE="+keyPath+"\n")
	router := gin.New()
	router.PUT("/stacks/:id/compose", h.Put) // no unlock: locked
	w := doJSONSiou(t, router, http.MethodPut, "/stacks/"+id+"/compose", map[string]string{
		"content": "services:\n  web:\n    image: nginx:1.27\n    restart: always\n    volumes:\n      - ${TLS_KEY_FILE}:/certs/key.pem:ro\n",
	})
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, lockedPlaceholderNoteSiou, body)
	assert.NotContains(t, body, keyPath, body)
	assert.NotContains(t, body, "site.key", body)
}
