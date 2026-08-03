package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
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
}

// Direct coverage of the shared ID producer. The byte-for-byte cases are the
// load-bearing ones: agent-os-elo's disambiguator is collision-only, and
// re-IDing a root that does not collide would strand six unenforced TEXT
// columns and every bookmarked frontend URL.
func TestStackID_PrefixIsBasenameWhenNoCollision(t *testing.T) {
	tests := []struct {
		name     string
		root     string
		allRoots []string
		want     string
	}{
		{
			name:     "single configured root",
			root:     "/srv/stacks",
			allRoots: []string{"/srv/stacks"},
			want:     "stacks~my-stack:default",
		},
		{
			name:     "several roots, all basenames distinct",
			root:     "/srv/stacks",
			allRoots: []string{"/srv/stacks", "/mnt/media", "/opt/apps"},
			want:     "stacks~my-stack:default",
		},
		{
			name:     "root spelled with a trailing separator still matches itself",
			root:     "/srv/stacks/",
			allRoots: []string{"/srv/stacks"},
			want:     "stacks~my-stack:default",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, StackID(tt.root, tt.allRoots, "my-stack", "default"))
		})
	}
}

func TestStackID_CollidingRootsGetDistinctPrefixes(t *testing.T) {
	roots := []string{"/a/stacks", "/b/stacks"}

	idA := StackID(roots[0], roots, "my-stack", "default")
	idB := StackID(roots[1], roots, "my-stack", "default")

	assert.NotEqual(t, idA, idB)
	assert.Regexp(t, `^stacks\.[0-9a-f]{8}~my-stack:default$`, idA)
	assert.Regexp(t, `^stacks\.[0-9a-f]{8}~my-stack:default$`, idB)
}

// A colliding root's disambiguated ID must not depend on where the root sits in
// EXTRA_STACKS_DIRS, or editing config order would re-ID stacks.
func TestStackID_DisambiguatorIsIndependentOfRootOrder(t *testing.T) {
	forward := StackID("/a/stacks", []string{"/a/stacks", "/b/stacks"}, "my-stack", "default")
	reversed := StackID("/a/stacks", []string{"/b/stacks", "/a/stacks"}, "my-stack", "default")

	assert.Equal(t, forward, reversed)
}

// Only the colliding basename is disambiguated; a unique root sharing the
// config with colliding ones keeps its plain prefix.
func TestStackID_NonCollidingRootUnaffectedByOtherCollisions(t *testing.T) {
	roots := []string{"/a/stacks", "/b/stacks", "/srv/compose"}

	assert.Equal(t, "compose~my-stack:default", StackID("/srv/compose", roots, "my-stack", "default"))
}

func TestStackID_NestedPathAndProfile(t *testing.T) {
	roots := []string{"/srv/stacks"}

	assert.Equal(t, "stacks~group-my-stack:testing", StackID("/srv/stacks", roots, "group-my-stack", "testing"))
}
