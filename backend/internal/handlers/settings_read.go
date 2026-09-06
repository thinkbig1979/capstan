package handlers

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// Shared settings-read helpers for agent-os-1gqn's conversion of the
// discarded-error sites. They live in their own file rather than in settings.go
// because settings.go, backup.go and updates.go all call them: a helper that
// three files depend on, kept inside one of them, makes that file impossible to
// revert on its own — which is exactly what a per-file mutation control needs
// to be able to do.

// settingOrFault reads one settings key, separating "no such row" from "this
// database could not answer". An absent row is the ordinary state on a fresh
// install and yields ("", nil), exactly as the discarded-error form did; any
// other error is a fault the caller must refuse on rather than serve a default
// it invented (agent-os-1gqn).
//
// GetSetting returns the bare Scan error (database/settings.go:14-19), so
// absence really is sql.ErrNoRows here and never ("", nil).
func settingOrFault(db *database.DB, key string) (string, error) {
	v, err := db.GetSetting(key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read setting %q: %w", key, err)
	}
	return v, nil
}

// readSettings reads every key through settingOrFault and returns them
// together, so a handler that needs several settings has ONE fault-capable
// read instead of one per key.
//
// That collapse is the point, not tidiness. agent-os-a6bc's lesson is that a
// refusal test using a fixture which faults EVERY read in a function pins none
// of them individually: any one site's error satisfies a bare assertion. With
// the reads behind a single call there is one site left to pin, and the wrapped
// key in the returned error names which read failed, so the assertion can be
// about a specific key rather than about "something failed".
func readSettings(db *database.DB, keys ...string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		v, err := settingOrFault(db, key)
		if err != nil {
			return nil, err
		}
		out[key] = v
	}
	return out, nil
}
