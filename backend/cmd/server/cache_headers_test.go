package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// withTempFrontend creates a throwaway frontend/{assets,index.html} tree and
// chdirs into its parent for the duration of the test, since the route
// registration under test resolves "./frontend/..." relative to the process
// CWD (see docker/Dockerfile WORKDIR /app + COPY frontend/dist /app/frontend).
func withTempFrontend(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "frontend", "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "frontend", "assets", "app-abc123.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "frontend", "index.html"), []byte("<html><head></head><body><script src=\"/assets/app-abc123.js\"></script></body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(origWd)
	})
}

// TestIndexHTML_NoStore_NoValidator guards agent-os-k6k: index.html's body is
// unique per request (the CSP nonce is spliced into <script>/<link> tags —
// main.go NoRoute handler), so it must be served Cache-Control: no-store with
// NO ETag/Last-Modified validator. A stable ETag over the source file would
// let the browser 304 and reuse an old-nonce cached body against a
// fresh-nonce CSP header, blocking every script on the page.
func TestIndexHTML_NoStore_NoValidator(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withTempFrontend(t)

	indexHTMLBytes, err := os.ReadFile("./frontend/index.html")
	if err != nil {
		t.Fatal(err)
	}

	r := gin.New()
	registerIndexRoute(r, string(indexHTMLBytes))

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/dashboard", nil)
	// The real SecurityHeaders middleware normally sets this; simulate it
	// directly since this test targets the NoRoute handler in isolation.
	r.Use(func(c *gin.Context) { c.Set("csp_nonce", "test-nonce") })
	r.ServeHTTP(w, req)

	cc := w.Header().Get("Cache-Control")
	if !strings.Contains(cc, "no-store") {
		t.Errorf("expected Cache-Control to contain %q, got %q", "no-store", cc)
	}
	if etag := w.Header().Get("ETag"); etag != "" {
		t.Errorf("expected no ETag on index.html response, got %q", etag)
	}
	if lm := w.Header().Get("Last-Modified"); lm != "" {
		t.Errorf("expected no Last-Modified on index.html response, got %q", lm)
	}
}

// TestStaticAssets_LongLivedImmutable guards agent-os-k6k: hashed Vite
// output under /assets and /fonts must be cacheable for a year since a new
// build ships under a new filename; without this, index.html's no-store
// would force a refetch of every asset on every navigation.
func TestStaticAssets_LongLivedImmutable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withTempFrontend(t)

	r := gin.New()
	registerStaticAssets(r)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/assets/app-abc123.js", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200 from static asset route, got %d body=%q", w.Code, w.Body.String())
	}
	cc := w.Header().Get("Cache-Control")
	want := "public, max-age=31536000, immutable"
	if cc != want {
		t.Errorf("expected Cache-Control %q, got %q", want, cc)
	}
}
