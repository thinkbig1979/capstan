package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// Tests for agent-os-k81p.1: the per-directory push switch and the commit
// author settings. Nothing here commits or pushes; part .2 reads both.

type pushTestEnv struct {
	db     *database.DB
	router *gin.Engine
}

func newPushTestEnv(t *testing.T) *pushTestEnv {
	t.Helper()
	// An encryptor is configured so the credentials route can store a token.
	db, err := database.NewWithMigrationsAndEncryptor(":memory:", services.NewTokenEncryptorOrDefault("push-switch-test-key-0123456789", ""))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	cfg := &config.Config{StacksDir: "/tmp/test"}
	dirs := NewDirectoriesHandler(services.NewScannerService(cfg, db), db)
	settings := NewSettingsHandler(db, "/opt/stacks", dbFaultTestSecret, false, nil, cfg)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	dirs.RegisterRoutes(r.Group("/directories"))
	r.GET("/settings/git", settings.GetGitSettings)
	r.PUT("/settings/git", settings.UpdateGitSettings)
	return &pushTestEnv{db: db, router: r}
}

func (e *pushTestEnv) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func (e *pushTestEnv) register(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, e.db.UpsertDirectory(models.Directory{Path: path, Name: "stacks", ScannedAt: time.Now()}))
}

func (e *pushTestEnv) putPush(t *testing.T, body string) *httptest.ResponseRecorder {
	return e.do(t, http.MethodPut, "/directories/push", body)
}

func pushActionRows(t *testing.T, db *database.DB) []models.ActionLog {
	t.Helper()
	all, err := db.GetRecentActions(100)
	require.NoError(t, err)
	var rows []models.ActionLog
	for _, a := range all {
		if a.Action == services.ActionUpdateDirectoryPush {
			rows = append(rows, a)
		}
	}
	return rows
}

func directoryOf(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp struct {
		Directory map[string]any `json:"directory"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), w.Body.String())
	require.NotNil(t, resp.Directory, w.Body.String())
	return resp.Directory
}

func TestGitPushEnabled_DefaultsFalseAndIsListed(t *testing.T) {
	e := newPushTestEnv(t)
	e.register(t, "/stacks/a")

	w := e.do(t, http.MethodGet, "/directories", "")
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Directories []map[string]any `json:"directories"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Directories, 1)
	v, present := resp.Directories[0]["gitPushEnabled"]
	require.True(t, present, "gitPushEnabled is always sent, never omitted: %s", w.Body.String())
	assert.Equal(t, false, v)
}

func TestGitPushEnabled_SetTrueThenFalse(t *testing.T) {
	e := newPushTestEnv(t)
	e.register(t, "/stacks/a")

	w := e.putPush(t, `{"path":"/stacks/a","enabled":true}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, true, directoryOf(t, w)["gitPushEnabled"])
	got, err := e.db.GetDirectory("/stacks/a")
	require.NoError(t, err)
	assert.True(t, got.GitPushEnabled)

	// false must be accepted: a plain bool with binding:"required" rejects it,
	// and the switch could then be turned on and never off.
	w = e.putPush(t, `{"path":"/stacks/a","enabled":false}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, false, directoryOf(t, w)["gitPushEnabled"])
	got, err = e.db.GetDirectory("/stacks/a")
	require.NoError(t, err)
	assert.False(t, got.GitPushEnabled)
}

func TestGitPushEnabled_UnregisteredPathIs404LikeCredentials(t *testing.T) {
	e := newPushTestEnv(t)
	buf := captureHandlerLogs(t)

	push := e.putPush(t, `{"path":"/stacks/missing","enabled":true}`)
	creds := e.do(t, http.MethodPut, "/directories/credentials", `{"path":"/stacks/missing","authType":"inherit"}`)
	require.Equal(t, http.StatusNotFound, creds.Code, "control: the credentials route answers 404 for an unregistered path")
	assert.Equal(t, http.StatusNotFound, push.Code, push.Body.String())
	assert.Empty(t, pushActionRows(t, e.db), "a refused change leaves no audit row")
	assert.NotContains(t, buf.String(), "Directory push setting updated")
}

func TestGitPushEnabled_OmittedOrMalformedBodyIs400(t *testing.T) {
	e := newPushTestEnv(t)
	e.register(t, "/stacks/a")
	require.NoError(t, e.db.SetDirectoryPushEnabled("/stacks/a", true))

	for name, body := range map[string]string{
		"enabled omitted": `{"path":"/stacks/a"}`,
		"enabled null":    `{"path":"/stacks/a","enabled":null}`,
		"path omitted":    `{"enabled":true}`,
		"not json":        `nope`,
	} {
		t.Run(name, func(t *testing.T) {
			w := e.putPush(t, body)
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		})
	}
	got, err := e.db.GetDirectory("/stacks/a")
	require.NoError(t, err)
	assert.True(t, got.GitPushEnabled, "a rejected request must not flip the switch")
	assert.Empty(t, pushActionRows(t, e.db))
}

// The switch is set TRUE first: with the default false, a credential save that
// resets the column is indistinguishable from one that leaves it alone.
func TestGitPushEnabled_CredentialsAndSwitchDoNotClobberEachOther(t *testing.T) {
	e := newPushTestEnv(t)
	e.register(t, "/stacks/a")

	w := e.do(t, http.MethodPut, "/directories/credentials",
		`{"path":"/stacks/a","authType":"https","httpsUser":"deploy","httpsToken":"s3cret-token"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	require.Equal(t, http.StatusOK, e.putPush(t, `{"path":"/stacks/a","enabled":true}`).Code)
	creds, err := e.db.GetDirectoryCredentials("/stacks/a")
	require.NoError(t, err)
	assert.Equal(t, "s3cret-token", creds.GitHTTPSToken, "arming the switch leaves the stored credential alone")
	assert.Equal(t, "deploy", creds.GitHTTPSUser)
	assert.Equal(t, "https", creds.GitAuthType)

	w = e.do(t, http.MethodPut, "/directories/credentials",
		`{"path":"/stacks/a","authType":"https","httpsUser":"other","httpsToken":"another-token"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, true, directoryOf(t, w)["gitPushEnabled"], "saving credentials must not reset the switch")
	got, err := e.db.GetDirectory("/stacks/a")
	require.NoError(t, err)
	assert.True(t, got.GitPushEnabled)

	assert.NotContains(t, e.do(t, http.MethodPut, "/directories/push", `{"path":"/stacks/a","enabled":true}`).Body.String(),
		"another-token", "the push response must not carry the token")
}

func TestGitPushEnabled_RescanKeepsTheSwitch(t *testing.T) {
	e := newPushTestEnv(t)
	e.register(t, "/stacks/a")
	require.Equal(t, http.StatusOK, e.putPush(t, `{"path":"/stacks/a","enabled":true}`).Code)

	require.NoError(t, e.db.UpsertDirectory(models.Directory{Path: "/stacks/a", Name: "renamed", ScannedAt: time.Now()}))

	got, err := e.db.GetDirectory("/stacks/a")
	require.NoError(t, err)
	require.Equal(t, "renamed", got.Name)
	assert.True(t, got.GitPushEnabled)
}

func TestGitPushEnabled_OneActionLogRowPerSuccessfulPut(t *testing.T) {
	e := newPushTestEnv(t)
	e.register(t, "/stacks/a")

	require.Equal(t, http.StatusOK, e.putPush(t, `{"path":"/stacks/a","enabled":true}`).Code)
	rows := pushActionRows(t, e.db)
	require.Len(t, rows, 1)
	var detail map[string]any
	require.NoError(t, json.Unmarshal([]byte(rows[0].Detail), &detail))
	assert.Equal(t, map[string]any{"path": "/stacks/a", "enabled": true}, detail)

	require.Equal(t, http.StatusOK, e.putPush(t, `{"path":"/stacks/a","enabled":false}`).Code)
	assert.Len(t, pushActionRows(t, e.db), 2)

	require.Equal(t, http.StatusNotFound, e.putPush(t, `{"path":"/stacks/nope","enabled":true}`).Code)
	assert.Len(t, pushActionRows(t, e.db), 2, "a 404 writes no row")
}

func TestGitAuthorSettings_RoundTrip(t *testing.T) {
	e := newPushTestEnv(t)

	w := e.do(t, http.MethodGet, "/settings/git", "")
	require.Equal(t, http.StatusOK, w.Code)
	var empty map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &empty))
	assert.Equal(t, "", empty["authorName"])
	assert.Equal(t, "", empty["authorEmail"])

	w = e.do(t, http.MethodPut, "/settings/git", `{"authorName":"  Ada Lovelace ","authorEmail":"ada@example.com"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "Ada Lovelace", got["authorName"], "stored trimmed")
	assert.Equal(t, "ada@example.com", got["authorEmail"])

	// "" and omitted both mean "no change", for the author like the other fields.
	w = e.do(t, http.MethodPut, "/settings/git", `{"authorName":"","httpsUser":"deploy"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "Ada Lovelace", got["authorName"])
	assert.Equal(t, "ada@example.com", got["authorEmail"])
	assert.Equal(t, "deploy", got["httpsUser"])
}

func TestGitAuthorSettings_ActionLogRecordsWhichFieldsNotValues(t *testing.T) {
	e := newPushTestEnv(t)
	require.Equal(t, http.StatusOK, e.do(t, http.MethodPut, "/settings/git", `{"authorName":"Ada Lovelace"}`).Code)

	all, err := e.db.GetRecentActions(10)
	require.NoError(t, err)
	var detail string
	for _, a := range all {
		if a.Action == services.ActionUpdateGitSettings {
			detail = a.Detail
		}
	}
	require.NotEmpty(t, detail)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(detail), &m))
	assert.Equal(t, true, m["author_name"])
	assert.Equal(t, false, m["author_email"])
	assert.NotContains(t, detail, "Ada")
}

func TestGitAuthorSettings_InvalidValuesAre400AndStoreNothing(t *testing.T) {
	e := newPushTestEnv(t)

	cases := map[string]string{
		"name whitespace only":    `{"authorName":"   "}`,
		"name newline":            `{"authorName":"Ada\nLovelace"}`,
		"name carriage return":    `{"authorName":"Ada\rLovelace"}`,
		"name too long":           `{"authorName":"` + strings.Repeat("a", 201) + `"}`,
		"email whitespace":        `{"authorEmail":"  "}`,
		"email newline":           `{"authorEmail":"a@b.c\n"}`,
		"email no at":             `{"authorEmail":"ada.example.com"}`,
		"email two at":            `{"authorEmail":"a@b@c.com"}`,
		"email empty local":       `{"authorEmail":"@example.com"}`,
		"email empty domain":      `{"authorEmail":"ada@"}`,
		"email too long":          `{"authorEmail":"` + strings.Repeat("a", 250) + `@b.co"}`,
		"name NUL":                `{"authorName":"Ada\u0000Lovelace"}`,
		"name ESC":                `{"authorName":"Ada\u001bLovelace"}`,
		"name VT":                 `{"authorName":"Ada\u000bLovelace"}`,
		"name tab":                `{"authorName":"Ada\tLovelace"}`,
		"name NEL U+0085":         `{"authorName":"Ada\u0085Lovelace"}`,
		"name line sep U+2028":    `{"authorName":"Ada\u2028Lovelace"}`,
		"name para sep U+2029":    `{"authorName":"Ada\u2029Lovelace"}`,
		"name trailing U+2028":    `{"authorName":"Ada\u2028"}`,
		"name angle open":         `{"authorName":"Ada <Lovelace"}`,
		"name angle close":        `{"authorName":"Ada Lovelace>"}`,
		"name 201 multibyte":      `{"authorName":"` + strings.Repeat("é", 201) + `"}`,
		"email NUL":               `{"authorEmail":"a\u0000@b.co"}`,
		"email ESC":               `{"authorEmail":"a\u001b@b.co"}`,
		"email NEL U+0085":        `{"authorEmail":"a\u0085@b.co"}`,
		"email line sep U+2028":   `{"authorEmail":"a@b.co\u2028"}`,
		"email para sep U+2029":   `{"authorEmail":"a\u2029@b.co"}`,
		"email angle brackets":    `{"authorEmail":"<>"}`,
		"email angle with at":     `{"authorEmail":"<>@<>"}`,
		"email wrapped in angles": `{"authorEmail":"<ada@example.com>"}`,
		"email 255 multibyte":     `{"authorEmail":"` + strings.Repeat("é", 250) + `@b.co"}`,
		// A valid name must not be stored when the email in the same request is bad.
		"valid name, bad email": `{"authorName":"Ada","authorEmail":"nope"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			w := e.do(t, http.MethodPut, "/settings/git", body)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), "VALIDATION_ERROR")
		})
	}

	var got map[string]any
	require.NoError(t, json.Unmarshal(e.do(t, http.MethodGet, "/settings/git", "").Body.Bytes(), &got))
	assert.Equal(t, "", got["authorName"])
	assert.Equal(t, "", got["authorEmail"])

	// Boundary sides: exactly the limits are accepted.
	ok := `{"authorName":"` + strings.Repeat("a", 200) + `","authorEmail":"` + strings.Repeat("a", 248) + `@b.co"}`
	require.Equal(t, http.StatusOK, e.do(t, http.MethodPut, "/settings/git", ok).Code)
	// Limits count characters, not bytes: 200 and 254 two-byte characters fit.
	multi := `{"authorName":"` + strings.Repeat("é", 200) + `","authorEmail":"` + strings.Repeat("é", 248) + `@b.co"}`
	require.Equal(t, http.StatusOK, e.do(t, http.MethodPut, "/settings/git", multi).Code)
}
