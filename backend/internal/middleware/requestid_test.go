package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func init() { gin.SetMode(gin.TestMode) }

// newRequestIDRouter wires RequestID ahead of a handler that echoes whatever the
// middleware put on the context, so the test can compare the two.
func newRequestIDRouter() *gin.Engine {
	r := gin.New()
	r.Use(RequestID())
	r.GET("/probe", func(c *gin.Context) {
		c.String(http.StatusOK, RequestIDFrom(c))
	})
	return r
}

func TestRequestID_SetsHeaderAndContext(t *testing.T) {
	w := httptest.NewRecorder()
	newRequestIDRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/probe", nil))

	header := w.Header().Get(RequestIDHeader)
	if header == "" {
		t.Fatalf("no %s response header", RequestIDHeader)
	}
	if _, err := uuid.Parse(header); err != nil {
		t.Errorf("%s = %q, which is not a UUID: %v", RequestIDHeader, header, err)
	}
	if body := w.Body.String(); body != header {
		t.Errorf("context ID %q does not match the response header %q", body, header)
	}
}

func TestRequestID_IsUniquePerRequest(t *testing.T) {
	r := newRequestIDRouter()
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/probe", nil))
		id := w.Body.String()
		if seen[id] {
			t.Fatalf("request ID %q reused across requests", id)
		}
		seen[id] = true
	}
}

// TestRequestID_HonoursInboundUUID keeps one identity across a reverse proxy
// that has already assigned an ID.
func TestRequestID_HonoursInboundUUID(t *testing.T) {
	inbound := uuid.New().String()
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set(RequestIDHeader, inbound)

	w := httptest.NewRecorder()
	newRequestIDRouter().ServeHTTP(w, req)

	if got := w.Body.String(); got != inbound {
		t.Errorf("inbound request ID not honoured: got %q, want %q", got, inbound)
	}
	if got := w.Header().Get(RequestIDHeader); got != inbound {
		t.Errorf("response header = %q, want the inbound %q", got, inbound)
	}
}

// TestRequestID_RejectsUntrustedInbound is the reason inbound IDs are parsed
// rather than trusted: the value lands in log lines and audit rows, where an
// arbitrary caller-supplied string could forge entries or bloat the database.
func TestRequestID_RejectsUntrustedInbound(t *testing.T) {
	for name, hostile := range map[string]string{
		"forged log line": `abc" msg="user deleted everything`,
		"newline":         "aaa\nbbb",
		"oversized":       strings.Repeat("x", 4096),
		"not a uuid":      "12345",
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/probe", nil)
			req.Header.Set(RequestIDHeader, hostile)

			w := httptest.NewRecorder()
			newRequestIDRouter().ServeHTTP(w, req)

			got := w.Body.String()
			if got == hostile {
				t.Fatalf("untrusted inbound ID was accepted verbatim: %q", got)
			}
			if _, err := uuid.Parse(got); err != nil {
				t.Errorf("replacement ID %q is not a UUID: %v", got, err)
			}
		})
	}
}

// TestRequestIDFrom_AbsentMiddleware covers background jobs and tests, which
// serve no request and must not panic.
func TestRequestIDFrom_AbsentMiddleware(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if got := RequestIDFrom(c); got != "" {
		t.Errorf("RequestIDFrom without the middleware = %q, want empty", got)
	}
	if got := RequestIDFrom(nil); got != "" {
		t.Errorf("RequestIDFrom(nil) = %q, want empty", got)
	}
}
