package services

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/compose-spec/compose-go/v2/dotenv"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// composeEnvMode picks which question resolveComposeEnv answers about a
// stack's compose environment (agent-os-1106). Both modes read the same
// files the same way, with compose-go's own dotenv resolver seeded with the
// compose child's environment (dockerEnv); they differ in which files and in
// what a fault means, and each difference is deliberate.
type composeEnvMode int

const (
	// composeEnvExact is the environment `docker compose` interpolates with
	// when Capstan runs it: the --env-file arguments buildComposeArgs passes
	// (global.env when it exists, then the stack's EnvFile), or the project
	// directory's .env when there are none, overridden by the compose child's
	// own environment. A file compose would fail to read fails here too.
	// Host space resolves object names with it, where a wrong name could offer
	// a stack's volume for removal, so a guess is worse than an error.
	composeEnvExact composeEnvMode = iota

	// composeEnvSuperset is every value compose could have interpolated, for
	// redaction. It also reads .env next to an EnvFile, as compose does when
	// run without --env-file, because over-including only masks more. A file
	// that cannot be stat'ed is skipped (compose, running as the same user,
	// could not read it either and cannot have echoed it), and a file
	// compose-go rejects still yields what it parsed before the fault. It
	// returns one map per file chain, {global.env, .env} and
	// {global.env, EnvFile}, so a value set in both is learned from each.
	composeEnvSuperset
)

// composeEnvFileChains returns the env files resolveComposeEnv reads, as the
// chains it resolves them in. An exact chain names a file whether or not it
// exists, so a missing EnvFile fails as it does for compose; superset chains
// hold only files that can be stat'ed and are not directories.
func composeEnvFileChains(st models.Stack, globalEnvPath string, mode composeEnvMode) ([][]string, error) {
	var dotEnv, envFile string
	if st.Directory != "" {
		dotEnv = filepath.Join(st.Directory, ".env")
	}
	if st.EnvFile != "" {
		envFile = stackRelativePath(st.Directory, st.EnvFile)
	}

	if mode == composeEnvSuperset {
		chains := [][]string{existingFiles([]string{globalEnvPath, dotEnv})}
		if envFile != "" && envFile != dotEnv {
			chains = append(chains, existingFiles([]string{globalEnvPath, envFile}))
		}
		return chains, nil
	}

	var files []string
	if globalEnvPath != "" {
		if _, err := os.Stat(globalEnvPath); err == nil {
			files = append(files, globalEnvPath)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("checking the global env file: %w", err)
		}
	}
	if envFile != "" {
		files = append(files, envFile)
	}
	if len(files) == 0 && dotEnv != "" {
		if _, err := os.Stat(dotEnv); err == nil {
			files = append(files, dotEnv)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("checking the stack's .env: %w", err)
		}
	}
	return [][]string{files}, nil
}

// resolveComposeEnv resolves a stack's compose environment in the given mode:
// one map in exact mode, one per chain in superset mode. In superset mode the
// maps are returned even when err is non-nil; err then only says a file was
// partly parsed, and its text can quote a secret from the offending line
// (observed: `unterminated quoted value "<value>`), so it is not for a log.
func resolveComposeEnv(st models.Stack, globalEnvPath string, mode composeEnvMode) ([]map[string]string, error) {
	chains, err := composeEnvFileChains(st, globalEnvPath, mode)
	if err != nil {
		return nil, err
	}

	processEnv := map[string]string{}
	for _, kv := range dockerEnv() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			processEnv[k] = v
		}
	}

	var envs []map[string]string
	var faults []error
	for _, files := range chains {
		env, err := dotenv.GetEnvFromFile(processEnv, files)
		if err != nil {
			if mode == composeEnvExact {
				return nil, err
			}
			faults = append(faults, err)
		}
		if mode == composeEnvExact {
			for k, v := range processEnv {
				env[k] = v
			}
		}
		envs = append(envs, env)
	}
	return envs, errors.Join(faults...)
}

// existingFiles drops the paths GetEnvFromFile would stop on before reading
// the rest: empty, unstattable or a directory. An unstattable file is skipped
// for the same reason composeSecrets skips an unreadable one.
func existingFiles(paths []string) []string {
	var out []string
	for _, p := range paths {
		if p == "" {
			continue
		}
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !fi.IsDir() {
			out = append(out, p)
		}
	}
	return out
}
