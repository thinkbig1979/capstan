package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// TestAuthHandler_Setup_ConcurrentIsSingleAdmin is the regression test for
// agent-os-iut. Setup() reads UserCount() then, after an expensive bcrypt hash,
// calls the insert. That hash sits INSIDE the check->write window, so two
// concurrent /auth/setup calls both pass the count==0 check before either
// writes; with distinct usernames (username is UNIQUE, so same-name collisions
// are caught but different names are not) both used to insert -> two admins.
//
// A sequential test cannot see this: after the first setup the fast-path
// UserCount()>0 check returns 409. Only true concurrency reaches the race, so
// the test fires both requests at once behind a start barrier.
//
// Seen failing first against pre-fix code (handler calling CreateUser instead of
// CreateFirstUser): both requests returned 200 and UserCount()==2 — the
// assertions below failed on their values, not on a compile error.
func TestAuthHandler_Setup_ConcurrentIsSingleAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()

	handler := NewAuthHandler(db, "test-secret-key-32-chars-long!!!", false)
	router := gin.New()
	router.POST("/auth/setup", handler.Setup)

	const n = 2
	usernames := []string{"adminone", "admintwo"}
	codes := make([]int, n)

	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	done.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer done.Done()
			body := `{"username":"` + usernames[i] + `","password":"OldPass123!"}`
			req := httptest.NewRequest(http.MethodPost, "/auth/setup", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			start.Wait() // release both goroutines together to hit the window
			router.ServeHTTP(w, req)
			codes[i] = w.Code
		}(i)
	}
	start.Done()
	done.Wait()

	// Exactly one setup must win; the other must be told setup is already done.
	var ok, conflict int
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		}
	}
	assert.Equal(t, 1, ok, "exactly one concurrent /auth/setup may succeed, got codes %v", codes)
	assert.Equal(t, 1, conflict, "the losing concurrent /auth/setup must get 409, got codes %v", codes)

	// The ground truth: the bootstrap window must leave exactly one admin.
	count, err := db.UserCount()
	require.NoError(t, err)
	assert.Equal(t, 1, count,
		"concurrent setup must create exactly one admin; >1 means the TOCTOU created multiple admins (agent-os-iut)")
}

// TestAuthHandler_Setup_SingleStillWorks is the positive control: the ordinary
// one-request bootstrap must still succeed and create exactly one admin.
func TestAuthHandler_Setup_SingleStillWorks(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()

	handler := NewAuthHandler(db, "test-secret-key-32-chars-long!!!", false)
	router := gin.New()
	router.POST("/auth/setup", handler.Setup)

	req := httptest.NewRequest(http.MethodPost, "/auth/setup",
		strings.NewReader(`{"username":"admin","password":"OldPass123!"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	count, err := db.UserCount()
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}
