package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

func TestNewScannerService(t *testing.T) {
	cfg := &config.Config{StacksDir: "/tmp/test"}
	db := &database.DB{}

	service := NewScannerService(cfg, db)

	assert.NotNil(t, service)
	assert.Equal(t, cfg, service.config)
	assert.Equal(t, db, service.db)
}

func TestScannerService_ScanAll_EmptyDirectory(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{StacksDir: tempDir}

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	service := NewScannerService(cfg, db)

	hasGlobalEnv, err := service.ScanAll()
	assert.NoError(t, err)
	assert.False(t, hasGlobalEnv)

	dirs, err := db.ListDirectories()
	assert.NoError(t, err)
	assert.Empty(t, dirs)
}

func TestScannerService_ScanAll_WithGlobalEnv(t *testing.T) {
	tempDir := t.TempDir()
	dataDir := t.TempDir()
	cfg := &config.Config{StacksDir: tempDir, DataDir: dataDir}

	globalEnvPath := filepath.Join(dataDir, "global.env")
	err := os.WriteFile(globalEnvPath, []byte("TEST=value\n"), 0644)
	require.NoError(t, err)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	service := NewScannerService(cfg, db)

	hasGlobalEnv, err := service.ScanAll()
	assert.NoError(t, err)
	assert.True(t, hasGlobalEnv)
}

func TestScannerService_ScanAll_SingleStack(t *testing.T) {
	tempDir := t.TempDir()

	stackDir := filepath.Join(tempDir, "my-stack")
	err := os.MkdirAll(stackDir, 0755)
	require.NoError(t, err)

	composeContent := `services:
  web:
    image: nginx:latest
`
	composePath := filepath.Join(stackDir, "compose.yaml")
	err = os.WriteFile(composePath, []byte(composeContent), 0644)
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir}
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	service := NewScannerService(cfg, db)

	_, err = service.ScanAll()
	assert.NoError(t, err)

	stacks, err := db.ListStacks()
	assert.NoError(t, err)
	assert.Len(t, stacks, 1)
	assert.Equal(t, filepath.Base(tempDir)+"~my-stack:default", stacks[0].ID)
}

func TestScannerService_ScanAll_MultipleStacks(t *testing.T) {
	tempDir := t.TempDir()

	stack1Dir := filepath.Join(tempDir, "stack1")
	err := os.MkdirAll(stack1Dir, 0755)
	require.NoError(t, err)

	composePath := filepath.Join(stack1Dir, "compose.yaml")
	err = os.WriteFile(composePath, []byte("services:\n  web:\n    image: nginx\n"), 0644)
	require.NoError(t, err)

	stack2Dir := filepath.Join(tempDir, "stack2")
	err = os.MkdirAll(stack2Dir, 0755)
	require.NoError(t, err)

	composePath = filepath.Join(stack2Dir, "docker-compose.yml")
	err = os.WriteFile(composePath, []byte("services:\n  db:\n    image: postgres\n"), 0644)
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir}
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	service := NewScannerService(cfg, db)

	_, err = service.ScanAll()
	assert.NoError(t, err)

	stacks, err := db.ListStacks()
	assert.NoError(t, err)
	assert.Len(t, stacks, 2)
}

func TestScannerService_ScanAll_HiddenDirectories(t *testing.T) {
	tempDir := t.TempDir()

	hiddenDir := filepath.Join(tempDir, ".hidden")
	err := os.MkdirAll(hiddenDir, 0755)
	require.NoError(t, err)

	composePath := filepath.Join(hiddenDir, "compose.yaml")
	err = os.WriteFile(composePath, []byte("services:\n  web:\n    image: nginx\n"), 0644)
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir}
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	service := NewScannerService(cfg, db)

	_, err = service.ScanAll()
	assert.NoError(t, err)

	stacks, err := db.ListStacks()
	assert.NoError(t, err)
	assert.Empty(t, stacks)
}

func TestScannerService_ScanAll_MultipleComposeFiles(t *testing.T) {
	tempDir := t.TempDir()

	stackDir := filepath.Join(tempDir, "multi-stack")
	err := os.MkdirAll(stackDir, 0755)
	require.NoError(t, err)

	composePath := filepath.Join(stackDir, "compose.yaml")
	err = os.WriteFile(composePath, []byte("services:\n  web:\n    image: nginx\n"), 0644)
	require.NoError(t, err)

	composePath = filepath.Join(stackDir, "compose.testing.yaml")
	err = os.WriteFile(composePath, []byte("services:\n  test:\n    image: test\n"), 0644)
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir}
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	service := NewScannerService(cfg, db)

	_, err = service.ScanAll()
	assert.NoError(t, err)

	stacks, err := db.ListStacks()
	assert.NoError(t, err)
	assert.Len(t, stacks, 2)

	stackNames := make(map[string]bool)
	for _, s := range stacks {
		stackNames[s.ID] = true
	}
	rootPrefix := filepath.Base(tempDir)
	assert.True(t, stackNames[rootPrefix+"~multi-stack:default"])
	assert.True(t, stackNames[rootPrefix+"~multi-stack:testing"])
}

// Regression test for agent-os-4yy: extractStackName strips both "compose."
// and "docker-compose." prefixes, so compose.api.yaml and
// docker-compose.api.yml both extract to the stack name "api". Before this
// fix, the duplicate-name guard at scanner.go only fired for name=="default",
// so the second file's UpsertStack (INSERT OR REPLACE) silently overwrote the
// first's row, leaving the other compose file on disk but untracked. There
// must be exactly one row for "api", and the winner must be deterministic
// (pinned to compose.api.yaml, per the scan's fixed pattern order — compose*
// before docker-compose* — which is Docker Compose's own file precedence, not
// directory-read order) rather than merely "some row survives".
func TestScannerService_ScanAll_DuplicateStackName_SkipsSecondFileDeterministically(t *testing.T) {
	tempDir := t.TempDir()

	stackDir := filepath.Join(tempDir, "my-stack")
	require.NoError(t, os.MkdirAll(stackDir, 0755))

	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "compose.api.yaml"),
		[]byte("services:\n  api:\n    image: api-v1\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "docker-compose.api.yml"),
		[]byte("services:\n  api:\n    image: api-v2\n"), 0644))

	cfg := &config.Config{StacksDir: tempDir}
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	service := NewScannerService(cfg, db)

	_, err = service.ScanAll()
	assert.NoError(t, err)

	stacks, err := db.ListStacks()
	assert.NoError(t, err)

	var apiStacks []models.Stack
	for _, s := range stacks {
		if strings.HasSuffix(s.ID, ":api") {
			apiStacks = append(apiStacks, s)
		}
	}
	require.Len(t, apiStacks, 1, "two compose files mapping to the same stack name must produce exactly one row")
	assert.Equal(t, "compose.api.yaml", apiStacks[0].ComposeFile,
		"the winner must be deterministic (compose.* precedes docker-compose.* in scan order), not whichever file the directory walk reached last")
}

func TestScannerService_ScanAll_WithGitRepo(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{StacksDir: tempDir}

	stackDir := filepath.Join(tempDir, "git-stack")
	err := os.MkdirAll(stackDir, 0755)
	require.NoError(t, err)

	gitDir := filepath.Join(stackDir, ".git")
	err = os.MkdirAll(gitDir, 0755)
	require.NoError(t, err)

	headPath := filepath.Join(gitDir, "HEAD")
	err = os.WriteFile(headPath, []byte("ref: refs/heads/main\n"), 0644)
	require.NoError(t, err)

	composePath := filepath.Join(stackDir, "compose.yaml")
	err = os.WriteFile(composePath, []byte("services:\n  web:\n    image: nginx\n"), 0644)
	require.NoError(t, err)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	service := NewScannerService(cfg, db)

	_, err = service.ScanAll()
	assert.NoError(t, err)

	stacks, err := db.ListStacks()
	assert.NoError(t, err)
	assert.Len(t, stacks, 1)
	assert.True(t, stacks[0].IsGitRepo)
	assert.Equal(t, "main", stacks[0].GitBranch)
}

// TestScannerService_ScanAll_PreservesCredentialsAcrossRescan is the
// regression test for agent-os-qll: a scan built a models.Directory with only
// Path/Name/RootDir/IsGitRepo/GitRemote/GitBranch set and passed it to
// UpsertDirectory, which used to be INSERT OR REPLACE over all eleven
// columns — deleting and rewriting the row, wiping any credential an
// operator had saved. A rescan must leave a previously-saved per-directory
// credential intact.
//
// It uses newTestDBWithEncryptor (agent-os-dgj). It previously used
// database.NewWithMigrations, whose nil encryptor stored the token as cleartext
// and read it straight back, so the token assertion below passed without any
// encryption happening. The real path is encrypt-on-write then decrypt-on-read,
// and that is what a rescan has to leave intact.
func TestScannerService_ScanAll_PreservesCredentialsAcrossRescan(t *testing.T) {
	tempDir := t.TempDir()
	stackDir := filepath.Join(tempDir, "my-stack")
	require.NoError(t, os.MkdirAll(stackDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte("services: {}\n"), 0644))

	cfg := &config.Config{StacksDir: tempDir}
	db := newTestDBWithEncryptor(t)

	service := NewScannerService(cfg, db)

	_, err := service.ScanAll()
	require.NoError(t, err)

	require.NoError(t, db.UpdateDirectoryCredentials(stackDir, "https", "", "git-user", "s3cr3t-token"))

	// Rescan. This is the operation the bug report says wipes the row.
	_, err = service.ScanAll()
	require.NoError(t, err)

	cred, err := db.GetDirectoryCredentials(stackDir)
	require.NoError(t, err)
	assert.Equal(t, "https", cred.GitAuthType)
	assert.Equal(t, "git-user", cred.GitHTTPSUser)
	assert.Equal(t, "s3cr3t-token", cred.GitHTTPSToken)
}

func TestScannerService_ScanAll_WithEnvFile(t *testing.T) {
	tempDir := t.TempDir()

	stackDir := filepath.Join(tempDir, "stack-with-env")
	err := os.MkdirAll(stackDir, 0755)
	require.NoError(t, err)

	composePath := filepath.Join(stackDir, "compose.yaml")
	err = os.WriteFile(composePath, []byte("services:\n  web:\n    image: nginx\n"), 0644)
	require.NoError(t, err)

	envPath := filepath.Join(stackDir, ".env")
	err = os.WriteFile(envPath, []byte("DB_HOST=localhost\nDB_PORT=5432\n"), 0644)
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir}
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	service := NewScannerService(cfg, db)

	_, err = service.ScanAll()
	assert.NoError(t, err)

	stacks, err := db.ListStacks()
	assert.NoError(t, err)
	assert.Len(t, stacks, 1)
	assert.Equal(t, ".env", stacks[0].EnvFile)
}

func TestScannerService_ScanAll_WithOverrideFiles(t *testing.T) {
	tempDir := t.TempDir()

	stackDir := filepath.Join(tempDir, "stack-override")
	err := os.MkdirAll(stackDir, 0755)
	require.NoError(t, err)

	composePath := filepath.Join(stackDir, "compose.yaml")
	err = os.WriteFile(composePath, []byte("services:\n  web:\n    image: nginx\n"), 0644)
	require.NoError(t, err)

	overridePath := filepath.Join(stackDir, "compose.override.yaml")
	err = os.WriteFile(overridePath, []byte("services:\n  web:\n    ports:\n      - \"8080:80\"\n"), 0644)
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir}
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	service := NewScannerService(cfg, db)

	_, err = service.ScanAll()
	assert.NoError(t, err)

	stacks, err := db.ListStacks()
	assert.NoError(t, err)
	assert.Len(t, stacks, 1)
}

func TestScannerService_ScanAll_NonDirectoryEntries(t *testing.T) {
	tempDir := t.TempDir()

	filePath := filepath.Join(tempDir, "not-a-dir.txt")
	err := os.WriteFile(filePath, []byte("test"), 0644)
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir}
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	service := NewScannerService(cfg, db)

	_, err = service.ScanAll()
	assert.NoError(t, err)

	dirs, err := db.ListDirectories()
	assert.NoError(t, err)
	assert.Empty(t, dirs)
}

// The scanner is the second stack-ID producer. For a single configured root it
// must keep minting exactly the ID it always has (agent-os-elo is
// collision-only), and two roots sharing a basename must not collapse into one
// row.
func TestScannerService_ScanAll_SingleRootIDIsUnchanged(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "srv", "stacks")
	stackDir := filepath.Join(root, "my-stack")
	require.NoError(t, os.MkdirAll(stackDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte("services:\n  web:\n    image: nginx\n"), 0644))

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = NewScannerService(&config.Config{StacksDir: root}, db).ScanAll()
	require.NoError(t, err)

	stacks, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 1)
	assert.Equal(t, "stacks~my-stack:default", stacks[0].ID)
}

func TestScannerService_ScanAll_SameBasenameRootsGetDistinctIDs(t *testing.T) {
	base := t.TempDir()
	rootA := filepath.Join(base, "a", "stacks")
	rootB := filepath.Join(base, "b", "stacks")
	for _, r := range []string{rootA, rootB} {
		dir := filepath.Join(r, "my-stack")
		require.NoError(t, os.MkdirAll(dir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  web:\n    image: nginx\n"), 0644))
	}

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{StacksDir: rootA, ExtraStacksDirs: []string{rootB}}
	_, err = NewScannerService(cfg, db).ScanAll()
	require.NoError(t, err)

	stacks, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 2, "same-basename roots must not collapse into one stacks row")

	byDir := map[string]string{}
	for _, s := range stacks {
		byDir[s.Directory] = s.ID
	}
	assert.NotEqual(t, byDir[filepath.Join(rootA, "my-stack")], byDir[filepath.Join(rootB, "my-stack")])

	// rootA is the PRIMARY root and keeps the ID it had before rootB existed.
	assert.Equal(t, "stacks~my-stack:default", byDir[filepath.Join(rootA, "my-stack")])
	assert.Regexp(t, `^stacks\.[0-9a-f]{8}~my-stack:default$`, byDir[filepath.Join(rootB, "my-stack")])
}

// The scenario the primary-root exemption exists for, end to end: an operator
// runs with one root, later adds a second root that happens to share its
// basename, and rescans. The primary root's stack must keep its exact ID and
// must not gain a second row.
//
// The duplicate matters more than the rename. pruneStaleStacks removes rows by
// DIRECTORY existence, never by ID, so a re-IDed stack whose directory still
// exists leaves its old row behind while the scan upserts a new one — and the
// six unenforced TEXT columns holding stack ids would still point at the old.
func TestScannerService_AddingCollidingExtraRootDoesNotReIDPrimary(t *testing.T) {
	base := t.TempDir()
	primary := filepath.Join(base, "srv", "stacks")
	extra := filepath.Join(base, "opt", "stacks")
	for _, r := range []string{primary, extra} {
		dir := filepath.Join(r, "web")
		require.NoError(t, os.MkdirAll(dir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  web:\n    image: nginx\n"), 0644))
	}

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Before: the primary root alone.
	cfg := &config.Config{StacksDir: primary}
	_, err = NewScannerService(cfg, db).ScanAll()
	require.NoError(t, err)

	stacks, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 1)
	require.Equal(t, "stacks~web:default", stacks[0].ID, "setup: the pre-existing ID")

	// After: a colliding extra root is configured and everything is rescanned.
	cfg.ExtraStacksDirs = []string{extra}
	_, err = NewScannerService(cfg, db).ScanAll()
	require.NoError(t, err)

	stacks, err = db.ListStacks()
	require.NoError(t, err)

	byDir := map[string]string{}
	for _, st := range stacks {
		byDir[st.Directory] = st.ID
	}
	assert.Len(t, stacks, 2, "one row per stack; a re-ID of the primary would leave a stale third row behind")
	assert.Equal(t, "stacks~web:default", byDir[filepath.Join(primary, "web")],
		"the pre-existing stack was re-IDed by a config change it had nothing to do with")
	assert.Regexp(t, `^stacks\.[0-9a-f]{8}~web:default$`, byDir[filepath.Join(extra, "web")])
}

// Direct coverage of the shared ID producer. The byte-for-byte cases are the
// load-bearing ones: agent-os-elo's disambiguator is collision-only, and
// re-IDing a root that does not collide would strand six unenforced TEXT
// columns and every bookmarked frontend URL.
func TestStackID_PrefixIsBasenameWhenNoCollision(t *testing.T) {
	tests := []struct {
		name        string
		root        string
		primaryRoot string
		extraRoots  []string
		want        string
	}{
		{
			name:        "single configured root",
			root:        "/srv/stacks",
			primaryRoot: "/srv/stacks",
			want:        "stacks~my-stack:default",
		},
		{
			name:        "several roots, all basenames distinct",
			root:        "/srv/stacks",
			primaryRoot: "/srv/stacks",
			extraRoots:  []string{"/mnt/media", "/opt/apps"},
			want:        "stacks~my-stack:default",
		},
		{
			name:        "root spelled with a trailing separator still matches itself",
			root:        "/srv/stacks/",
			primaryRoot: "/srv/stacks",
			want:        "stacks~my-stack:default",
		},
		{
			name:        "extra root whose basename is unique",
			root:        "/opt/apps",
			primaryRoot: "/srv/stacks",
			extraRoots:  []string{"/opt/apps"},
			want:        "apps~my-stack:default",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, StackID(tt.root, tt.primaryRoot, tt.extraRoots, "my-stack", "default"))
		})
	}
}

// THE guard for the primary-root exemption. Because the prefix depends on the
// set of configured roots, adding a root could otherwise re-ID stacks that
// already exist — and pruneStaleStacks keys on directory existence, not on ID,
// so the old row would survive alongside the new one. The primary root is
// exempt unconditionally: the colliding EXTRA root takes the suffix instead.
func TestStackID_PrimaryRootKeepsBareBasenameWhenExtraCollides(t *testing.T) {
	const primary = "/srv/stacks"
	extras := []string{"/opt/stacks"}

	primaryID := StackID(primary, primary, extras, "web", "default")
	extraID := StackID(extras[0], primary, extras, "web", "default")

	assert.Equal(t, "stacks~web:default", primaryID,
		"adding a colliding extra root must not change an ID under the primary root")
	assert.Regexp(t, `^stacks\.[0-9a-f]{8}~web:default$`, extraID)
	assert.NotEqual(t, primaryID, extraID)
}

// Adding extra roots must leave the primary root's ID untouched whether they
// collide or not — the single-root deployment is the overwhelming majority and
// must never be re-IDed by a config edit elsewhere.
func TestStackID_PrimaryRootIDIsStableAcrossConfigGrowth(t *testing.T) {
	const primary = "/srv/stacks"
	const want = "stacks~web:default"

	assert.Equal(t, want, StackID(primary, primary, nil, "web", "default"))
	assert.Equal(t, want, StackID(primary, primary, []string{"/opt/apps"}, "web", "default"))
	assert.Equal(t, want, StackID(primary, primary, []string{"/opt/stacks"}, "web", "default"))
	assert.Equal(t, want, StackID(primary, primary, []string{"/opt/stacks", "/mnt/stacks"}, "web", "default"))
}

// Two EXTRA roots colliding with each other are both suffixed; neither is
// privileged, so both must move off the bare basename to stay distinct.
func TestStackID_CollidingExtraRootsGetDistinctPrefixes(t *testing.T) {
	const primary = "/srv/apps"
	extras := []string{"/a/stacks", "/b/stacks"}

	idA := StackID(extras[0], primary, extras, "my-stack", "default")
	idB := StackID(extras[1], primary, extras, "my-stack", "default")

	assert.NotEqual(t, idA, idB)
	assert.Regexp(t, `^stacks\.[0-9a-f]{8}~my-stack:default$`, idA)
	assert.Regexp(t, `^stacks\.[0-9a-f]{8}~my-stack:default$`, idB)
}

// A colliding root's disambiguated ID must not depend on where the root sits in
// EXTRA_STACKS_DIRS, or editing config order would re-ID stacks.
func TestStackID_DisambiguatorIsIndependentOfRootOrder(t *testing.T) {
	const primary = "/srv/apps"
	forward := StackID("/a/stacks", primary, []string{"/a/stacks", "/b/stacks"}, "my-stack", "default")
	reversed := StackID("/a/stacks", primary, []string{"/b/stacks", "/a/stacks"}, "my-stack", "default")

	assert.Equal(t, forward, reversed)
}

// Only the colliding basename is disambiguated; a unique root sharing the
// config with colliding ones keeps its plain prefix.
func TestStackID_NonCollidingRootUnaffectedByOtherCollisions(t *testing.T) {
	const primary = "/srv/apps"
	extras := []string{"/a/stacks", "/b/stacks", "/srv/compose"}

	assert.Equal(t, "compose~my-stack:default", StackID("/srv/compose", primary, extras, "my-stack", "default"))
}

func TestStackID_NestedPathAndProfile(t *testing.T) {
	assert.Equal(t, "stacks~group-my-stack:testing",
		StackID("/srv/stacks", "/srv/stacks", nil, "group-my-stack", "testing"))
}

// agent-os-509: ScanDirectory(rootDir="") is the watcher's entry point
// (services/watcher.go performRescan -> ScanDirectory), and it resolves which
// configured root a path belongs to itself, via buildDirectoryRecord's
// effectiveRoot fallback. That fallback used to be a bare
// strings.HasPrefix(path, stacksDir) with no path-separator boundary, so a
// primary root of ".../stacks" matched paths under ".../stacks-extra" too —
// the same class of bug the path-traversal guard in stack_crud.go's Create
// (filepath.Rel + ".." check) exists to prevent.
//
// Each test first seeds the CORRECT row via an explicit-root scan (the state
// a prior correct ScanAll or Create would have left), then fires the
// watcher's rootDir="" rescan on the exact same path and asserts it does not
// double-mint a second stacks row under the wrong root, nor corrupt the
// shared directories row's root_dir.
func TestScannerService_ScanDirectory_SiblingPrefixRootIsNotConflated(t *testing.T) {
	base := t.TempDir()
	stacksRoot := filepath.Join(base, "stacks")
	extraRoot := filepath.Join(base, "stacks-extra") // textually prefixed by stacksRoot, NOT a child of it
	webDir := filepath.Join(extraRoot, "web")
	require.NoError(t, os.MkdirAll(stacksRoot, 0755))
	require.NoError(t, os.MkdirAll(webDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(webDir, "compose.yaml"), []byte("services:\n  web:\n    image: nginx\n"), 0644))

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{StacksDir: stacksRoot, ExtraStacksDirs: []string{extraRoot}}
	service := NewScannerService(cfg, db)

	// Seed the correct row, as an explicit-root scan of extraRoot would leave it.
	require.NoError(t, service.ScanDirectoryWithRoot(webDir, extraRoot))

	stacks, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 1)
	correctID := stacks[0].ID
	require.Equal(t, "stacks-extra~web:default", correctID, "setup: the pre-existing correct ID")

	// The watcher's debounced rescan calls ScanDirectory with no rootDir
	// (services/watcher.go performRescan), which is the only path that
	// exercises the effectiveRoot fallback this test targets.
	require.NoError(t, service.ScanDirectory(webDir))

	stacks, err = db.ListStacks()
	require.NoError(t, err)
	assert.Len(t, stacks, 1, "a watcher rescan of a path under stacks-extra must not double-mint a stacks row keyed on the sibling-prefix root stacks")
	if len(stacks) > 0 {
		assert.Equal(t, correctID, stacks[0].ID, "the watcher rescan must not change the stack's ID")
	}

	dir, err := db.GetDirectory(webDir)
	require.NoError(t, err)
	assert.Equal(t, extraRoot, dir.RootDir, "root_dir must not be rewritten to the sibling-prefix root")
}

func TestScannerService_ScanDirectory_NestedRootResolvesToLongestMatch(t *testing.T) {
	base := t.TempDir()
	stacksRoot := filepath.Join(base, "stacks")
	nestedRoot := filepath.Join(stacksRoot, "team") // a child of stacksRoot, ALSO independently configured
	webDir := filepath.Join(nestedRoot, "web")
	require.NoError(t, os.MkdirAll(webDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(webDir, "compose.yaml"), []byte("services:\n  web:\n    image: nginx\n"), 0644))

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{StacksDir: stacksRoot, ExtraStacksDirs: []string{nestedRoot}}
	service := NewScannerService(cfg, db)

	// Seed the correct row: nestedRoot is the more specific containing root,
	// so this is the ID a correct resolution assigns.
	require.NoError(t, service.ScanDirectoryWithRoot(webDir, nestedRoot))

	stacks, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 1)
	correctID := stacks[0].ID
	require.Equal(t, "team~web:default", correctID, "setup: the pre-existing correct ID")

	// Watcher-style rescan with no rootDir: both stacksRoot and nestedRoot
	// contain webDir, so the fallback must pick the LONGEST (most specific)
	// match — nestedRoot — not whichever root sorts first in config.
	require.NoError(t, service.ScanDirectory(webDir))

	stacks, err = db.ListStacks()
	require.NoError(t, err)
	assert.Len(t, stacks, 1, "a watcher rescan under a nested root must not double-mint a stacks row keyed on the outer root")
	if len(stacks) > 0 {
		assert.Equal(t, correctID, stacks[0].ID, "the watcher rescan must resolve to the longest (most specific) matching root")
	}

	dir, err := db.GetDirectory(webDir)
	require.NoError(t, err)
	assert.Equal(t, nestedRoot, dir.RootDir, "root_dir must resolve to the nested root, not the outer stacksRoot")
}

// Regression test for agent-os-ufv: two EXTRA roots that collide with each
// other (agent-os-elo's residual, documented at StackIDRootPrefix's "Known
// residual" comment) both flip from a bare basename to a suffixed one the
// moment the second one is added. pruneStaleStacks removed rows only by
// directory existence, never by ID, so extraA's directory — still on disk,
// still active — kept its stale bare-ID row alongside the freshly-minted
// suffixed one: one stack, two rows.
//
// Step 1 scans with extraA alone (no collision, bare "stacks~web:default").
// Step 2 adds extraB, which shares extraA's basename, and rescans. Only the
// freshly-computed suffixed row for extraA/web must remain.
func TestScannerService_TwoCollidingExtraRootsIDChange_PruneRemovesStaleRow(t *testing.T) {
	base := t.TempDir()
	primary := filepath.Join(base, "primary", "apps") // basename "apps": collides with neither extra
	extraA := filepath.Join(base, "groupA", "stacks")
	extraB := filepath.Join(base, "groupB", "stacks") // same basename as extraA, different path

	require.NoError(t, os.MkdirAll(primary, 0755))

	webA := filepath.Join(extraA, "web")
	require.NoError(t, os.MkdirAll(webA, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(webA, "compose.yaml"), []byte("services:\n  web:\n    image: nginx\n"), 0644))

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg := &config.Config{StacksDir: primary, ExtraStacksDirs: []string{extraA}}

	// Step 1: extraA alone, no collision.
	_, err = NewScannerService(cfg, db).ScanAll()
	require.NoError(t, err)

	preStacks, err := db.ListStacksByDirectory(webA)
	require.NoError(t, err)
	require.Len(t, preStacks, 1)
	require.Equal(t, "stacks~web:default", preStacks[0].ID, "setup: the pre-collision ID")

	// Step 2: extraB is configured, sharing extraA's basename. extraA's ID
	// must now change (see StackIDRootPrefix), and the old row must not
	// survive alongside the new one.
	webB := filepath.Join(extraB, "web")
	require.NoError(t, os.MkdirAll(webB, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(webB, "compose.yaml"), []byte("services:\n  web:\n    image: nginx\n"), 0644))

	cfg.ExtraStacksDirs = []string{extraA, extraB}
	_, err = NewScannerService(cfg, db).ScanAll()
	require.NoError(t, err)

	postStacksA, err := db.ListStacksByDirectory(webA)
	require.NoError(t, err)
	require.Len(t, postStacksA, 1, "extraA's directory must have exactly one row after its ID changed, not a stale row plus a fresh one")
	assert.Regexp(t, `^stacks\.[0-9a-f]{8}~web:default$`, postStacksA[0].ID)
	assert.NotEqual(t, "stacks~web:default", postStacksA[0].ID, "extraA's ID must actually have changed for this test to be meaningful")

	postStacksB, err := db.ListStacksByDirectory(webB)
	require.NoError(t, err)
	require.Len(t, postStacksB, 1)
	assert.Regexp(t, `^stacks\.[0-9a-f]{8}~web:default$`, postStacksB[0].ID)
	assert.NotEqual(t, postStacksA[0].ID, postStacksB[0].ID)

	allStacks, err := db.ListStacks()
	require.NoError(t, err)
	assert.Len(t, allStacks, 2, "one row per stack directory; no stale leftover from the ID change")
}

// Negative counterpart to the test above: pruneStaleStacks must not delete a
// row just because it shares a directory with another stack, or just because
// a rescan happened. Two distinct compose files in one directory is a
// legitimate, unrelated multi-stack layout (agent-os-w8o) — not a stale-ID
// pair — and neither row's ID changes across the rescan, so both must survive
// untouched.
func TestScannerService_RescanWithMultipleStacksInDirectory_AllSurvivePrune(t *testing.T) {
	tempDir := t.TempDir()
	stackDir := filepath.Join(tempDir, "multi-stack")
	require.NoError(t, os.MkdirAll(stackDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte("services:\n  web:\n    image: nginx\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "compose.api.yaml"), []byte("services:\n  api:\n    image: api\n"), 0644))

	cfg := &config.Config{StacksDir: tempDir}
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	service := NewScannerService(cfg, db)

	_, err = service.ScanAll()
	require.NoError(t, err)

	before, err := db.ListStacksByDirectory(stackDir)
	require.NoError(t, err)
	require.Len(t, before, 2)
	idsBefore := map[string]string{}
	for _, s := range before {
		idsBefore[s.ComposeFile] = s.ID
	}

	// Rescan with nothing changed: both rows' IDs are still exactly what the
	// scanner would mint, so pruneStaleStacks must leave them both alone.
	_, err = service.ScanAll()
	require.NoError(t, err)

	after, err := db.ListStacksByDirectory(stackDir)
	require.NoError(t, err)
	require.Len(t, after, 2, "a correct-ID row sharing a directory with another stack must survive a prune")
	for _, s := range after {
		assert.Equal(t, idsBefore[s.ComposeFile], s.ID, "unrelated stack's ID must not change across an uneventful rescan")
	}
}

// Pins the not-visited-this-pass guard in pruneStaleIDStacks (the
// `!hasExpected { continue }` check). Two rows share (Directory, ComposeFile)
// but NEITHER carries the ID the scanner would mint right now -- exactly the
// state a directory is left in when a pass does not visit it (a temporarily
// unreadable compose file, a scan_depth change, a permission error skipping a
// subtree): whatever row existed before stays, and nothing fresh gets
// written, so no row in the group is "proven current".
//
// Neither TestScannerService_TwoCollidingExtraRootsIDChange_PruneRemovesStaleRow
// nor TestScannerService_RescanWithMultipleStacksInDirectory_AllSurvivePrune
// exercises this branch: the former's group always contains a row with the
// expected ID (that's what makes deletion provably safe there), and the
// latter's two rows never share a ComposeFile, so they never reach a group of
// size > 1 in the first place. Without this test, deleting the `!hasExpected`
// guard -- which reads like a redundant early-continue -- passes every other
// test in this file while turning the prune into "delete every row whose ID
// isn't the expected one", live stacks included.
//
// Constructed directly against the DB rather than through ScanAll: a real
// scan would visit stackDir and write the correct-ID row itself, which is
// exactly the state this test must NOT be in.
func TestScannerService_PruneStaleIDStacks_NeitherRowMatchesExpected_BothSurvive(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "srv", "stacks")
	stackDir := filepath.Join(root, "mystack")
	require.NoError(t, os.MkdirAll(stackDir, 0755))

	cfg := &config.Config{StacksDir: root}
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// The directory row a prior (visited) scan would have left behind.
	require.NoError(t, db.UpsertDirectory(models.Directory{
		Path:    stackDir,
		Name:    "mystack",
		RootDir: root,
	}))

	// The ID the scanner would mint right now is StackID(root, root, nil,
	// "mystack", "default") == "stacks~mystack:default" (root is its own
	// primary root, bare basename, no collision). Neither seeded row is that
	// ID -- standing in for two leftovers from before, with no scan pass
	// having (re)written the correct one.
	staleA := "stacks~mystack:default.old-a"
	staleB := "stacks~mystack:default.old-b"
	require.NoError(t, db.UpsertStack(models.Stack{ID: staleA, Directory: stackDir, ComposeFile: "compose.yaml", ProjectName: "mystack"}))
	require.NoError(t, db.UpsertStack(models.Stack{ID: staleB, Directory: stackDir, ComposeFile: "compose.yaml", ProjectName: "mystack"}))

	service := NewScannerService(cfg, db)
	require.NoError(t, service.pruneStaleStacks())

	stacks, err := db.ListStacksByDirectory(stackDir)
	require.NoError(t, err)
	ids := make([]string, 0, len(stacks))
	for _, s := range stacks {
		ids = append(ids, s.ID)
	}
	assert.ElementsMatch(t, []string{staleA, staleB}, ids,
		"neither row carries the ID the scanner would currently mint, so this reads as 'not visited this pass', not 'stale' -- both must survive")
}

// ───────────────────────────────────────────
// agent-os-f3ah: persisted project_name must be what compose derives
// ───────────────────────────────────────────

// writeComposeStack creates <root>/<dirName>/compose.yaml and returns the
// directory path.
func writeComposeStack(t *testing.T, root, dirName string) string {
	t.Helper()
	dir := filepath.Join(root, dirName)
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  web:\n    image: nginx\n"), 0644))
	return dir
}

// TestComposeProjectName_MatchesComposeNormalisation is the PIN for the
// normaliser, not its implementation: ComposeProjectName delegates to
// compose-go's loader.NormalizeProjectName, the function compose itself uses
// to derive a project name from a directory basename, so the two cannot drift.
//
// Before agent-os-f3ah the directory basename was persisted VERBATIM. A
// directory "MyStack" started outside Capstan is labelled "mystack" by
// compose, so Capstan's label filter found 0 containers (status stopped,
// empty metrics) and its `-p MyStack` was rejected by compose on Start.
func TestComposeProjectName_MatchesComposeNormalisation(t *testing.T) {
	cases := []struct {
		dirName, name, want, why string
	}{
		{"MyStack", "default", "mystack", "the filed defect: compose lower-cases"},
		{"my.stack", "default", "mystack", "compose drops the dot; -p my.stack is rejected"},
		{"my_stack", "default", "my_stack", "CONTROL: underscore is legal, byte-for-byte"},
		{"ok-normal", "default", "ok-normal", "CONTROL: already normal, byte-for-byte"},
		{"-leading", "default", "leading", "compose trims leading '-'"},
		{"_leading", "default", "leading", "compose trims leading '_'"},
		{"--x_", "default", "x_", "only LEADING punctuation is trimmed"},
		{"Café", "default", "caf", "compose's class is ASCII; the é is dropped, not transliterated"},
		{"", "default", "", "nothing to derive from"},
		{"---", "default", "", "a name made only of trimmed punctuation normalises to nothing; the scanner skips it and the create endpoint refuses it"},
		{"MyStack", "API", "mystack-api", "the profile suffix is normalised too: -p is passed the whole string"},
		{"myapp", "web:2", "myapp-web-2", "CONTROL: the existing colon rule (agent-os-07x) still applies before normalising"},
		{"myapp", "api", "myapp-api", "CONTROL"},
	}
	for _, tc := range cases {
		if got := ComposeProjectName(tc.dirName, tc.name); got != tc.want {
			t.Errorf("ComposeProjectName(%q, %q):\n  got  = %q\n  want = %q\n  why  = %s", tc.dirName, tc.name, got, tc.want, tc.why)
		}
	}

	// The property the table pins: for the default profile the result IS
	// compose's own derivation, byte-for-byte.
	for _, dirName := range []string{"MyStack", "my.stack", "my_stack", "ok-normal", "-leading", "Café", "---", "A.B-C_d"} {
		if got, want := ComposeProjectName(dirName, "default"), loader.NormalizeProjectName(dirName); got != want {
			t.Errorf("ComposeProjectName(%q) = %q but compose derives %q", dirName, got, want)
		}
	}
}

// TestScannerService_ScanAll_RewritesALegacyProjectName pins the migration
// story: UpsertStack is INSERT OR REPLACE keyed on the stack ID, and the ID
// carries no project_name, so the first scan after the fix rewrites an
// existing "MyStack" row in place. No migration, no new row.
func TestScannerService_ScanAll_RewritesALegacyProjectName(t *testing.T) {
	tempDir := t.TempDir()
	stackDir := writeComposeStack(t, tempDir, "MyStack")

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Seed the row exactly as the pre-fix scanner persisted it.
	id := StackID(tempDir, tempDir, nil, "MyStack", "default")
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: stackDir, Name: "MyStack", RootDir: tempDir}))
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: id, Directory: stackDir, ComposeFile: "compose.yaml", ProjectName: "MyStack", Status: "unknown",
	}))

	_, err = NewScannerService(&config.Config{StacksDir: tempDir}, db).ScanAll()
	require.NoError(t, err)

	row, err := db.GetStack(id)
	require.NoError(t, err, "the legacy row must keep its ID; a re-ID would orphan every reference to it")
	assert.Equal(t, "mystack", row.ProjectName, "the next scan must rewrite the legacy value in place")

	all, err := db.ListStacks()
	require.NoError(t, err)
	assert.Len(t, all, 1, "one directory, one row: the rewrite must not leave the legacy row beside a new one")
}

// newLabelFilteringDockerAPI serves a Docker Engine API double that answers
// GET /containers/json honouring the `label` filter the way the daemon does,
// and returns a DockerService whose real SDK client points at it. That drives
// GetContainerList's actual filter path (docker.go), not a stub of it.
//
// Every container carries label com.docker.compose.project=<project>.
func newLabelFilteringDockerAPI(t *testing.T, containers map[string]string) *DockerService {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/containers/json") {
			http.NotFound(w, r)
			return
		}
		var f map[string]map[string]bool
		_ = json.Unmarshal([]byte(r.URL.Query().Get("filters")), &f)
		want := ""
		for k := range f["label"] {
			want = strings.TrimPrefix(k, "com.docker.compose.project=")
		}
		out := make([]map[string]any, 0)
		for name, project := range containers {
			if project != want {
				continue
			}
			out = append(out, map[string]any{
				"Id": name, "Names": []string{"/" + name}, "Image": "nginx", "State": "running", "Status": "Up",
				"Labels": map[string]string{"com.docker.compose.project": project},
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)

	cli, err := client.NewClientWithOpts(
		client.WithHost("tcp://"+strings.TrimPrefix(srv.URL, "http://")),
		client.WithVersion("1.44"),
	)
	require.NoError(t, err)
	return &DockerService{config: &config.Config{}, client: cli}
}

// TestScannerService_ScanAll_NormalisedNameFindsTheLiveContainers is the
// POSITIVE arm end to end inside the unit suite: a directory "MyStack" whose
// containers were started outside Capstan carry label
// com.docker.compose.project=mystack. After the scan, the stored row's
// project name must be what the label filter needs to find them.
func TestScannerService_ScanAll_NormalisedNameFindsTheLiveContainers(t *testing.T) {
	tempDir := t.TempDir()
	writeComposeStack(t, tempDir, "MyStack")

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = NewScannerService(&config.Config{StacksDir: tempDir}, db).ScanAll()
	require.NoError(t, err)
	stacks, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 1)

	docker := newLabelFilteringDockerAPI(t, map[string]string{
		"mystack-alpha-1": "mystack",
		"mystack-beta-1":  "mystack",
		"other-web-1":     "other",
	})

	// The instrument discriminates: the value the pre-fix scanner persisted
	// finds nothing, which is exactly the "stopped, 0 containers" symptom.
	pre, err := docker.GetContainerList("MyStack")
	require.NoError(t, err)
	require.Empty(t, pre, "control: the un-normalised name must find nothing, or the filter double is not filtering")

	got, err := docker.GetContainerList(stacks[0].ProjectName)
	require.NoError(t, err)
	names := make([]string, 0, len(got))
	for _, c := range got {
		names = append(names, c.Name)
	}
	assert.ElementsMatch(t, []string{"mystack-alpha-1", "mystack-beta-1"}, names,
		"stored project_name %q must match the compose label so the stack's containers are visible", stacks[0].ProjectName)
}

// TestScannerService_ScanAll_AlreadyNormalNameIsUntouched is the NEGATIVE arm:
// a directory compose would not rename is persisted byte-for-byte and a rescan
// leaves the whole row identical (the stacks table has no updated_at, so the
// row itself is the evidence).
func TestScannerService_ScanAll_AlreadyNormalNameIsUntouched(t *testing.T) {
	tempDir := t.TempDir()
	writeComposeStack(t, tempDir, "ok-normal")

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	svc := NewScannerService(&config.Config{StacksDir: tempDir}, db)

	_, err = svc.ScanAll()
	require.NoError(t, err)
	id := StackID(tempDir, tempDir, nil, "ok-normal", "default")
	first, err := db.GetStack(id)
	require.NoError(t, err)
	assert.Equal(t, "ok-normal", first.ProjectName)

	_, err = svc.ScanAll()
	require.NoError(t, err)
	second, err := db.GetStack(id)
	require.NoError(t, err)
	assert.Equal(t, *first, *second, "a rescan of an already-normal directory must not churn the row")
}

// TestScannerService_ScanAll_CollidingDirectoriesArePersistedAndWarned is the
// COLLISION arm: "MyStack" and "my.stack" both normalise to "mystack". Compose
// itself would run them as ONE project, so Capstan cannot separate them; it
// persists both rows (distinct IDs, since the ID is path-based) and logs one
// WARN naming the shared project and both directories.
func TestScannerService_ScanAll_CollidingDirectoriesArePersistedAndWarned(t *testing.T) {
	tempDir := t.TempDir()
	dirA := writeComposeStack(t, tempDir, "MyStack")
	dirB := writeComposeStack(t, tempDir, "my.stack")
	// A bystander: must not appear in the warning.
	writeComposeStack(t, tempDir, "ok-normal")

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	logs := captureSlog(t)
	_, err = NewScannerService(&config.Config{StacksDir: tempDir}, db).ScanAll()
	require.NoError(t, err)

	stacks, err := db.ListStacks()
	require.NoError(t, err)
	byDir := map[string]string{}
	for _, s := range stacks {
		byDir[s.Directory] = s.ProjectName
	}
	assert.Equal(t, "mystack", byDir[dirA])
	assert.Equal(t, "mystack", byDir[dirB], "both colliding directories must be persisted; dropping one silently hides a directory")
	assert.Len(t, stacks, 3)

	out := logs.String()
	warnLines := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "level=WARN") && strings.Contains(line, "collision") {
			warnLines++
			assert.Contains(t, line, "mystack")
			assert.Contains(t, line, dirA)
			assert.Contains(t, line, dirB)
			assert.NotContains(t, line, "ok-normal", "the bystander is not part of the collision")
		}
	}
	assert.Equal(t, 1, warnLines, "exactly one collision warning, naming the shared project and both directories; log was:\n%s", out)
}

// TestScannerService_ScanAll_EmptyProjectNameIsSkippedWithAWarning: a directory
// whose name normalises to nothing ("---") cannot be a compose project at all
// (compose refuses an empty project name), so no row is written and a WARN
// names the directory. Note the scanner already skips dot-prefixed entries, so
// "..." never reaches this rule; "---" does.
func TestScannerService_ScanAll_EmptyProjectNameIsSkippedWithAWarning(t *testing.T) {
	tempDir := t.TempDir()
	dir := writeComposeStack(t, tempDir, "---")

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	logs := captureSlog(t)
	_, err = NewScannerService(&config.Config{StacksDir: tempDir}, db).ScanAll()
	require.NoError(t, err)

	stacks, err := db.ListStacks()
	require.NoError(t, err)
	assert.Empty(t, stacks, "an empty project_name is unlistable and unstartable; the row must not be written")
	assert.Contains(t, logs.String(), "level=WARN")
	assert.Contains(t, logs.String(), dir, "the warning must name the directory so the operator can rename it")
}
