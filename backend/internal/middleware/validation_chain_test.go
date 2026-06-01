package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestValidateInput_DoesNotConsumeRequestBody guards against regressions of the
// pre-fix bug where the validation middleware called ShouldBindJSON, draining
// req.Body before the downstream handler could re-bind. With auth body
// validation removed from the middleware, the handler must still see the body.
func TestValidateInput_DoesNotConsumeRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidateInput())

	var received struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	var handlerCalled bool

	r.POST("/api/v1/auth/login", func(c *gin.Context) {
		handlerCalled = true
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			t.Fatalf("handler could not read body: %v", err)
		}
		if len(body) == 0 {
			t.Fatal("body was empty in handler — middleware consumed it")
		}
		if err := json.Unmarshal(body, &received); err != nil {
			t.Fatalf("handler could not parse body: %v", err)
		}
	})

	payload := []byte(`{"username":"validuser","password":"GoodPass123!"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !handlerCalled {
		t.Fatalf("handler never reached; status=%d body=%s", w.Code, w.Body.String())
	}
	if received.Username != "validuser" || received.Password != "GoodPass123!" {
		t.Fatalf("handler got wrong payload: %+v", received)
	}
}

// TestValidateInput_StackIDStillRejected confirms the surviving validation
// (stack ID format) still works after we stripped body binding.
func TestValidateInput_StackIDStillRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidateInput())
	r.GET("/api/v1/stacks/:id", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stacks/bad%20id%21", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad stack id, got %d (%s)", w.Code, w.Body.String())
	}
}

// TestValidateInput_StackIDWithSpaceAccepted reproduces the bug where a stack
// scanned from a directory whose name contains a space (e.g. "backup script-test",
// id "development~backup script-test:test") is listed by GET /stacks but 400s on
// its detail page because the ID validator's regex excluded spaces. The ID is only
// a DB lookup key, so a space is safe; the request must reach the handler.
func TestValidateInput_StackIDWithSpaceAccepted(t *testing.T) {
	const idWithSpace = "development~backup script-test:test"

	if !ValidateStackID(idWithSpace) {
		t.Fatalf("ValidateStackID rejected a valid scanned stack id with a space: %q", idWithSpace)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidateInput())
	var reached bool
	r.GET("/api/v1/stacks/:id", func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})

	// %20 is the URL-encoded space the frontend sends for this id.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stacks/development~backup%20script-test:test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !reached || w.Code != http.StatusOK {
		t.Fatalf("expected handler reached with 200 for space-containing id, got %d (%s)", w.Code, w.Body.String())
	}
}
