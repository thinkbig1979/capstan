package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
