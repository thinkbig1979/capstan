package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/template"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"gopkg.in/yaml.v3"
)

type LinterService struct{}

func NewLinterService() *LinterService {
	return &LinterService{}
}

// LintCompose lints content as `docker compose` would load it from workingDir
// with env as its interpolation environment (agent-os-siou). There is no
// variant without an env: linting with none is what made every file using
// ${VAR:?msg} unsavable, so a caller has to state which env it means.
//
// What is a fact about the environment rather than the file is a warning, not
// an error, because the value often lands in a separate step (the env editor,
// global env): a required variable with no value ("required-variable"), an
// env_file that is not on disk yet ("missing-env-file"), env files that could
// not be read ("env-unresolved").
//
// Every message is redacted with the sensitive values of env, because
// compose-go quotes interpolated values in its errors (an invalid cpus quotes
// ${DB_PASSWORD}'s value), and these results reach sessions that have not
// unlocked env values, 422 bodies and logs.
func (l *LinterService) LintCompose(content, workingDir string, env LintEnv) ([]models.LintResult, error) {
	results, err := l.lintCompose(content, workingDir, env)
	if err != nil {
		return nil, err
	}
	secrets := env.secretValues()
	for i := range results {
		results[i].Message = redactComposeOutput(results[i].Message, secrets)
	}
	return results, nil
}

func (l *LinterService) lintCompose(content, workingDir string, env LintEnv) ([]models.LintResult, error) {
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

	if env.Unresolved {
		results = append(results, models.LintResult{
			Level:   "warning",
			Line:    1,
			Message: "The stack's env files could not be read, so variables were checked against no values. Deploy reads the same files and may fail; check the env file's syntax.",
			Rule:    "env-unresolved",
		})
	}

	load := func(skipInterpolation bool) (*types.Project, error) {
		return loader.LoadWithContext(context.Background(), types.ConfigDetails{
			ConfigFiles: []types.ConfigFile{{Content: []byte(content), Filename: "docker-compose.yml"}},
			WorkingDir:  workingDir,
			Environment: env.Vars,
		}, func(o *loader.Options) {
			o.SetProjectName("compose-linter", true)
			// env_file contents are not needed to lint, and a file the
			// create request has not written yet must not fail the load.
			// A missing one is reported as a warning below.
			o.SkipResolveEnvironment = true
			o.SkipInterpolation = skipInterpolation
		})
	}

	composeConfig, err := load(false)
	var missing *template.MissingRequiredError
	if err != nil && errors.As(err, &missing) {
		for _, name := range missingRequiredVars(content, env.Vars, missing.Variable) {
			results = append(results, models.LintResult{
				Level:   "warning",
				Line:    findVarLine(content, name),
				Message: fmt.Sprintf("Required variable %s has no value. Deploy will fail until %s is set in the stack's .env or the global environment.", name, name),
				Rule:    "required-variable",
			})
		}
		// Load again uninterpolated, so the service checks still run.
		composeConfig, err = load(true)
	}
	if err != nil {
		composeErr := err.Error()
		line := extractLineNumber(composeErr)

		results = append(results, models.LintResult{
			Level:   "error",
			Line:    line,
			Message: composeErr,
			Rule:    "compose-structure",
		})
		l.sortResults(results)
		return results, nil
	}

	results = append(results, l.missingEnvFiles(content, workingDir, composeConfig)...)

	for serviceName, service := range composeConfig.Services {
		line := l.findServiceLine(content, serviceName)

		if service.Image == "" && service.Build == nil {
			results = append(results, models.LintResult{
				Level:   "error",
				Line:    line,
				Message: fmt.Sprintf("Service '%s' has neither 'image' nor 'build' specified. Docker Compose needs at least one to create the container.", serviceName),
				Rule:    "missing-image",
			})
		}

		if service.Image != "" {
			if l.isLatestTag(service.Image) {
				results = append(results, models.LintResult{
					Level:   "warning",
					Line:    line,
					Message: fmt.Sprintf("Service '%s' uses :latest tag (image: %s). The 'latest' tag is mutable and can point to different images over time, leading to unpredictable deployments. Consider pinning to a specific version tag.", serviceName, service.Image),
					Rule:    "latest-tag",
				})
			}
		}

		if service.Restart == "" {
			results = append(results, models.LintResult{
				Level:   "warning",
				Line:    line,
				Message: fmt.Sprintf("Service '%s' has no restart policy. Without one, the container won't automatically restart on failure or host reboot. Consider adding `restart: unless-stopped` or `restart: always`.", serviceName),
				Rule:    "no-restart-policy",
			})
		}

		if service.Privileged {
			results = append(results, models.LintResult{
				Level:   "warning",
				Line:    line,
				Message: fmt.Sprintf("Service '%s' runs in privileged mode, which grants full host access and bypasses container isolation. This is a security risk. Use specific capabilities (cap_add) instead if possible.", serviceName),
				Rule:    "privileged-mode",
			})
		}

		if service.NetworkMode == "host" {
			results = append(results, models.LintResult{
				Level:   "info",
				Line:    line,
				Message: fmt.Sprintf("Service '%s' uses host network mode, which shares the host's network stack and exposes all ports without mapping. Ensure this is intentional.", serviceName),
				Rule:    "host-network",
			})
		}

		if service.Deploy == nil || service.Deploy.Resources.Limits == nil {
			results = append(results, models.LintResult{
				Level:   "info",
				Line:    line,
				Message: fmt.Sprintf("Service '%s' has no resource limits. Without limits, a single service can consume all available CPU/memory, affecting other containers on the host.", serviceName),
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
	_, err := fmt.Sscanf(errMsg, "line %d", &line) //geterrors:ignore Sscanf failing IS "this message has no leading \"line N\"", and the manual scan below is the declared fallback
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

// LintEnv is the interpolation environment a compose file is linted with.
type LintEnv struct {
	// Vars are the variables compose would interpolate with.
	Vars map[string]string
	// Unresolved is true when the env files could not be read; Vars then
	// holds what could be resolved without them.
	Unresolved bool
}

// StackLintEnv is the environment `docker compose` interpolates the stack's
// compose file with: composeEnvironment, the exact mode deploy uses.
func StackLintEnv(st models.Stack, globalEnvPath string) LintEnv {
	vars, err := composeEnvironment(st, globalEnvPath)
	if err != nil {
		// err is neither logged nor returned: compose-go quotes the offending
		// line, which can be a secret (resolveComposeEnv's doc comment).
		return LintEnv{Vars: processComposeEnv(), Unresolved: true}
	}
	return LintEnv{Vars: vars}
}

// NewLintEnv is the environment for a stack whose env file is not on disk:
// global.env with envText (the env file the request will write) laid over it.
// envText "" lints with global.env alone.
func NewLintEnv(globalEnvPath, envText string) LintEnv {
	envs, err := resolveComposeEnv(models.Stack{}, globalEnvPath, composeEnvExact)
	if err != nil {
		return LintEnv{Vars: processComposeEnv(), Unresolved: true}.WithEnvText(envText)
	}
	return LintEnv{Vars: envs[0]}.WithEnvText(envText)
}

// WithEnvText lays the env file content envText over e, parsed by compose-go's
// dotenv as compose reads an --env-file, with the compose child's own
// environment still winning, as in composeEnvExact. A key the replaced env
// file set and envText drops stays in Vars: that only spares a warning.
func (e LintEnv) WithEnvText(envText string) LintEnv {
	if envText == "" {
		return e
	}
	vars := make(map[string]string, len(e.Vars))
	for k, v := range e.Vars {
		vars[k] = v
	}
	parsed, err := dotenv.UnmarshalWithLookup(envText, func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	})
	if err != nil {
		return LintEnv{Vars: vars, Unresolved: true}
	}
	for k, v := range parsed {
		vars[k] = v
	}
	for k, v := range processComposeEnv() {
		vars[k] = v
	}
	return LintEnv{Vars: vars, Unresolved: e.Unresolved}
}

// secretValues are the values of e's sensitive keys, longest first, as
// composeSecrets returns them for compose output.
func (e LintEnv) secretValues() []string {
	var secrets []string
	seen := map[string]bool{}
	for k, v := range e.Vars {
		if IsSensitiveEnvKey(k) && len(v) >= composeSecretMinLen && !seen[v] {
			seen[v] = true
			secrets = append(secrets, v)
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return secrets
}

// processComposeEnv is the compose child's own environment, which
// resolveComposeEnv lays over every env file.
func processComposeEnv() map[string]string {
	env := map[string]string{}
	for _, kv := range dockerEnv() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return env
}

// requiredRefRe finds ${NAME:?...} and ${NAME?...}. Group 1 is the character
// before "${" (so an escaped $${ is skipped), 2 the name, 3 is ":" for the
// form that also refuses an empty value.
var requiredRefRe = regexp.MustCompile(`(^|[^$])\$\{([A-Za-z_][A-Za-z0-9_]*)(:?)\?`)

// missingRequiredVars names every required variable in content that vars
// leaves without a value, the one compose-go reported first. compose-go stops
// at the first; one lint should name all of them.
func missingRequiredVars(content string, vars map[string]string, first string) []string {
	names := []string{first}
	seen := map[string]bool{first: true}
	for _, m := range requiredRefRe.FindAllStringSubmatch(content, -1) {
		name := m[2]
		if seen[name] {
			continue
		}
		v, ok := vars[name]
		if !ok || (m[3] == ":" && v == "") {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// findVarLine is the first line of content that references ${name, or 1.
func findVarLine(content, name string) int {
	for i, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "${"+name) {
			return i + 1
		}
	}
	return 1
}

// missingEnvFiles warns about each required env_file that is not on disk,
// only when workingDir exists: a stack being created has no folder yet, and
// its .env comes from the same request.
func (l *LinterService) missingEnvFiles(content, workingDir string, project *types.Project) []models.LintResult {
	if fi, err := os.Stat(workingDir); err != nil || !fi.IsDir() {
		return nil
	}
	var results []models.LintResult
	for name, svc := range project.Services {
		for _, ef := range svc.EnvFiles {
			if !bool(ef.Required) {
				continue
			}
			p := ef.Path
			if !filepath.IsAbs(p) {
				p = filepath.Join(workingDir, p)
			}
			if _, err := os.Stat(p); err == nil { //geterrors:ignore existence probe: any stat failure means compose cannot read it either, which is what the warning says
				continue
			}
			shown := p
			if r, err := filepath.Rel(workingDir, p); err == nil && !strings.HasPrefix(r, "..") {
				shown = r
			}
			results = append(results, models.LintResult{
				Level:   "warning",
				Line:    l.findServiceLine(content, name),
				Message: fmt.Sprintf("Service '%s' names env_file %s, which does not exist. Deploy will fail until it is created, or mark it `required: false`.", name, shown),
				Rule:    "missing-env-file",
			})
		}
	}
	return results
}
