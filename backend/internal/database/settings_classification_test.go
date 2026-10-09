package database

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Guard for safe-defaults rule 13 (agent-os-qags.5): every settings key the
// code reads or writes is classified, either in sensitiveSettingKeys
// (settings.go, encrypted at rest) or in notSensitiveSettingKeys below with a
// reason. restic_repository and rclone_remote shipped in clear for releases
// because nothing asked the question when they were added (agent-os-n4ca.7);
// with this test a new key is a red CI run until someone answers it.
//
// The scan reads source from disk, so a go test -overlay mutant is invisible
// to it; TestSettingsKeyScanner_ReportsUnclassifiedAndDynamic is the in-test
// negative arm instead, run against a synthetic tree.

// notSensitiveSettingKeys: keys that hold no credential and cannot embed one.
var notSensitiveSettingKeys = map[string]string{
	"auto_update_enabled":                "boolean flag",
	"backup_auto_prune":                  "boolean flag",
	"backup_hostname":                    "host name restic tags snapshots with",
	"backup_keep_daily":                  "retention count",
	"backup_keep_monthly":                "retention count",
	"backup_keep_weekly":                 "retention count",
	"backup_keep_yearly":                 "retention count",
	"backup_schedule_days":               "weekday list",
	"backup_schedule_interval":           "minutes",
	"backup_schedule_mode":               "enum",
	"backup_schedule_time":               "HH:MM",
	"backup_sync_after":                  "boolean flag",
	"default_stacks_dir":                 "directory path",
	"docker_cleanup_enabled":             "boolean flag",
	"docker_cleanup_interval_hours":      "hours",
	"docker_cleanup_min_age_hours":       "hours",
	"git_https_user":                     "user name; the token is git_https_token",
	"git_ssh_key":                        "path to a key file; key contents are refused on save (handlers/settings.go, looksLikePrivateKey)",
	"max_backup_history_retention_days":  "days",
	"max_cleanup_history_retention_days": "days",
	"max_log_retention_days":             "days",
	"max_update_history_retention_days":  "days",
	"rclone_path":                        "path inside the remote; the remote itself is rclone_remote",
	"rclone_transfers":                   "count",
	"scan_depth":                         "count",
	"stack_id_version":                   "internal schema marker",
	"update_apply_arm_error":             "status message written by the scheduler",
	"update_apply_days":                  "weekday list",
	"update_apply_last_error":            "status message written by the scheduler",
	"update_apply_mode":                  "enum",
	"update_apply_time":                  "HH:MM",
	"update_scan_interval":               "minutes",
	"update_scan_last_error":             "status message written by the scheduler",
	"update_scan_last_run":               "timestamp",
}

// settingsKeyFuncs maps each function that takes a settings key to the
// argument index (or first index, for variadic readSettings) holding it.
var settingsKeyFuncs = map[string]int{
	"SetSetting":           0,
	"GetSetting":           0,
	"RetentionDays":        0, // the method; the package func of the same name is excluded in keyArgIndex
	"recordApplyError":     0,
	"settingOrFault":       1,
	"readSettings":         1,
	"readSetting":          1,
	"resolveIntSetting":    1,
	"resolveBoolSetting":   1,
	"resolveStringSetting": 1,
}

// allowedDynamicKeySites are the calls whose key is not a literal or constant,
// keyed "<path>:<enclosing func>". Each is either a wrapper whose own callers
// are scanned (their keys are collected there) or a loop over constants that
// are collected elsewhere. A new site fails the test until it is reviewed and
// added here.
var allowedDynamicKeySites = map[string]string{
	"internal/handlers/settings_read.go:settingOrFault":       "wrapper; its callers are scanned",
	"internal/handlers/settings_read.go:readSettings":         "wrapper; its callers are scanned",
	"internal/services/backup_config.go:readSetting":          "wrapper; its callers are scanned",
	"internal/services/backup_config.go:resolveIntSetting":    "wrapper; its callers are scanned",
	"internal/services/backup_config.go:resolveStringSetting": "wrapper; its callers are scanned",
	"internal/services/backup_config.go:resolveBoolSetting":   "wrapper; its callers are scanned",
	"internal/services/scheduler.go:recordApplyError":         "wrapper; its callers are scanned",
	"internal/database/retention.go:RetentionDays":            "wrapper; its callers are scanned",
	"internal/handlers/settings.go:GetLogRetention":           "loop over the four database.Setting*RetentionDays constants, each also read by name in retention.go",
	"internal/handlers/settings.go:UpdateLogRetention":        "loop over the four database.Setting*RetentionDays constants, each also read by name in retention.go",
}

// seededSettingRe finds keys a migration seeds with SQL rather than through
// SetSetting.
var seededSettingRe = regexp.MustCompile(`INTO settings \(key, value\) VALUES \('([a-z0-9_]+)'`)

type settingsKeyScan struct {
	keys    map[string][]string // key -> sites
	dynamic map[string][]string // "<path>:<func>" -> sites
}

// scanSettingsKeys parses every non-test .go file under root's subdirectories
// dirs and returns the settings keys it finds.
func scanSettingsKeys(t *testing.T, root string, dirs ...string) settingsKeyScan {
	t.Helper()
	fset := token.NewFileSet()
	type parsed struct {
		rel  string
		file *ast.File
	}
	var files []parsed
	for _, d := range dirs {
		require.NoError(t, filepath.WalkDir(filepath.Join(root, d), func(path string, e os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, parsed{filepath.ToSlash(rel), f})
			return nil
		}))
	}
	require.NotEmpty(t, files, "the scan parsed no files under %s %v", root, dirs)

	// String constants by package name, so services.SettingDockerCleanupEnabled
	// and a bare applyArmErrorKey both resolve.
	consts := map[string]map[string]string{}
	for _, p := range files {
		pkg := p.file.Name.Name
		if consts[pkg] == nil {
			consts[pkg] = map[string]string{}
		}
		for _, decl := range p.file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if v, err := strconv.Unquote(lit.Value); err == nil {
							consts[pkg][name.Name] = v
						}
					}
				}
			}
		}
	}

	scan := settingsKeyScan{keys: map[string][]string{}, dynamic: map[string][]string{}}
	for _, p := range files {
		pkg := p.file.Name.Name
		resolve := func(e ast.Expr) (string, bool) {
			switch x := e.(type) {
			case *ast.BasicLit:
				if x.Kind == token.STRING {
					v, err := strconv.Unquote(x.Value)
					return v, err == nil
				}
			case *ast.Ident:
				v, ok := consts[pkg][x.Name]
				return v, ok
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok {
					v, ok := consts[id.Name][x.Sel.Name]
					return v, ok
				}
			}
			return "", false
		}
		for _, decl := range p.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.BasicLit:
					if x.Kind == token.STRING {
						for _, m := range seededSettingRe.FindAllStringSubmatch(x.Value, -1) {
							scan.keys[m[1]] = append(scan.keys[m[1]], fset.Position(x.Pos()).String())
						}
					}
				case *ast.CompositeLit:
					// A SettingValue literal is a key written through
					// SetSettings (agent-os-u0nd); its Key field is the key.
					// In a []SettingValue literal the elements may elide
					// their type, so those are read through the slice.
					lits := []*ast.CompositeLit{}
					if isSettingValueType(x.Type) {
						lits = append(lits, x)
					} else if at, ok := x.Type.(*ast.ArrayType); ok && isSettingValueType(at.Elt) {
						for _, elt := range x.Elts {
							if cl, ok := elt.(*ast.CompositeLit); ok && cl.Type == nil {
								lits = append(lits, cl)
							}
						}
					}
					for _, lit := range lits {
						settingValueKeys(lit, func(e ast.Expr) {
							site := fset.Position(e.Pos()).String()
							if v, ok := resolve(e); ok {
								scan.keys[v] = append(scan.keys[v], site)
							} else {
								k := p.rel + ":" + fn.Name.Name
								scan.dynamic[k] = append(scan.dynamic[k], site)
							}
						})
					}
				case *ast.CallExpr:
					idx, ok := keyArgIndex(x)
					if !ok || idx >= len(x.Args) {
						return true
					}
					site := fset.Position(x.Pos()).String()
					variadic := calleeName(x) == "readSettings"
					for i := idx; i < len(x.Args); i++ {
						if v, ok := resolve(x.Args[i]); ok {
							scan.keys[v] = append(scan.keys[v], site)
						} else {
							k := p.rel + ":" + fn.Name.Name
							scan.dynamic[k] = append(scan.dynamic[k], site)
						}
						if !variadic {
							break
						}
					}
				}
				return true
			})
		}
		// Package-level SQL (the migrations slice) is outside any FuncDecl.
		for _, decl := range p.file.Decls {
			if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.VAR {
				ast.Inspect(gd, func(n ast.Node) bool {
					if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						for _, m := range seededSettingRe.FindAllStringSubmatch(lit.Value, -1) {
							scan.keys[m[1]] = append(scan.keys[m[1]], fset.Position(lit.Pos()).String())
						}
					}
					return true
				})
			}
		}
	}
	return scan
}

// settingValueKeys calls found with the Key field of a SettingValue literal.
func settingValueKeys(lit *ast.CompositeLit, found func(ast.Expr)) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "Key" {
			found(kv.Value)
		}
	}
}

// isSettingValueType reports whether a composite literal's type is
// SettingValue, bare inside package database or qualified outside it.
func isSettingValueType(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name == "SettingValue"
	case *ast.SelectorExpr:
		return t.Sel.Name == "SettingValue"
	}
	return false
}

func calleeName(c *ast.CallExpr) string {
	switch f := c.Fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

func keyArgIndex(c *ast.CallExpr) (int, bool) {
	name := calleeName(c)
	idx, ok := settingsKeyFuncs[name]
	if !ok {
		return 0, false
	}
	if name == "RetentionDays" {
		// RetentionDays(value) and database.RetentionDays(value) are the
		// package func that parses a value; only the (*DB) method takes a key.
		sel, isSel := c.Fun.(*ast.SelectorExpr)
		if !isSel {
			return 0, false
		}
		if id, isID := sel.X.(*ast.Ident); isID && id.Name == "database" {
			return 0, false
		}
	}
	return idx, true
}

func TestEverySettingsKeyIsClassified(t *testing.T) {
	t.Parallel()
	scan := scanSettingsKeys(t, filepath.Join("..", ".."), "internal", "cmd")

	var unclassified, both []string
	for key, sites := range scan.keys {
		_, notSensitive := notSensitiveSettingKeys[key]
		switch {
		case sensitiveSettingKeys[key] && notSensitive:
			both = append(both, key)
		case !sensitiveSettingKeys[key] && !notSensitive:
			unclassified = append(unclassified, key+" (at "+sites[0]+")")
		}
	}
	sort.Strings(unclassified)
	sort.Strings(both)
	assert.Empty(t, unclassified, "settings keys in neither sensitiveSettingKeys (settings.go) nor "+
		"notSensitiveSettingKeys: a key that holds or can embed a credential joins sensitiveSettingKeys "+
		"(safe-defaults rule 13); any other key is listed in notSensitiveSettingKeys with a reason")
	assert.Empty(t, both, "settings keys classified both ways")

	var unreviewed []string
	for site, calls := range scan.dynamic {
		if _, ok := allowedDynamicKeySites[site]; !ok {
			unreviewed = append(unreviewed, site+" "+strings.Join(calls, ", "))
		}
	}
	sort.Strings(unreviewed)
	assert.Empty(t, unreviewed, "a settings call whose key the scan cannot resolve to a literal or "+
		"constant: make it one, or review the site and add it to allowedDynamicKeySites")

	// Stale entries: every classified key and every allowed site must still
	// be found. This is also the positive control: a scan that matched
	// nothing would fail here rather than report a clean tree.
	for key := range sensitiveSettingKeys {
		assert.Contains(t, scan.keys, key, "sensitiveSettingKeys names a key the code never uses")
	}
	for key := range notSensitiveSettingKeys {
		assert.Contains(t, scan.keys, key, "notSensitiveSettingKeys names a key the code never uses")
	}
	for site := range allowedDynamicKeySites {
		assert.Contains(t, scan.dynamic, site, "allowedDynamicKeySites names a site that no longer exists")
	}
	t.Logf("settings keys found: %d; dynamic-key sites: %d", len(scan.keys), len(scan.dynamic))
}

// TestSettingsKeyScanner_ReportsUnclassifiedAndDynamic is the negative arm:
// the scanner, run over a synthetic package, must find a literal key, a
// same-package constant, a qualified constant, a variadic key and a migration
// seed, and must report a variable key as dynamic.
func TestSettingsKeyScanner_ReportsUnclassifiedAndDynamic(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "fake"), 0o755))
	src := "package fake\n\n" +
		"const tokenKey = \"fake_api_token\"\n\n" +
		"var migrationsSQL = `INSERT OR IGNORE INTO settings (key, value) VALUES ('fake_seeded', '1');`\n\n" +
		"func f(db interface{ SetSetting(string, string) error }, h struct{ db interface{ RetentionDays(string) (int, error) } }, k string) {\n" +
		"\t_ = db.SetSetting(\"fake_literal\", \"x\")\n" +
		"\t_ = db.SetSetting(tokenKey, \"x\")\n" +
		"\t_ = db.SetSetting(other.QualifiedKey, \"x\")\n" +
		"\t_, _ = readSettings(nil, \"fake_variadic_a\", \"fake_variadic_b\")\n" +
		"\t_, _ = h.db.RetentionDays(\"fake_retention\")\n" +
		"\t_ = RetentionDays(\"not_a_key\")\n" +
		"\t_ = db.SetSetting(k, \"x\")\n" +
		"\t_ = []database.SettingValue{{Key: \"fake_batch_elided\"}}\n" +
		"\t_ = database.SettingValue{Key: \"fake_batch\", Value: \"x\"}\n" +
		"}\n\n" +
		"func g(k string) {\n" +
		"\t_ = database.SettingValue{Key: k}\n" +
		"}\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "internal", "fake", "fake.go"), []byte(src), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "other"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "internal", "other", "other.go"),
		[]byte("package other\n\nconst QualifiedKey = \"fake_qualified\"\n"), 0o644))

	scan := scanSettingsKeys(t, root, "internal")
	var got []string
	for k := range scan.keys {
		got = append(got, k)
	}
	sort.Strings(got)
	assert.Equal(t, []string{"fake_api_token", "fake_batch", "fake_batch_elided", "fake_literal", "fake_qualified", "fake_retention",
		"fake_seeded", "fake_variadic_a", "fake_variadic_b"}, got)
	assert.Contains(t, scan.dynamic, "internal/fake/fake.go:f", "a variable key must be reported, not skipped")
	assert.Contains(t, scan.dynamic, "internal/fake/fake.go:g", "a variable SettingValue key must be reported, not skipped")
}
