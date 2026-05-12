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
