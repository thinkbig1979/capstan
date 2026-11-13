package handlers

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Review cor D1, the reviewer's input: an interpolated port plus a missing
// ${DB_PASSWORD:?} saves with a warning, not a 422.
func TestComposePut_MissingRequiredWithInterpolatedPortSaves_siou(t *testing.T) {
	compose := "services:\n  db:\n    image: postgres:16\n    restart: unless-stopped\n    ports:\n      - \"${HOST_PORT:-5432}:5432\"\n    environment:\n      POSTGRES_PASSWORD: ${DB_PASSWORD:?set DB_PASSWORD}\n"
	h, id, _ := composeStackSiou(t, "services:\n  db:\n    image: postgres:16\n", "HOST_PORT=5433\n")
	router := gin.New()
	router.PUT("/stacks/:id/compose", unlockedSiou, h.Put)

	w := doJSONSiou(t, router, http.MethodPut, "/stacks/"+id+"/compose", map[string]string{"content": compose})

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp ComposeSaveResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	var warned int
	for _, r := range resp.LintResults {
		assert.NotEqual(t, "error", r.Level, "%+v", r)
		if r.Rule == "required-variable" {
			warned++
			assert.Contains(t, r.Message, "DB_PASSWORD")
		}
	}
	assert.Equal(t, 1, warned, "%+v", resp.LintResults)
}
