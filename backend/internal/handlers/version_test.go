package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/version"
)

func versionRouter() *gin.Engine {
	r := gin.New()
	NewVersionHandler().RegisterVersionRoutes(r.Group("/api/v1"))
	return r
}

func TestVersionEndpointReturnsBuildIdentity(t *testing.T) {
	origV, origC, origD := version.Version, version.Commit, version.BuildDate
	t.Cleanup(func() { version.Version, version.Commit, version.BuildDate = origV, origC, origD })
	version.Version, version.Commit, version.BuildDate = "9.9.9", "deadbeef", "2026-07-31T00:00:00Z"

	w := httptest.NewRecorder()
	versionRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/version", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var got version.Info
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding %s: %v", w.Body.String(), err)
	}
	if got.Version != "9.9.9" || got.Commit != "deadbeef" || got.BuildDate != "2026-07-31T00:00:00Z" {
		t.Errorf("body = %+v, want the stamped build identity", got)
	}
}

// TestVersionEndpointServesUnstampedDefaults covers the local-build path: no
// ldflags, so the response must carry "dev" rather than an empty version string.
func TestVersionEndpointServesUnstampedDefaults(t *testing.T) {
	w := httptest.NewRecorder()
	versionRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/version", nil))

	var got version.Info
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding %s: %v", w.Body.String(), err)
	}
	if got.Version != "dev" {
		t.Errorf("version = %q, want %q", got.Version, "dev")
	}
}

// TestVersionPathIsPublic pins the deliberate public choice. middleware.PublicPaths
// is consulted by the auth middleware *and* the CSRF middleware, so a route that
// is mounted publicly but missing from the list would start 401ing the moment it
// is moved under the protected group.
func TestVersionPathIsPublic(t *testing.T) {
	if !middleware.IsPublicPath("/api/v1/version") {
		t.Error("/api/v1/version is served outside the protected group but is not in middleware.PublicPaths")
	}
}
