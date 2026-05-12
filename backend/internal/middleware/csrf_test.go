package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newCSRFTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CSRFMiddleware())
	r.GET("/api/v1/stacks", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.POST("/api/v1/stacks", func(c *gin.Context) { c.Status(http.StatusCreated) })
	r.POST("/api/v1/auth/login", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func TestCSRF_GETSetsCookie(t *testing.T) {
	r := newCSRFTestRouter()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stacks", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	cookies := w.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "docker_manager_csrf" && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected docker_manager_csrf cookie to be set on GET")
	}
}

func TestCSRF_POSTWithoutCookieIsRejected(t *testing.T) {
	r := newCSRFTestRouter()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/stacks", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on POST without CSRF cookie, got %d (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "CSRF_COOKIE_MISSING") {
		t.Fatalf("expected CSRF_COOKIE_MISSING code, got: %s", w.Body.String())
	}
}

func TestCSRF_POSTWithMatchingHeaderAndCookiePasses(t *testing.T) {
	r := newCSRFTestRouter()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/stacks", strings.NewReader("{}"))
	req.AddCookie(&http.Cookie{Name: "docker_manager_csrf", Value: "abc123"})
	req.Header.Set("X-CSRF-Token", "abc123")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestCSRF_POSTWithMismatchedHeaderRejected(t *testing.T) {
	r := newCSRFTestRouter()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/stacks", strings.NewReader("{}"))
	req.AddCookie(&http.Cookie{Name: "docker_manager_csrf", Value: "abc123"})
	req.Header.Set("X-CSRF-Token", "different")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on mismatched token, got %d", w.Code)
	}
}

func TestCSRF_PublicPathBypassesCheck(t *testing.T) {
	r := newCSRFTestRouter()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected login POST to bypass CSRF, got %d (%s)", w.Code, w.Body.String())
	}
}
