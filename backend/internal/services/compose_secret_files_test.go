package services

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memComposeSource serves compose files from a map of slash paths.
func memComposeSource(files map[string]string) composeSource {
	return composeSource{
		read: func(p string) ([]byte, bool, error) {
			c, ok := files[p]
			return []byte(c), ok, nil
		},
		place: func(dir, entry string) (string, bool, error) {
			if path.IsAbs(entry) {
				return "", false, nil
			}
			p, ok := repoJoin(dir, entry)
			return p, ok, nil
		},
		dir: path.Dir,
	}
}

func TestComposeSecretFiles(t *testing.T) {
	t.Run("every shape that names a file", func(t *testing.T) {
		named, files, err := composeSecretFiles(memComposeSource(map[string]string{
			"app/compose.yaml": "include:\n  - inc/db.yaml\n  - path: [c.yaml]\n    env_file: [i.env.conf]\n" +
				"services:\n  a:\n    env_file: a.conf\n    extends: {file: ../base.yaml, service: b}\n  b:\n    env_file: [x.conf, {path: y.conf}]\n  c:\n    extends: b\n" +
				"secrets:\n  s:\n    file: ./s.txt\n  e:\n    environment: E\n",
			"app/inc/db.yaml": "services:\n  db:\n    env_file: db.conf\n",
			"base.yaml":       "services:\n  b:\n    env_file: base.conf\n",
		}), "app/compose.yaml")
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"app/a.conf", "app/x.conf", "app/y.conf", "app/s.txt", "app/i.env.conf", "app/inc/db.conf", "base.conf"}, named)
		assert.ElementsMatch(t, []string{"app/compose.yaml", "app/inc/db.yaml", "base.yaml"}, files, "c.yaml is missing, so it names nothing")
	})
	t.Run("a missing root names nothing", func(t *testing.T) {
		named, files, err := composeSecretFiles(memComposeSource(nil), "compose.yaml")
		require.NoError(t, err)
		assert.Empty(t, named)
		assert.Empty(t, files)
	})
	for name, body := range map[string]string{
		"interpolated env_file":   "services:\n  a:\n    env_file: ${F}\n",
		"interpolated secret":     "secrets:\n  s:\n    file: $S\n",
		"project_directory":       "include:\n  - path: x.yaml\n    project_directory: ..\n",
		"remote include":          "include:\n  - oci://example.invalid/x\n",
		"include not a list":      "include: x.yaml\n",
		"extends file outside":    "services:\n  a:\n    extends: {file: /etc/x.yaml, service: b}\n",
		"include outside":         "include:\n  - ../../x.yaml\n",
		"env_file of a mapping":   "services:\n  a:\n    env_file: {path: x}\n",
		"extends of another kind": "services:\n  a:\n    extends: [x]\n",
	} {
		t.Run(name+" is an error", func(t *testing.T) {
			_, _, err := composeSecretFiles(memComposeSource(map[string]string{"compose.yaml": body}), "compose.yaml")
			assert.Error(t, err)
		})
	}
	t.Run("an include chain past the bound is an error, a cycle is not", func(t *testing.T) {
		files := map[string]string{"a.yaml": "include: [b.yaml]\n", "b.yaml": "include: [a.yaml]\n"}
		_, _, err := composeSecretFiles(memComposeSource(files), "a.yaml")
		require.NoError(t, err)
		long := map[string]string{}
		for i := 0; i <= maxComposeFiles; i++ {
			long[fmt.Sprintf("f%d.yaml", i)] = fmt.Sprintf("include: [f%d.yaml]\n", i+1)
		}
		_, _, err = composeSecretFiles(memComposeSource(long), "f0.yaml")
		require.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), "more than"), err)
	})
}

// The disk source reads only inside the repository and only bounded regular
// files, so a compose file can't make Capstan read any file the server can.
func TestDiskComposeSourceBounds(t *testing.T) {
	base := t.TempDir()
	top := filepath.Join(base, "repo")
	writeFile(t, filepath.Join(base, "outside.yaml"), "services:\n  o:\n    env_file: o.conf\n")
	writeFile(t, filepath.Join(top, "compose.yaml"), "include:\n  - ../outside.yaml\n")
	writeFile(t, filepath.Join(top, "big.yaml"), "services: {}\n"+strings.Repeat("#", maxComposeBytes))
	writeFile(t, filepath.Join(top, "ok.yaml"), "services:\n  a:\n    env_file: a.conf\n")

	_, _, err := composeSecretFiles(diskComposeSource(top), filepath.Join(top, "compose.yaml"))
	assert.ErrorContains(t, err, "outside the repository")
	_, _, err = composeSecretFiles(diskComposeSource(top), filepath.Join(top, "big.yaml"))
	assert.ErrorContains(t, err, "at most")
	named, _, err := composeSecretFiles(diskComposeSource(top), filepath.Join(top, "ok.yaml"))
	require.NoError(t, err, "control: a file inside, under the bound")
	assert.Equal(t, []string{filepath.Join(top, "a.conf")}, named)
}
