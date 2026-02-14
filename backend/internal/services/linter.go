package services

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker-manager/backend/internal/models"
	"gopkg.in/yaml.v3"
)

type LinterService struct{}

func NewLinterService() *LinterService {
	return &LinterService{}
}

func (l *LinterService) Lint(content string) ([]models.LintResult, error) {
	var results []models.LintResult

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(content), &parsed); err != nil {
		var yamlErr *yaml.TypeError
		if errors.As(err, &yamlErr) {
			for _, e := range yamlErr.Errors {
				results = append(results, models.LintResult{
					Level:   "error",
					Line:    extractLineNumber(e),
					Message: e,
					Rule:    "yaml-syntax",
				})
			}
			return results, nil
		}

		line := extractLineNumber(err.Error())
		return []models.LintResult{{
			Level:   "error",
			Line:    line,
			Message: err.Error(),
			Rule:    "yaml-syntax",
		}}, nil
	}

	configDetails := types.ConfigDetails{
		ConfigFiles: []types.ConfigFile{
			{
				Content:  []byte(content),
				Filename: "docker-compose.yml",
			},
		},
		WorkingDir: "/tmp",
	}

	composeConfig, err := loader.LoadWithContext(context.Background(), configDetails, func(o *loader.Options) {
		o.SetProjectName("compose-linter", true)
	})
	if err != nil {
		composeErr := err.Error()
		line := extractLineNumber(composeErr)

		results = append(results, models.LintResult{
			Level:   "error",
			Line:    line,
			Message: composeErr,
			Rule:    "compose-structure",
		})
		return results, nil
	}

	for serviceName, service := range composeConfig.Services {
		line := l.findServiceLine(content, serviceName)

		if service.Image == "" && service.Build == nil {
			results = append(results, models.LintResult{
				Level:   "error",
				Line:    line,
				Message: fmt.Sprintf("Service '%s' has neither 'image' nor 'build' specified", serviceName),
				Rule:    "missing-image",
			})
		}

		if service.Image != "" {
			if l.isLatestTag(service.Image) {
				results = append(results, models.LintResult{
					Level:   "warning",
					Line:    line,
					Message: fmt.Sprintf("Service '%s' uses :latest tag (image: %s)", serviceName, service.Image),
					Rule:    "latest-tag",
				})
			}
		}

		if service.Restart == "" {
			results = append(results, models.LintResult{
				Level:   "warning",
				Line:    line,
				Message: fmt.Sprintf("Service '%s' has no restart policy specified", serviceName),
				Rule:    "no-restart-policy",
			})
		}

		if service.Privileged {
			results = append(results, models.LintResult{
				Level:   "warning",
				Line:    line,
				Message: fmt.Sprintf("Service '%s' uses privileged mode", serviceName),
				Rule:    "privileged-mode",
			})
		}

		if service.NetworkMode == "host" {
			results = append(results, models.LintResult{
				Level:   "info",
				Line:    line,
				Message: fmt.Sprintf("Service '%s' uses host network mode", serviceName),
				Rule:    "host-network",
			})
		}

		if service.Deploy == nil || service.Deploy.Resources.Limits == nil {
			results = append(results, models.LintResult{
				Level:   "info",
				Line:    line,
				Message: fmt.Sprintf("Service '%s' has no resource limits specified", serviceName),
				Rule:    "no-resource-limits",
			})
		}
	}

	l.sortResults(results)

	if len(results) == 0 {
		return []models.LintResult{}, nil
	}

	return results, nil
}

func (l *LinterService) isLatestTag(image string) bool {
	parts := strings.Split(image, ":")
	if len(parts) == 1 {
		return true
	}
	tag := parts[len(parts)-1]
	return tag == "latest"
}

func (l *LinterService) findServiceLine(content, serviceName string) int {
	lines := strings.Split(content, "\n")
	servicePattern := serviceName + ":"

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, servicePattern) {
			return i + 1
		}
	}

	return 1
}

func (l *LinterService) sortResults(results []models.LintResult) {
	sort.Slice(results, func(i, j int) bool {
		levelOrder := map[string]int{
			"error":   0,
			"warning": 1,
			"info":    2,
		}

		levelI := levelOrder[results[i].Level]
		levelJ := levelOrder[results[j].Level]

		if levelI != levelJ {
			return levelI < levelJ
		}

		return results[i].Line < results[j].Line
	})
}

func extractLineNumber(errMsg string) int {
	var line int
	_, err := fmt.Sscanf(errMsg, "line %d", &line)
	if err == nil && line > 0 {
		return line
	}

	idx := strings.Index(strings.ToLower(errMsg), "line ")
	if idx != -1 {
		afterLine := errMsg[idx+5:]
		for i, c := range afterLine {
			if c >= '0' && c <= '9' {
				line = line*10 + int(c-'0')
			} else {
				break
			}
			if i > 10 {
				break
			}
		}
	}

	if line > 0 {
		return line
	}

	return 1
}
