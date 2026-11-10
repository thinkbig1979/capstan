package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-0j8a. Two ways a save wrote a file it had not locked or checked:
//  1. EnvHandler.Put and Create validated the env path BEFORE taking the stack
//     lock (Put also read the body in between), so a git pull that retargeted
//     a link in that window had the write follow a link nobody checked.
//  2. A save locked only its own stack, though the file it writes can be
//     another stack's too (a symlinked compose or .env, or same-folder stacks
//     sharing .env), so the other stack's save could run at the same time.

type lockOrderFixture struct {
	router *gin.Engine
	lock   *services.OperationLock
	db     *database.DB
	root   string
}

func newLockOrderFixture(t *testing.T) lockOrderFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg := &config.Config{StacksDir: root}
	lock := services.NewOperationLock()
	compose := NewComposeHandler(services.NewLinterService(), db, cfg)
	compose.SetOperationLock(lock)
	env := NewEnvHandler(db, cfg)
	env.SetOperationLock(lock)

	router := gin.New()
	g := router.Group("/api")
	g.Use(func(c *gin.Context) { c.Set(middleware.CtxEnvUnlocked, true); c.Next() })
	compose.RegisterRoutes(g)
	env.RegisterRoutes(g)
	return lockOrderFixture{router: router, lock: lock, db: db, root: root}
}

// addStack registers a stack; its project name is its id.
func (f lockOrderFixture) addStack(t *testing.T, id, dir, composeFile, envFile string) {
	t.Helper()
	createTestDirectory(t, f.db, dir)
	require.NoError(t, f.db.UpsertStack(models.Stack{
		ID: id, Directory: dir, ComposeFile: composeFile, EnvFile: envFile, ProjectName: id,
	}))
}

func (f lockOrderFixture) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func writeFile0j8a(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func readFile0j8a(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: test fixture path under t.TempDir()
	require.NoError(t, err)
	return string(b)
}

func relink0j8a(t *testing.T, link, target string) {
	t.Helper()
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(target, link))
}

// putEnvParkedInBind sends PUT /:id/env with a body that arrives in two parts
// and runs between() once the handler has read the first part, so it is
// parked inside body binding. It returns the response.
func (f lockOrderFixture) putEnvParkedInBind(t *testing.T, id string, between func()) *httptest.ResponseRecorder {
	t.Helper()
	pr, pw := io.Pipe()
	defer pr.Close()
	req := httptest.NewRequest(http.MethodPut, "/api/"+id+"/env", pr)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.router.ServeHTTP(rec, req)
	}()

	send := func(part string) {
		errc := make(chan error, 1)
		go func() { _, err := pw.Write([]byte(part)); errc <- err }()
		select {
		case err := <-errc:
			require.NoError(t, err)
		case <-done:
			// The handler finishing and the write returning can be ready at
			// once. Closing the pipe fails only a write still waiting for a
			// reader, so the write's own result says which happened.
			pr.CloseWithError(io.ErrClosedPipe)
			if err := <-errc; err != nil {
				t.Fatalf("handler answered %d before reading the whole body: %s", rec.Code, rec.Body.String())
			}
		case <-time.After(time.Until(hangGuardDeadline(t))):
			t.Fatal("handler never read the request body")
		}
	}
	send(`{"raw":"`)
	between()
	send(`NEW=1\n"}`)
	require.NoError(t, pw.Close())

	select {
	case <-done:
	case <-time.After(time.Until(hangGuardDeadline(t))):
		t.Fatal("handler did not answer after the body was complete")
	}
	return rec
}

// TestEnvPut_ValidatesUnderLock_0j8a: the .env link is retargeted while Put is
// still reading its body. A target outside the stacks root must be refused
// with the outside file unchanged; a target that stays inside must be saved.
func TestEnvPut_ValidatesUnderLock_0j8a(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outside bool
	}{
		{"retarget outside the root is refused", true},
		{"retarget inside the root is saved", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLockOrderFixture(t)
			dir := filepath.Join(f.root, "app")
			writeFile0j8a(t, filepath.Join(dir, "compose.yaml"), "services: {}\n")
			writeFile0j8a(t, filepath.Join(dir, "real.env"), "OLD=1\n")
			require.NoError(t, os.Symlink(filepath.Join(dir, "real.env"), filepath.Join(dir, ".env")))
			f.addStack(t, "app", dir, "compose.yaml", ".env")

			target := filepath.Join(dir, "other.env")
			if tc.outside {
				target = filepath.Join(t.TempDir(), "secret.env")
			}
			writeFile0j8a(t, target, "KEEP=1\n")

			rec := f.putEnvParkedInBind(t, "app", func() {
				relink0j8a(t, filepath.Join(dir, ".env"), target)
			})

			assert.Equal(t, "OLD=1\n", readFile0j8a(t, filepath.Join(dir, "real.env")), "the link's first target must not be written")
			if tc.outside {
				assert.Equal(t, http.StatusBadRequest, rec.Code, "a .env retargeted outside the stacks root before the lock must be refused: %s", rec.Body.String())
				assert.Equal(t, "KEEP=1\n", readFile0j8a(t, target), "the file outside the stacks root must be unchanged")
				return
			}
			assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, "NEW=1\n", readFile0j8a(t, target), "a retarget inside the root is saved to the new target")
		})
	}
}

// TestEnvCreate_ValidatesUnderLock_0j8a: the stack's folder is a link, and it
// is retargeted after the handler read the stack and before it takes the lock
// (envCreateBeforeLockHook). A leaf .env link cannot reach this: Create refuses
// an existing target with 409 and resolveWriteTarget refuses a dangling one.
func TestEnvCreate_ValidatesUnderLock_0j8a(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outside bool
	}{
		{"folder retargeted outside the root is refused", true},
		{"folder retargeted inside the root is created", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLockOrderFixture(t)
			real1 := filepath.Join(f.root, "real1")
			real2 := filepath.Join(f.root, "real2")
			writeFile0j8a(t, filepath.Join(real1, "compose.yaml"), "services: {}\n")
			writeFile0j8a(t, filepath.Join(real2, "compose.yaml"), "services: {}\n")
			dir := filepath.Join(f.root, "app")
			require.NoError(t, os.Symlink(real1, dir))
			f.addStack(t, "app", dir, "compose.yaml", "")

			target := real2
			if tc.outside {
				target = t.TempDir()
			}
			envCreateBeforeLockHook = func() { relink0j8a(t, dir, target) }
			t.Cleanup(func() { envCreateBeforeLockHook = nil })

			rec := f.do(http.MethodPost, "/app/env", `{"content":"B=2\n"}`)

			assert.NoFileExists(t, filepath.Join(real1, ".env"), "the folder's first target must not be written")
			if tc.outside {
				assert.Equal(t, http.StatusBadRequest, rec.Code, "a folder retargeted outside the stacks root before the lock must be refused: %s", rec.Body.String())
				assert.NoFileExists(t, filepath.Join(target, ".env"), "no .env may be created outside the stacks root")
				return
			}
			assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			assert.Equal(t, "B=2\n", readFile0j8a(t, filepath.Join(real2, ".env")))
		})
	}
}

// TestSharedFileSave_LocksEveryStack_0j8a: a save on stack "a" whose file is
// also stack "b"'s must take b's lock too. With b held (a save on b in
// progress) the save answers 409, names b, leaves the file alone and holds no
// lock afterwards. With only an unrelated stack "c" held it saves and frees
// every lock it took.
func TestSharedFileSave_LocksEveryStack_0j8a(t *testing.T) {
	const newCompose = `{"content":"services:\n  web:\n    image: nginx\n"}`
	for _, tc := range []struct {
		name   string
		setup  func(t *testing.T, f lockOrderFixture) (shared string)
		method string
		path   string
		body   string
		okCode int
	}{
		{
			name: "compose symlinked to another stack's compose",
			setup: func(t *testing.T, f lockOrderFixture) string {
				a, b := filepath.Join(f.root, "a"), filepath.Join(f.root, "b")
				writeFile0j8a(t, filepath.Join(b, "compose.yaml"), "services: {}\n")
				require.NoError(t, os.MkdirAll(a, 0o755))
				require.NoError(t, os.Symlink(filepath.Join(b, "compose.yaml"), filepath.Join(a, "compose.yaml")))
				f.addStack(t, "a", a, "compose.yaml", "")
				f.addStack(t, "b", b, "compose.yaml", "")
				return filepath.Join(b, "compose.yaml")
			},
			method: http.MethodPut, path: "/a/compose", body: newCompose, okCode: http.StatusOK,
		},
		{
			name: "compose-env save, compose symlinked to another stack's compose",
			setup: func(t *testing.T, f lockOrderFixture) string {
				a, b := filepath.Join(f.root, "a"), filepath.Join(f.root, "b")
				writeFile0j8a(t, filepath.Join(b, "compose.yaml"), "services: {}\n")
				require.NoError(t, os.MkdirAll(a, 0o755))
				require.NoError(t, os.Symlink(filepath.Join(b, "compose.yaml"), filepath.Join(a, "compose.yaml")))
				f.addStack(t, "a", a, "compose.yaml", "")
				f.addStack(t, "b", b, "compose.yaml", "")
				return filepath.Join(b, "compose.yaml")
			},
			method: http.MethodPut, path: "/a/compose-env", body: `{"composeContent":"services:\n  web:\n    image: nginx\n"}`, okCode: http.StatusOK,
		},
		{
			name: "compose-env save, .env symlinked to another stack's .env",
			setup: func(t *testing.T, f lockOrderFixture) string {
				a, b := filepath.Join(f.root, "a"), filepath.Join(f.root, "b")
				writeFile0j8a(t, filepath.Join(a, "compose.yaml"), "services: {}\n")
				writeFile0j8a(t, filepath.Join(b, "compose.yaml"), "services: {}\n")
				writeFile0j8a(t, filepath.Join(b, ".env"), "OLD=1\n")
				require.NoError(t, os.Symlink(filepath.Join(b, ".env"), filepath.Join(a, ".env")))
				f.addStack(t, "a", a, "compose.yaml", ".env")
				f.addStack(t, "b", b, "compose.yaml", ".env")
				return filepath.Join(b, ".env")
			},
			method: http.MethodPut, path: "/a/compose-env", body: `{"composeContent":"services: {}\n","envRaw":"NEW=1\n"}`, okCode: http.StatusOK,
		},
		{
			name: "env symlinked to another stack's .env",
			setup: func(t *testing.T, f lockOrderFixture) string {
				a, b := filepath.Join(f.root, "a"), filepath.Join(f.root, "b")
				writeFile0j8a(t, filepath.Join(a, "compose.yaml"), "services: {}\n")
				writeFile0j8a(t, filepath.Join(b, "compose.yaml"), "services: {}\n")
				writeFile0j8a(t, filepath.Join(b, ".env"), "OLD=1\n")
				require.NoError(t, os.Symlink(filepath.Join(b, ".env"), filepath.Join(a, ".env")))
				f.addStack(t, "a", a, "compose.yaml", ".env")
				f.addStack(t, "b", b, "compose.yaml", ".env")
				return filepath.Join(b, ".env")
			},
			method: http.MethodPut, path: "/a/env", body: `{"raw":"NEW=1\n"}`, okCode: http.StatusOK,
		},
		{
			name: "same-folder stacks share .env",
			setup: func(t *testing.T, f lockOrderFixture) string {
				s := filepath.Join(f.root, "s")
				writeFile0j8a(t, filepath.Join(s, "compose.yaml"), "services: {}\n")
				writeFile0j8a(t, filepath.Join(s, "compose.prod.yaml"), "services: {}\n")
				writeFile0j8a(t, filepath.Join(s, ".env"), "OLD=1\n")
				f.addStack(t, "a", s, "compose.yaml", ".env")
				f.addStack(t, "b", s, "compose.prod.yaml", ".env")
				return filepath.Join(s, ".env")
			},
			method: http.MethodPut, path: "/a/env", body: `{"raw":"NEW=1\n"}`, okCode: http.StatusOK,
		},
		{
			name: "same-folder stack records a .env that is missing",
			setup: func(t *testing.T, f lockOrderFixture) string {
				s := filepath.Join(f.root, "s")
				writeFile0j8a(t, filepath.Join(s, "compose.yaml"), "services: {}\n")
				writeFile0j8a(t, filepath.Join(s, "compose.prod.yaml"), "services: {}\n")
				f.addStack(t, "a", s, "compose.yaml", "")
				f.addStack(t, "b", s, "compose.prod.yaml", ".env")
				return filepath.Join(s, ".env")
			},
			method: http.MethodPost, path: "/a/env", body: `{"content":"NEW=1\n"}`, okCode: http.StatusCreated,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := func(path string) string {
				b, err := os.ReadFile(path) //nolint:gosec // G304: test fixture path under t.TempDir()
				if os.IsNotExist(err) {
					return "<absent>"
				}
				require.NoError(t, err)
				return string(b)
			}
			assertFree := func(t *testing.T, f lockOrderFixture, ids ...string) {
				t.Helper()
				for _, id := range ids {
					token, err := f.lock.Acquire(id, "test probe")
					if assert.NoError(t, err, "stack %s's lock must be free after the request", id) {
						f.lock.Release(id, token)
					}
				}
			}

			t.Run("other stack busy", func(t *testing.T) {
				f := newLockOrderFixture(t)
				shared := tc.setup(t, f)
				before := snapshot(shared)
				token, err := f.lock.Acquire("b", "test holder")
				require.NoError(t, err)

				rec := f.do(tc.method, tc.path, tc.body)

				assert.Equal(t, http.StatusConflict, rec.Code, "a save on a must wait for b, which uses the same file: %s", rec.Body.String())
				assert.Contains(t, rec.Body.String(), `stack b`, "the refusal names the other stack")
				assert.NotContains(t, rec.Body.String(), f.root, "the refusal names no path")
				assert.Equal(t, before, snapshot(shared), "the shared file must be unchanged")
				f.lock.Release("b", token)
				assertFree(t, f, "a", "b")
			})

			t.Run("unrelated stack busy", func(t *testing.T) {
				f := newLockOrderFixture(t)
				shared := tc.setup(t, f)
				before := snapshot(shared)
				c := filepath.Join(f.root, "c")
				writeFile0j8a(t, filepath.Join(c, "compose.yaml"), "services: {}\n")
				f.addStack(t, "c", c, "compose.yaml", "")
				token, err := f.lock.Acquire("c", "test holder")
				require.NoError(t, err)
				defer f.lock.Release("c", token)

				rec := f.do(tc.method, tc.path, tc.body)

				assert.Equal(t, tc.okCode, rec.Code, rec.Body.String())
				assert.NotEqual(t, before, snapshot(shared), "the save must land on the shared file")
				assertFree(t, f, "a", "b")
			})
		})
	}
}

// TestSharedFileSave_BrokenUnrelatedStack_0j8a: a registered stack whose files
// cannot exist (a compose file that is a link to itself, ELOOP; a folder that
// is a regular file, ENOTDIR) shares nothing, so it must not turn a healthy
// stack's save into a 500. Covers both branches of the sharer check: an
// existing target (PUT) and a missing one (POST /env).
func TestSharedFileSave_BrokenUnrelatedStack_0j8a(t *testing.T) {
	for _, broken := range []string{"looping compose link", "folder is a file"} {
		for _, tc := range []struct {
			method, path, body string
			okCode             int
		}{
			{http.MethodPut, "/a/compose", `{"content":"services:\n  web:\n    image: nginx\n"}`, http.StatusOK},
			{http.MethodPut, "/a/env", `{"raw":"NEW=1\n"}`, http.StatusOK},
			{http.MethodPost, "/b/env", `{"content":"NEW=1\n"}`, http.StatusCreated},
		} {
			t.Run(broken+" "+tc.method+" "+tc.path, func(t *testing.T) {
				f := newLockOrderFixture(t)
				a, b := filepath.Join(f.root, "a"), filepath.Join(f.root, "b")
				writeFile0j8a(t, filepath.Join(a, "compose.yaml"), "services: {}\n")
				writeFile0j8a(t, filepath.Join(a, ".env"), "OLD=1\n")
				writeFile0j8a(t, filepath.Join(b, "compose.yaml"), "services: {}\n")
				f.addStack(t, "a", a, "compose.yaml", ".env")
				f.addStack(t, "b", b, "compose.yaml", "")

				c := filepath.Join(f.root, "c")
				switch broken {
				case "looping compose link":
					require.NoError(t, os.MkdirAll(c, 0o755))
					require.NoError(t, os.Symlink(filepath.Join(c, "compose.yaml"), filepath.Join(c, "compose.yaml")))
				case "folder is a file":
					writeFile0j8a(t, c, "not a folder\n")
				}
				f.addStack(t, "c", c, "compose.yaml", ".env")

				rec := f.do(tc.method, tc.path, tc.body)

				assert.Equal(t, tc.okCode, rec.Code, "an unrelated broken stack must not block the save: %s", rec.Body.String())
			})
		}
	}
}

// listStacksSequence answers each ListStacks call with the next entry.
type listStacksSequence struct {
	answers [][]models.Stack
	calls   int
}

func (s *listStacksSequence) ListStacks() ([]models.Stack, error) {
	answer := s.answers[min(s.calls, len(s.answers)-1)]
	s.calls++
	return answer, nil
}

// TestLockStacksSharingFiles_SharersChangedUnderLock_0j8a: the sharers are
// found again once every lock is held. When the second answer differs (a link
// moved before its stack's lock was taken) the save is refused with 409 and
// every lock it took is free again; the same answer twice locks both stacks.
func TestLockStacksSharingFiles_SharersChangedUnderLock_0j8a(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	shared := filepath.Join(root, "s", ".env")
	writeFile0j8a(t, shared, "OLD=1\n")
	stackA := models.Stack{ID: "a", Directory: filepath.Join(root, "s"), ComposeFile: "compose.yaml", EnvFile: ".env", ProjectName: "a"}
	stackB := models.Stack{ID: "b", Directory: filepath.Join(root, "s"), ComposeFile: "compose.prod.yaml", EnvFile: ".env", ProjectName: "b"}

	assertFree := func(t *testing.T, lock *services.OperationLock) {
		t.Helper()
		for _, id := range []string{"a", "b"} {
			token, err := lock.Acquire(id, "test probe")
			if assert.NoError(t, err, "stack %s's lock must be free", id) {
				lock.Release(id, token)
			}
		}
	}

	t.Run("sharers changed", func(t *testing.T) {
		lock := services.NewOperationLock()
		db := &listStacksSequence{answers: [][]models.Stack{{stackA, stackB}, {stackA}}}
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)

		release, ok := lockStacksSharingFiles(c, lock, db, &stackA, services.OpKindEnv, shared)

		assert.False(t, ok, "a sharer set that changed under the locks must refuse")
		assert.Nil(t, release)
		assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.Equal(t, 2, db.calls, "the sharers must be listed twice")
		assertFree(t, lock)
	})

	t.Run("sharers unchanged", func(t *testing.T) {
		lock := services.NewOperationLock()
		db := &listStacksSequence{answers: [][]models.Stack{{stackA, stackB}}}
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)

		release, ok := lockStacksSharingFiles(c, lock, db, &stackA, services.OpKindEnv, shared)

		require.True(t, ok, rec.Body.String())
		_, err := lock.Acquire("b", "test probe")
		assert.Error(t, err, "the sharer b must be held while the save runs")
		release()
		release()
		assertFree(t, lock)
	})
}

// TestEnvSave_PathCheckAfterLock_0j8a pins the order itself: with the stack's
// own lock held elsewhere and a path that resolves outside the root, the save
// must answer the lock's 409, not the path check's 400. A path check that ran
// before the lock would answer 400. With the lock free the same request is 400.
func TestEnvSave_PathCheckAfterLock_0j8a(t *testing.T) {
	for _, tc := range []struct {
		name, method, body string
		setup              func(t *testing.T, f lockOrderFixture, outside string)
	}{
		{"PUT, .env links outside", http.MethodPut, `{"raw":"NEW=1\n"}`, func(t *testing.T, f lockOrderFixture, outside string) {
			dir := filepath.Join(f.root, "app")
			writeFile0j8a(t, filepath.Join(dir, "compose.yaml"), "services: {}\n")
			writeFile0j8a(t, filepath.Join(outside, "secret.env"), "KEEP=1\n")
			require.NoError(t, os.Symlink(filepath.Join(outside, "secret.env"), filepath.Join(dir, ".env")))
			f.addStack(t, "app", dir, "compose.yaml", ".env")
		}},
		{"POST, folder links outside", http.MethodPost, `{"content":"NEW=1\n"}`, func(t *testing.T, f lockOrderFixture, outside string) {
			dir := filepath.Join(f.root, "app")
			writeFile0j8a(t, filepath.Join(outside, "compose.yaml"), "services: {}\n")
			require.NoError(t, os.Symlink(outside, dir))
			f.addStack(t, "app", dir, "compose.yaml", "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLockOrderFixture(t)
			tc.setup(t, f, t.TempDir())
			token, err := f.lock.Acquire("app", "test holder")
			require.NoError(t, err)

			rec := f.do(tc.method, "/app/env", tc.body)

			assert.Equal(t, http.StatusConflict, rec.Code, "the path must be checked only once the lock is held: %s", rec.Body.String())

			// Same request with the lock free: the path check refuses it, so
			// the 409 above was the lock answering first, not a passing path.
			f.lock.Release("app", token)
			rec = f.do(tc.method, "/app/env", tc.body)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}
}
