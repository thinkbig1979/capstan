package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewLinterService(t *testing.T) {
	service := NewLinterService()

	assert.NotNil(t, service)
}

func TestLinterService_Lint_ValidCompose(t *testing.T) {
	service := NewLinterService()

	content := `services:
  web:
    image: nginx:1.21
    restart: unless-stopped
    deploy:
      resources:
        limits:
          cpus: '0.5'
          memory: 512M
`

	results, err := service.Lint(content)
	assert.NoError(t, err)
	assert.Len(t, results, 0)
}

func TestLinterService_Lint_MissingImage(t *testing.T) {
	service := NewLinterService()

	content := `services:
  web:
    restart: unless-stopped
    deploy:
      resources:
        limits:
          cpus: '0.5'
          memory: 512M
`

	results, err := service.Lint(content)
	assert.NoError(t, err)
	assert.Len(t, results, 1)

	assert.Equal(t, "error", results[0].Level)
	assert.Equal(t, "compose-structure", results[0].Rule)
	assert.Contains(t, results[0].Message, "neither an image nor a build")
}

func TestLinterService_Lint_LatestTag(t *testing.T) {
	service := NewLinterService()

	content := `services:
  web:
    image: nginx:latest
    restart: unless-stopped
    deploy:
      resources:
        limits:
          cpus: '0.5'
          memory: 512M
`

	results, err := service.Lint(content)
	assert.NoError(t, err)
	assert.Len(t, results, 1)

	assert.Equal(t, "warning", results[0].Level)
	assert.Equal(t, "latest-tag", results[0].Rule)
	assert.Contains(t, results[0].Message, ":latest tag")
}

func TestLinterService_Lint_NoRestartPolicy(t *testing.T) {
	service := NewLinterService()

	content := `services:
  web:
    image: nginx:1.21
    deploy:
      resources:
        limits:
          cpus: '0.5'
          memory: 512M
`

	results, err := service.Lint(content)
	assert.NoError(t, err)
	assert.Len(t, results, 1)

	assert.Equal(t, "warning", results[0].Level)
	assert.Equal(t, "no-restart-policy", results[0].Rule)
	assert.Contains(t, results[0].Message, "no restart policy")
}

func TestLinterService_Lint_PrivilegedMode(t *testing.T) {
	service := NewLinterService()

	content := `services:
  web:
    image: nginx:1.21
    privileged: true
    restart: unless-stopped
    deploy:
      resources:
        limits:
          cpus: '0.5'
          memory: 512M
`

	results, err := service.Lint(content)
	assert.NoError(t, err)
	assert.Len(t, results, 1)

	assert.Equal(t, "warning", results[0].Level)
	assert.Equal(t, "privileged-mode", results[0].Rule)
	assert.Contains(t, results[0].Message, "privileged mode")
}

func TestLinterService_Lint_HostNetwork(t *testing.T) {
	service := NewLinterService()

	content := `services:
  web:
    image: nginx:1.21
    network_mode: host
    restart: unless-stopped
    deploy:
      resources:
        limits:
          cpus: '0.5'
          memory: 512M
`

	results, err := service.Lint(content)
	assert.NoError(t, err)
	assert.Len(t, results, 1)

	assert.Equal(t, "info", results[0].Level)
	assert.Equal(t, "host-network", results[0].Rule)
	assert.Contains(t, results[0].Message, "host network mode")
}

func TestLinterService_Lint_NoResourceLimits(t *testing.T) {
	service := NewLinterService()

	content := `services:
  web:
    image: nginx:1.21
    restart: unless-stopped
`

	results, err := service.Lint(content)
	assert.NoError(t, err)

	assert.Len(t, results, 1)
	assert.Equal(t, "info", results[0].Level)
	assert.Equal(t, "no-resource-limits", results[0].Rule)
	assert.Contains(t, results[0].Message, "no resource limits")
}

func TestLinterService_Lint_MultipleIssues(t *testing.T) {
	service := NewLinterService()

	content := `services:
  web:
    image: nginx:latest
  db:
    image: postgres:latest
    privileged: true
`

	results, err := service.Lint(content)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(results), 4)

	rules := make(map[string]bool)
	for _, r := range results {
		rules[r.Rule] = true
	}

	assert.True(t, rules["latest-tag"])
	assert.True(t, rules["privileged-mode"])
	assert.True(t, rules["no-restart-policy"])
	assert.True(t, rules["no-resource-limits"])
}

func TestLinterService_Lint_InvalidYAML(t *testing.T) {
	service := NewLinterService()

	content := `services:
  web:
    image: nginx:latest
    invalid: [unclosed list
`

	results, err := service.Lint(content)
	assert.NoError(t, err)
	assert.Len(t, results, 1)

	assert.Equal(t, "error", results[0].Level)
	assert.Equal(t, "yaml-syntax", results[0].Rule)
}

func TestLinterService_Lint_ResultSorting(t *testing.T) {
	service := NewLinterService()

	content := `services:
  web:
    image: nginx:latest
  db:
    image: postgres:latest
    privileged: true
  cache:
    image: redis
    restart: always
`

	results, err := service.Lint(content)
	assert.NoError(t, err)
	assert.Greater(t, len(results), 0)

	for i := 1; i < len(results); i++ {
		levelOrder := map[string]int{
			"error":   0,
			"warning": 1,
			"info":    2,
		}

		prevLevel := levelOrder[results[i-1].Level]
		currLevel := levelOrder[results[i].Level]

		if prevLevel != currLevel {
			assert.Less(t, prevLevel, currLevel, "Results should be sorted by severity")
		}
	}
}

func TestLinterService_isLatestTag(t *testing.T) {
	tests := []struct {
		image  string
		isLate bool
	}{
		{"nginx:latest", true},
		{"nginx:1.21", false},
		{"nginx", true},
		{"postgres:14-alpine", false},
		{"myregistry.com/image:latest", true},
	}

	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			service := NewLinterService()
			result := service.isLatestTag(tt.image)
			assert.Equal(t, tt.isLate, result)
		})
	}
}

func TestLinterService_findServiceLine(t *testing.T) {
	service := NewLinterService()

	content := `version: '3.8'

services:
  web:
    image: nginx
  db:
    image: postgres

volumes:
  data:
`

	line := service.findServiceLine(content, "web")
	assert.Equal(t, 4, line)

	line = service.findServiceLine(content, "db")
	assert.Equal(t, 6, line)

	line = service.findServiceLine(content, "notfound")
	assert.Equal(t, 1, line)
}

func TestExtractLineNumber(t *testing.T) {
	tests := []struct {
		errMsg string
		line   int
	}{
		{"error at line 15: invalid syntax", 15},
		{"line 42: unexpected token", 42},
		{"no line here", 1},
		{"line 100: multiple errors", 100},
	}

	for _, tt := range tests {
		t.Run(tt.errMsg, func(t *testing.T) {
			line := extractLineNumber(tt.errMsg)
			assert.Equal(t, tt.line, line)
		})
	}
}

func TestLintResult_LevelPriority(t *testing.T) {
	service := NewLinterService()

	content := `services:
  web:
    image: nginx:latest
  db:
    image: postgres:latest
    privileged: true
`

	results, err := service.Lint(content)
	assert.NoError(t, err)

	levelOrder := make(map[string]int)
	for i, r := range results {
		levelOrder[r.Level] = i
	}

	assert.Less(t, levelOrder["warning"], levelOrder["info"], "Warnings should come before info")
}
