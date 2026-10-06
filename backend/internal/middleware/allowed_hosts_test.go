package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// newRebindingServer builds the shape DNS rebinding attacks (agent-os-n4ca.1):
// a real listener on 127.0.0.1, so every request arrives FROM loopback and
// passes AuthMiddleware's AUTH_DISABLED peer check, with a protected REST
// route and a WebSocket route behind it. The WS upgrader accepts a same-origin
// Origin exactly as handlers.InitUpgrader does with CORS_ORIGINS unset
// (ws.go), which a rebound page always satisfies: its Origin and its Host
// both name the attacker's hostname. withHostCheck=false is the pre-fix chain.
func newRebindingServer(t *testing.T, withHostCheck bool, allowedHosts []string) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)

	r := gin.New()
	if withHostCheck {
		r.Use(AllowedHosts(true, allowedHosts))
	}
	protected := r.Group("/api/v1")
	protected.Use(AuthMiddleware(nil, "", true, ""))
	protected.GET("/stacks", func(c *gin.Context) { c.Status(http.StatusOK) })

	upgrader := websocket.Upgrader{CheckOrigin: func(req *http.Request) bool {
		origin := req.Header.Get("Origin")
		return origin == "" || origin == "http://"+req.Host
	}}
	protected.GET("/ws/test", func(c *gin.Context) {
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		_ = conn.Close()
	})

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func serverPort(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Port()
}

func getWithHost(t *testing.T, srv *httptest.Server, host string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/stacks", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// dialWithHost opens the WS route with the Host and Origin a browser on that
// hostname would send, returning the handshake status.
func dialWithHost(t *testing.T, srv *httptest.Server, host string) int {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/ws/test"
	header := http.Header{}
	header.Set("Host", host)
	header.Set("Origin", "http://"+host)
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if conn != nil {
		_ = conn.Close()
	}
	if resp == nil {
		t.Fatalf("dial %s: no handshake response: %v", host, err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestAllowedHosts_RebindingHostIsRefusedOnRESTAndWebSocket(t *testing.T) {
	srv := newRebindingServer(t, true, nil)
	port := serverPort(t, srv)
	evil := "evil.example:" + port
	local := "localhost:" + port

	if got := getWithHost(t, srv, evil); got != http.StatusForbidden {
		t.Errorf("REST with Host %q: status %d, want 403", evil, got)
	}
	if got := getWithHost(t, srv, local); got != http.StatusOK {
		t.Errorf("REST with Host %q: status %d, want 200", local, got)
	}
	if got := dialWithHost(t, srv, evil); got != http.StatusForbidden {
		t.Errorf("WS upgrade with Host %q: status %d, want 403", evil, got)
	}
	if got := dialWithHost(t, srv, local); got != http.StatusSwitchingProtocols {
		t.Errorf("WS upgrade with Host %q: status %d, want 101", local, got)
	}
}

// The other half of the pair above: on the same fixture WITHOUT the Host
// check, the rebound request gets through on both routes. If this ever goes
// red, the fixture no longer models the attack and the test above proves
// nothing.
func TestAllowedHosts_FixtureWithoutHostCheckIsOpenToRebinding(t *testing.T) {
	srv := newRebindingServer(t, false, nil)
	port := serverPort(t, srv)
	evil := "evil.example:" + port

	if got := getWithHost(t, srv, evil); got != http.StatusOK {
		t.Errorf("REST with Host %q and no host check: status %d, want 200", evil, got)
	}
	if got := dialWithHost(t, srv, evil); got != http.StatusSwitchingProtocols {
		t.Errorf("WS upgrade with Host %q and no host check: status %d, want 101", evil, got)
	}
}

func hostCheckStatus(authDisabled bool, allowedHosts []string, host string) (int, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AllowedHosts(authDisabled, allowedHosts))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Host = host
	r.ServeHTTP(w, req)
	return w.Code, w
}

func TestAllowedHosts_HostForms(t *testing.T) {
	extra := []string{"Capstan.LAN", "192.168.1.10", "nas.local."}
	cases := []struct {
		host string
		want int
	}{
		{"localhost", http.StatusOK},
		{"localhost:5001", http.StatusOK},
		{"LOCALHOST:5001", http.StatusOK},
		{"localhost.:5001", http.StatusOK},
		{"127.0.0.1", http.StatusOK},
		{"127.0.0.1:5001", http.StatusOK},
		{"[::1]", http.StatusOK},
		{"[::1]:5001", http.StatusOK},
		{"::1", http.StatusOK},
		{"capstan.lan", http.StatusOK},
		{"capstan.lan:443", http.StatusOK},
		{"192.168.1.10:5001", http.StatusOK},
		{"nas.local", http.StatusOK},

		{"", http.StatusForbidden},
		{"evil.example", http.StatusForbidden},
		{"evil.example:5001", http.StatusForbidden},
		{"localhost.evil.example", http.StatusForbidden},
		{"127.0.0.1.nip.io:5001", http.StatusForbidden},
		{"capstan.lan.evil.example", http.StatusForbidden},
		{"192.168.1.11:5001", http.StatusForbidden},
		{"[::2]:5001", http.StatusForbidden},
		{"[::1]x:5001", http.StatusForbidden},
	}
	for _, tc := range cases {
		if got, _ := hostCheckStatus(true, extra, tc.host); got != tc.want {
			t.Errorf("Host %q: status %d, want %d", tc.host, got, tc.want)
		}
	}
}

// The 403 is what an AUTH_DISABLED user browsing by LAN name sees after
// upgrading, so it must name the setting that fixes it.
func TestAllowedHosts_RefusalNamesTheSetting(t *testing.T) {
	code, w := hostCheckStatus(true, nil, "capstan.lan:5001")
	if code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", code)
	}
	var body models.AppError
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("403 body is not an AppError: %v (%s)", err, w.Body.String())
	}
	if body.Code != models.ErrForbidden {
		t.Errorf("code %q, want %q", body.Code, models.ErrForbidden)
	}
	if !strings.Contains(body.Message, "ALLOWED_HOSTS") || !strings.Contains(body.Message, "capstan.lan") {
		t.Errorf("message %q must name the host and ALLOWED_HOSTS", body.Message)
	}
}

// With authentication on, a request's Host decides nothing: a session token
// is required whatever name the browser used, so the check stays out of the
// way. Paired with the same request under AUTH_DISABLED on the same helper.
func TestAllowedHosts_NoOpWhenAuthEnabled(t *testing.T) {
	if got, _ := hostCheckStatus(false, nil, "evil.example:5001"); got != http.StatusOK {
		t.Errorf("auth enabled: status %d, want 200", got)
	}
	if got, _ := hostCheckStatus(true, nil, "evil.example:5001"); got != http.StatusForbidden {
		t.Errorf("auth disabled: status %d, want 403", got)
	}
}
