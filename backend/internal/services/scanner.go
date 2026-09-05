package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/template"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"gopkg.in/yaml.v3"
)

// StackID is the single producer of stack IDs. Both the scanner (discovering a
// compose file on disk) and POST /api/v1/stacks (creating one) must mint the
// same ID for the same stack, or a create is immediately shadowed by the next
// scan. They agreed by coincidence of two independent format strings until
// agent-os-elo; they agree by construction now.
//
// root MUST already be resolved to one of the configured stacks roots. This
// helper deliberately does NOT work out which root a path belongs to: that
// resolution is a separate concern (see resolveEffectiveRoot below, used by
// buildDirectoryRecord's effectiveRoot fallback for the rootDir="" case).
// Taking the root as a parameter keeps that resolution logic from flowing
// through ID minting, so a future change to how roots are resolved cannot
// silently change what StackID does with an already-resolved one.
//
// primaryRoot and extraRoots are config.StacksDir and config.ExtraStacksDirs.
// They are passed as separate parameters rather than as one GetAllStacksDirs
// slice because the primary root is privileged (see StackIDRootPrefix) and
// inferring which entry is primary from its position in a combined slice would
// make that guarantee depend on a caller's slice-building order.
//
// pathID is the stack's path relative to root with separators flattened to "-";
// name is the compose profile ("default" for compose.yaml).
func StackID(root, primaryRoot string, extraRoots []string, pathID, name string) string {
	return fmt.Sprintf("%s~%s:%s", StackIDRootPrefix(root, primaryRoot, extraRoots), pathID, name)
}

// ComposeProjectName is Capstan's DIRECTORY-DERIVED project name: the last of
// compose's three sources, and now the DEFAULT and the FALLBACK rather than the
// answer. ResolveComposeProjectName below is the producer both writers call;
// it asks compose-go for the name compose itself would use and drops back to
// this function when the file does not name a project, or cannot be read
// (agent-os-89z2). Everything below still describes what this function does,
// because it is still what gets persisted whenever `name:` is absent — which is
// the overwhelmingly common case.
//
// It remains a SINGLE producer of the directory-derived form, for the same
// reason StackID above is the single producer of IDs.
//
// The scanner and POST /api/v1/stacks each derived this independently and
// disagreed for the default compose file: create-with-deploy deployed under
// "<dir>-default" while the scan that immediately follows it stored "<dir>"
// (agent-os-07x). buildComposeArgs (docker.go) passes the STORED value as -p,
// so the containers the create had just started were unreachable to every later
// operation: the dashboard reported Stopped while the stack served traffic, and
// Start built a second, colliding project.
//
// The scanner's rule is the one kept, because it is the one that matches what
// Docker Compose itself does with a bare compose.yaml in <dir>: the project is
// the directory name, with no suffix. Only a named compose file (compose.api.
// yaml) earns one.
//
// name is the compose profile as extractStackName returns it ("default" for
// compose.yaml). Colons are not legal in a compose project name, so a profile
// carrying one is flattened to "-".
//
// The result is then passed through compose-go's loader.NormalizeProjectName,
// the function Docker Compose itself applies to a directory basename before
// labelling containers: lower-case, keep only [a-z0-9_-], trim leading "-"
// and "_" (agent-os-f3ah). Before that, the basename was persisted VERBATIM,
// so a directory "MyStack" started outside Capstan was labelled "mystack" by
// compose while Capstan filtered on "MyStack": zero containers, status
// stopped, empty metrics, and Start could not repair it because compose
// rejects `-p MyStack` outright. Delegating to compose-go rather than copying
// its rule means the two cannot drift; a compose-go bump moves both.
//
// The result can be EMPTY ("---", "..."): compose refuses to run such a
// directory at all (loader.go: "project name must not be empty"), so no
// compose project can exist for it. Callers decide what to do with that: the
// scanner skips the row with a warning, the create endpoint refuses the name.
//
// What normalising the whole "<dir>-<profile>" string does and does not buy,
// stated because it is easy to over-claim: compose derives a project name
// from exactly three sources, all normalised — explicit -p (loader.go:695,
// which must already be normal), top-level `name:` (loader.go:752), else the
// directory basename (cli/options.go:561-567) — and NEVER from the compose
// FILE's name. So "<dir>-<profile>" is Capstan's own namespace for a named
// file; normalising it makes the `-p` Capstan passes acceptable to compose
// (the whole string has to satisfy loader.go:695), but a named file started
// OUTSIDE Capstan with -f is labelled by the directory alone and is not found
// under "<dir>-<profile>" (S6 in agent-os-f3ah's notes; not this bead). Label
// parity holds for the DEFAULT file, which is what this bead promises.
//
// Two stacks can normalise to one name: two directories ("MyStack",
// "my.stack") or two named files in one directory ("compose.api.v2.yaml",
// "compose.apiv2.yaml"). Compose would run them as ONE project, so Capstan
// cannot separate them either; the scanner persists every row and warns
// (see warnProjectNameCollisions).
func ComposeProjectName(dirName, name string) string {
	raw := dirName
	if name != "default" {
		raw = fmt.Sprintf("%s-%s", dirName, strings.ReplaceAll(name, ":", "-"))
	}
	return loader.NormalizeProjectName(raw)
}

// Where a persisted project name came from. Reported by
// ResolveComposeProjectName and logged with the discovered stack, so an
// operator asking "why is this stack called that" gets the answer from the log
// rather than by reasoning about precedence.
const (
	// ProjectNameSourceComposeName: a top-level `name:` in the compose file.
	ProjectNameSourceComposeName = "compose-name"
	// ProjectNameSourceDirectory: the directory basename, compose's last source.
	ProjectNameSourceDirectory = "directory"
	// ProjectNameSourceNamedFile: Capstan's own "<dir>-<profile>" namespace,
	// used for a named compose file that declares no `name:` of its own. Not
	// one of compose's sources — it is Capstan's FALLBACK for named files, the
	// counterpart of ProjectNameSourceDirectory for the default file. See the
	// OWNER POLICY note in ResolveComposeProjectName.
	ProjectNameSourceNamedFile = "capstan-named-file"
	// ProjectNameSourceFallback: the compose file could not be read, so the
	// directory derivation stands in. Always accompanied by a WARN.
	ProjectNameSourceFallback = "fallback"
)

// ResolveComposeProjectName is the producer of persisted project names: the
// scanner (ScanDirectoryWithRoot) and POST /api/v1/stacks both call it, so the
// row a create writes and the row the scan immediately after it rewrites agree
// by construction (agent-os-07x is the bug that invariant exists to prevent).
//
// It answers the question "what would `docker compose` call this?". Compose
// derives a project name from three sources, in order: an explicit -p /
// COMPOSE_PROJECT_NAME, else a top-level `name:` in the compose file, else the
// directory basename — all through NormalizeProjectName, and NEVER from the
// compose FILE's name. agent-os-f3ah made Capstan agree with compose on the
// THIRD source; this makes it agree on the second (shape S2), which is the one
// with teeth: a directory "other" whose file says `name: custom` is labelled
// "custom" by compose, so Capstan filtering on "other" saw zero containers and
// a Start from Capstan built a SECOND project beside the operator's.
//
// Every RULE is still compose-go's: substitution is its template.Substitute,
// normalisation its NormalizeProjectName, so a compose-go bump moves Capstan
// with it. What composeFileProjectName owns is only the EXTRACTION of the
// `name:` string, for the reasons documented on that function — handing the
// whole file to the loader let anything in it decide the project name.
//
// The FIRST source is deliberately not honoured, and the reason is a choice,
// not an inability. The directory's .env is read, so a COMPOSE_PROJECT_NAME in
// it is plainly VISIBLE here; it is used only as a substitution mapping for the
// `name:` field and is never consulted as a source of the name itself. Capstan
// passes its own -p on every start (docker.go:297, logs.go:303), so honouring
// an env override at scan time would make the scanned name and the started name
// differ, which is the exact class of divergence this function exists to close.
// An explicit -p typed by an operator outside Capstan (shape S4) is not visible
// here at all.
//
// A top-level `name:` wins for EVERY compose file, named files included. What
// the OWNER POLICY (Edwin, 2026-09-05 16:05: KEEP Capstan's "<dir>-<profile>"
// namespace for a NAMED compose file) governs is the FALLBACK, not the
// precedence: a named file that declares no usable `name:` gets
// "<dir>-<profile>", and the default file gets "<dir>". That reading was
// settled by bud7 on 2026-09-05 ~20:50, after this function first shipped with
// the narrower one; KEEP answered "keep the namespace or drop it", not "ignore
// `name:` in named files". Reverting is re-adding an early return here for
// profile != "default", which is exactly what the narrower reading was.
//
// The reason is the bead's own premise: `docker compose -f compose.api.yaml up`
// labels containers with the file's `name:` when it has one, so ignoring it for
// named files would leave shape S2 open on precisely the files this bead was
// filed to cover.
//
// The namespace still earns its keep on the fallback branch. Two named files in
// one directory with no `name:` get distinct projects, which is Capstan's own
// convention and is passed as -p on start. Two that declare the SAME `name:`
// now collide onto one project — as compose itself would — and
// warnProjectNameCollisions surfaces that rather than resolving it.
//
// What stays uncovered is the other half of shape S6: a named file with NO
// `name:`, started OUTSIDE Capstan with `-f`, is labelled by the DIRECTORY
// alone (compose has no file-name source) while Capstan persists
// "<dir>-<profile>". That gap is accepted, not overlooked, and it is the price
// of keeping the namespace.
//
// source is one of the ProjectNameSource* constants above and is advisory: it
// explains the name, it does not participate in deriving it.
func ResolveComposeProjectName(ctx context.Context, composePath, dirName, profile string) (name string, source string) {
	derived := ComposeProjectName(dirName, profile)

	// Capstan's fallback for this file, reported only when the file names no
	// project of its own: the "<dir>-<profile>" namespace for a named file,
	// compose's own directory basename for the default file.
	derivedSource := ProjectNameSourceDirectory
	if profile != "default" {
		derivedSource = ProjectNameSourceNamedFile
	}

	loaded, err := composeFileProjectName(ctx, composePath)
	if err != nil {
		// The scanner runs at boot over every directory. A file compose cannot
		// parse must still produce a listed, startable-or-loudly-failing row
		// exactly as before this function existed, so the failure is a WARN and
		// the directory derivation stands — never a skipped stack.
		slog.Warn("Could not read the compose file's own project name; using the directory-derived name",
			"file", composePath, "project name", derived, "error", err)
		return derived, ProjectNameSourceFallback
	}
	if loaded == "" {
		// No `name:`, or one that normalises to nothing. Compose falls through
		// to its next source in exactly this case (loader.go:753-756 assigns
		// only a non-empty normalised name), so Capstan falls through to its
		// own derivation here.
		return derived, derivedSource
	}
	return loaded, ProjectNameSourceComposeName
}

// composeFileProjectName returns the project name compose derives from this
// one file, or "" if the file names no project. The error is returned rather
// than absorbed so the caller can warn about it exactly once.
//
// It reads the top-level `name:` and interpolates THAT STRING ONLY. An earlier
// revision handed the whole file to loader.LoadWithContext and read
// project.Name off the result; review found two defects in that, with one
// cause — the loader interpolates the WHOLE file (loader.go:477), so anything
// anywhere in it could decide the project name (OBSERVED on compose-go
// v2.14.0, 2026-09-05):
//
//   - `PW: ${SECRET:?required}` in a SERVICE aborted the load with
//     "required variable SECRET is missing a value", so the file's own `name:`
//     was thrown away and the stack fell back to its directory name. That is
//     the standard compose secrets idiom, and Capstan's global-env feature
//     exists because such variables are often NOT in the stack's own .env, so
//     the fix for shape S2 was inert for exactly the stacks most likely to need
//     it.
//   - Suppressing the resulting log spam by resolving an unset variable as
//     ("", true) turned "unset" into "set to empty", which is a DIFFERENT
//     question in compose's substitution syntax. `${NAME-dflt}` returned ""
//     instead of "dflt", and `${NAME?msg}` returned "" instead of erroring —
//     silently wrong names, reported as source "directory", with no warning.
//
// Loading with SkipInterpolation to get the raw string back does not fix it
// either: `ports: - "${PORT}:80"` then fails ModelToProject with
// 'services[web].ports[0]' expected a map or struct, got "string" (OBSERVED),
// which would have reintroduced the same inert-feature defect on a different,
// equally common idiom.
//
// So the extraction is done here. What is NOT reimplemented is the part with
// rules: substitution is compose-go's own template.Substitute, and
// normalisation is its NormalizeProjectName, so `-`, `:-`, `?` and `:?` behave
// exactly as `docker compose` does and a compose-go bump moves both. The only
// logic owned here is "the last non-empty top-level `name:` across the
// document stream", which mirrors loader.go:703-737.
//
// Two deliberate differences from that loop, both toward being loud:
// compose swallows a YAML decode error there and gives up on the name
// ("it'll get caught downstream"); this returns it, so the caller warns and
// falls back visibly. And compose reads with go.yaml.in/yaml/v4 while this uses
// gopkg.in/yaml.v3, already a direct dependency: for a top-level scalar key the
// two agree, and a file only v4 accepts would warn and fall back rather than
// resolve — the conservative direction.
func composeFileProjectName(ctx context.Context, composePath string) (string, error) {
	// The scan walks every directory at boot; a cancelled create or shutdown
	// should stop it rather than read the rest of the tree.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// composePath is bounded by both callers, not by this function: the scanner
	// passes a filepath.Glob match taken under a configured stacks root
	// (ScanDirectoryWithRoot's patterns loop), and POST /api/v1/stacks passes
	// filepath.Join(stackDir, "compose.yaml") for a stackDir that already
	// cleared isValidStacksDir (exact match against a configured root) and the
	// filepath.Rel traversal guard, and whose file the handler itself just
	// wrote — see README.md "Command execution and file access".
	//nolint:gosec // path bounded by the configured stacks roots at both call sites, cited above
	content, err := os.ReadFile(composePath)
	if err != nil {
		return "", err
	}

	raw, err := topLevelComposeName(content)
	if err != nil {
		return "", err
	}
	if raw == "" {
		return "", nil
	}

	// The .env is read only now, when a name actually needs interpolating. That
	// keeps the warning below truthful — it is about resolving THIS name — and
	// costs nothing for the overwhelming majority of files, which name no
	// project at all.
	environment := stackDotEnv(filepath.Dir(composePath))

	// Reporting a miss as ABSENT rather than as empty is the whole point: compose
	// distinguishes "unset" from "set but empty", and the `-` and `?` forms
	// (without a colon) answer the first question. Collapsing them is what
	// produced silently wrong names.
	substituted, err := template.Substitute(raw, func(key string) (string, bool) {
		value, ok := environment[key]
		return value, ok
	})
	if err != nil {
		return "", err
	}

	return loader.NormalizeProjectName(substituted), nil
}

// topLevelComposeName returns the last non-empty top-level `name:` in the
// document stream, mirroring compose's own extraction (loader.go:703-737):
// later documents win, which is how a multi-document file behaves there.
func topLevelComposeName(content []byte) (string, error) {
	type named struct {
		Name string `yaml:"name"`
	}

	decoder := yaml.NewDecoder(bytes.NewReader(content))
	name := ""
	for {
		var doc named
		err := decoder.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if doc.Name != "" {
			name = doc.Name
		}
	}
	return name, nil
}

// stackDotEnv reads the variables `docker compose` would resolve ${VAR} against
// for a stack in dir: its own .env, and nothing else.
//
// A MISSING .env is the common case and is silent. A PRESENT but unparseable
// one is warned about exactly once, naming the file: compose refuses to run on
// it at all, so letting a `name: ${PROJECT}` fall quietly to the directory name
// would be the same silent divergence this producer exists to close. It is
// still only a warning — the name stays resolvable, with the variables unset.
//
// The two are told apart by an os.Stat rather than by matching dotenv's error
// text, so a reworded compose-go message cannot silently turn a malformed file
// into a missing one.
//
// The process environment is deliberately NOT merged in. It keeps the scanned
// name a pure function of what is on disk, so two Capstan instances walking the
// same tree agree, and it keeps the backend's own environment out of project
// names. Cost: a `name:` interpolated from a host variable alone falls back to
// the directory.
func stackDotEnv(dir string) map[string]string {
	envPath := filepath.Join(dir, ".env")
	if _, err := os.Stat(envPath); err != nil {
		return map[string]string{}
	}

	environment, err := dotenv.GetEnvFromFile(map[string]string{}, []string{envPath})
	if err != nil {
		slog.Warn("Could not parse the stack's .env; compose project name resolved without it",
			"file", envPath, "error", err)
		return map[string]string{}
	}
	return environment
}

// StackIDRootPrefix returns the root component of a stack ID.
//
// Normally that is just the root's basename, which is what every stack ID in
// the field already carries. Two configured roots CAN share a basename
// (/a/stacks and /b/stacks), and then the basename alone collides: UpsertStack
// is an INSERT OR REPLACE, so the second stack silently repoints the first's
// row at its own directory and the first becomes unreachable by ID.
//
// The disambiguator is therefore applied to colliding roots ONLY, and every
// other root keeps its prefix byte-for-byte. Re-IDing unconditionally would be
// far more expensive than it looks: stack IDs are copied into six plain TEXT
// columns with no foreign key to keep them honest (action_log.stack_id,
// cached_updates.stack_id, update_history.stack_id, backup_run_items.stack_id,
// auto_update_policies.target_id, backup_policies.target_id) and into frontend
// URLs users bookmark. Collision-only touches none of that, and needs no
// migration.
//
// The suffix is a fingerprint of the root's own path rather than its position
// in the config, so reordering EXTRA_STACKS_DIRS does not re-ID anything. "."
// joins it because it is already legal in this ID's charset (see
// middleware.ValidateStackID), is safe unescaped in a URL path, and sits before
// the final "~" that the frontend splits on to recover a display name.
//
// # The primary root is never suffixed
//
// Because the prefix depends on the SET of configured roots, adding a root can
// in principle change the ID of a stack that already exists. primaryRoot
// (config.StacksDir) is therefore exempt unconditionally: it keeps the bare
// basename even when an extra root collides with it, and the extra takes the
// suffix instead. Adding, removing or reordering extra roots consequently never
// re-IDs anything under the primary root, which is where single-root
// deployments — the overwhelming majority — keep everything.
//
// # Known residual: extra roots are not stable under config changes
//
// Two EXTRA roots that collide with each other, or an extra root that only
// starts colliding once a LATER extra root is added, still flip from a bare
// prefix to a suffixed one. Changing the set of configured roots can therefore
// change the ID of stacks under a non-primary root.
//
// That is worse than a rename, because nothing cleans up after it: the ID is
// not what pruning keys on. pruneStaleStacks removes rows by DIRECTORY
// existence, and the directory is still there, so the old row survives while
// the scan upserts a second row under the new ID — one stack, two rows, with
// all six TEXT columns above still referencing the old ID. Filed separately;
// deliberately not solved here, because solving it means either persisting the
// root->prefix mapping or a migration, both out of scope for the collision fix.
func StackIDRootPrefix(root, primaryRoot string, extraRoots []string) string {
	base := filepath.Base(root)
	canonical := canonicalRoot(root)

	if canonical == canonicalRoot(primaryRoot) {
		return base
	}

	if filepath.Base(primaryRoot) == base {
		return base + "." + rootFingerprint(canonical)
	}
	for _, other := range extraRoots {
		if filepath.Base(other) == base && canonicalRoot(other) != canonical {
			return base + "." + rootFingerprint(canonical)
		}
	}

	return base
}

func rootFingerprint(canonicalRoot string) string {
	sum := sha256.Sum256([]byte(canonicalRoot))
	return hex.EncodeToString(sum[:4])
}

// canonicalRoot puts a root in a comparable form. It intentionally stops at
// filepath.Abs: resolving symlinks would let a root compare equal to a
// configured root it is not spelled the same as, and the callers' own
// validation (handlers.isValidStacksDir) matches on Abs too.
func canonicalRoot(root string) string {
	if abs, err := filepath.Abs(root); err == nil {
		return abs
	}
	return filepath.Clean(root)
}

type ScannerService struct {
	config *config.Config
	db     *database.DB
}

func NewScannerService(cfg *config.Config, db *database.DB) *ScannerService {
	return &ScannerService{
		config: cfg,
		db:     db,
	}
}

func (s *ScannerService) ScanAll() (hasGlobalEnv bool, err error) {
	allDirs := s.config.GetAllStacksDirs()
	slog.Info("Starting directory scan", "stacksDirs", allDirs)

	_, err = os.Stat(filepath.Join(s.config.DataDir, "global.env"))
	hasGlobalEnv = !os.IsNotExist(err)

	scanDepth, depthErr := s.readScanDepth()
	if depthErr != nil {
		// Refuse rather than scan shallow. handlers/directories.go:68 already
		// maps this error to a 500 carrying the cause, and an operator-
		// triggered rescan that silently visited one level and reported 200 OK
		// is a wrong answer presented as success. cmd/server/main.go:311
		// discards the error, so boot only skips this pass; the scan path
		// upserts and never deletes, so nothing is lost that the watcher's next
		// pass does not recover.
		slog.Error("Refusing to scan: the scan depth setting could not be read, so the scan would silently visit only the top level and under-report which stacks exist", "cause", depthErr)
		return hasGlobalEnv, depthErr
	}

	for _, stacksDir := range allDirs {
		s.scanDirectoryRecursive(stacksDir, stacksDir, scanDepth, 1)
	}

	if err := s.pruneStaleStacks(); err != nil {
		slog.Warn("Failed to prune stale stacks", "error", err)
	}

	s.warnProjectNameCollisions()

	slog.Info("Directory scan complete", "scanDepth", scanDepth)
	return hasGlobalEnv, nil
}

// warnProjectNameCollisions logs one WARN per compose project name that more
// than one persisted stack normalises to.
//
// Keyed on the NORMALISED NAME over every row, not on directory pairs: the
// collision can be two directories ("MyStack" and "my.stack" both become
// "mystack"; /a/stacks/web and /b/stacks/web across two roots) OR two named
// compose files in ONE directory ("compose.api.v2.yaml" and
// "compose.apiv2.yaml" both become "mystack-apiv2"), and a directory-keyed
// check is silent on the second.
//
// Compose itself would treat the colliding stacks as ONE project — same
// label, same `-p` — so this is not something Capstan can fix, only surface.
// Every row is kept: their IDs are path-and-profile based and distinct, and
// dropping one would hide a compose file that is on disk. It reads the
// persisted rows after the scan rather than threading state through
// ScanDirectoryWithRoot, so a collision with a stack persisted by an earlier
// scan is reported too, and single-directory rescans need no extra state.
func (s *ScannerService) warnProjectNameCollisions() {
	stacks, err := s.db.ListStacks()
	if err != nil {
		slog.Warn("Failed to list stacks for project name collision check", "error", err)
		return
	}

	sourcesByProject := make(map[string][]string)
	for _, stack := range stacks {
		source := filepath.Join(stack.Directory, stack.ComposeFile)
		sourcesByProject[stack.ProjectName] = append(sourcesByProject[stack.ProjectName], source)
	}

	for _, project := range slices.Sorted(maps.Keys(sourcesByProject)) {
		sources := sourcesByProject[project]
		if len(sources) < 2 {
			continue
		}
		sort.Strings(sources)
		slog.Warn("Compose project name collision: several stacks normalise to one project name; Docker Compose treats them as ONE project, so they share containers and Start/Stop on any of them acts on all",
			"project", project, "stacks", sources)
	}
}

func (s *ScannerService) scanDirectoryRecursive(path string, rootDir string, maxDepth int, currentDepth int) {
	entries, err := os.ReadDir(path)
	if err != nil {
		slog.Warn("Failed to read directory", "path", path, "error", err)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		dirPath := filepath.Join(path, entry.Name())

		if err := s.ScanDirectoryWithRoot(dirPath, rootDir); err != nil {
			slog.Warn("Failed to scan directory", "path", dirPath, "error", err)
			continue
		}

		if currentDepth < maxDepth {
			s.scanDirectoryRecursive(dirPath, rootDir, maxDepth, currentDepth+1)
		}
	}
}

// defaultScanDepth is the depth used when scan_depth carries no usable value:
// one level below each stacks root, which is the behaviour a fresh install has
// always had.
const defaultScanDepth = 1

// readScanDepth resolves the configured scan depth, separating "no row" from
// "this database could not answer".
//
// db.GetSetting returns the bare Scan error (database/settings.go:14-19), so an
// absent row arrives as sql.ErrNoRows. Mapping that — and only that — to the
// default keeps the fresh-install path byte-for-byte, while every other failure
// becomes an error the caller must handle. Same split as
// services/backup_config.go's readSetting (agent-os-7lg1, agent-os-l42o).
//
// Before agent-os-obgr both callers bound the error and then tested only
// `err == nil && v != ""`, on the same line as the GetSetting call, so an
// unreadable database and an unconfigured setting were the same event. In
// pruneStaleStacks that silently collapsed the depth to 1 and then DELETED the
// directories row — git_auth_type, git_ssh_key_path, git_https_user and the
// encrypted git_https_token with it — of every stack below depth 1, plus their
// stack rows by the ON DELETE CASCADE at migrations.go:151. A transient lock
// (busy_timeout is 5000 ms, database.go:98) was enough to trigger it.
//
// The old line is described above rather than quoted: quoting it verbatim would
// make this comment a permanent false positive for the single-line sweep this
// family of defects is found with.
func (s *ScannerService) readScanDepth() (int, error) {
	if s.db == nil {
		return defaultScanDepth, nil
	}
	depthStr, err := s.db.GetSetting("scan_depth")
	switch {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows):
		return defaultScanDepth, nil
	default:
		return 0, fmt.Errorf("read scan_depth setting: %w", err)
	}
	// An empty, non-numeric or out-of-range stored value is a value this code
	// cannot use, not a database fault: keep the pre-existing silent fallback
	// rather than turning a bad setting into a refusal to scan.
	if v, parseErr := strconv.Atoi(depthStr); parseErr == nil && v >= 1 {
		return v, nil
	}
	return defaultScanDepth, nil
}

func (s *ScannerService) pruneStaleStacks() error {
	allDirs := s.config.GetAllStacksDirs()

	// Guarded BEFORE activeDirs is built, not merely before the delete loops:
	// activeDirs is also the input to pruneStaleIDStacks at the tail of this
	// function, which deletes rows too. Refusing after building a wrong
	// activeDirs is not refusing.
	scanDepth, depthErr := s.readScanDepth()
	if depthErr != nil {
		// This ERROR is emitted here, where the cause is known, and not left to
		// the caller: ScanAll logs a prune failure as
		// slog.Warn("Failed to prune stale stacks", ...) (scanner.go:508), a
		// WARN naming no cause, which tells an operator nothing about a fault
		// that would otherwise have destroyed credentials.
		slog.Error("Refusing to prune stale stacks: the scan depth setting could not be read, and pruning at the default depth would delete the directory row of every stack below depth 1, taking its stacks by cascade and its git credentials permanently", "cause", depthErr)
		return depthErr
	}

	activeDirs := make(map[string]bool)
	for _, stacksDir := range allDirs {
		collectActiveDirs(stacksDir, scanDepth, 1, activeDirs)
	}

	directories, err := s.db.ListDirectories()
	if err != nil {
		return err
	}
	for _, dir := range directories {
		if !activeDirs[dir.Path] {
			// The next scan re-evaluates and retries this delete, so a
			// single failure isn't fatal to the sweep — but a delete that
			// keeps failing drifts the DB from disk indefinitely with
			// nothing to notice it, so surface it.
			if delErr := s.db.DeleteDirectory(dir.Path); delErr != nil {
				slog.Warn("Failed to delete stale directory row", "path", dir.Path, "error", delErr)
			}
		}
	}

	stacks, err := s.db.ListStacks()
	if err != nil {
		return err
	}

	for _, stack := range stacks {
		if !activeDirs[stack.Directory] {
			// See the DeleteDirectory comment above: retried next scan, but
			// a persistent failure should not go unnoticed.
			if delErr := s.db.DeleteStack(stack.ID); delErr != nil {
				slog.Warn("Failed to delete stale stack row", "stackID", stack.ID, "error", delErr)
			}
		}
	}

	return s.pruneStaleIDStacks(directories, activeDirs)
}

// pruneStaleIDStacks removes rows left behind when a stack's computed ID
// changes while its directory stays put (agent-os-ufv). StackIDRootPrefix's
// "Known residual" comment above explains when this happens: two EXTRA roots
// that collide with each other, or an extra that starts colliding once a
// later extra is added. The directory-existence pass above does not catch
// this — the directory is still active — so the OLD row (minted under the
// previous ID) survives while ScanDirectoryWithRoot upserts a NEW row under
// the current ID: one stack, two rows.
//
// THE HAZARD this guards against: mistaking "the scanner didn't visit this
// directory on this pass" (temporarily unreadable compose file, a scan_depth
// change, a permission error skipping a subtree) for "this row's ID is
// stale". Recomputing an expected ID and deleting any row that fails to match
// it would delete live stacks the scan simply hadn't reached yet.
//
// The safe predicate: only delete a row when another row for the exact same
// (Directory, ComposeFile) pair already carries the ID the scanner would mint
// right now. That pairing is unique per logical stack — ComposeFile fixes
// extractStackName, and the directory's current root fixes the rest of
// StackID — so two rows sharing it can only come from an ID change, never
// from an unrelated multi-compose-file directory (agent-os-w8o), which has
// distinct ComposeFile values and therefore never shares the key. And a
// "currently correct" sibling row only exists if THIS scan (or a prior one)
// actually wrote it, which only happens for directories the scanner visited —
// so a not-yet-visited directory's lone row is left alone, never treated as
// stale on recomputation alone.
//
// ACCEPTED COST (agent-os-ufv, deliberately not solved here): the six
// unenforced TEXT columns holding a stack ID (action_log.stack_id,
// cached_updates.stack_id, update_history.stack_id, backup_run_items.stack_id,
// auto_update_policies.target_id, backup_policies.target_id — see
// StackIDRootPrefix's doc comment) still reference the deleted row's old ID
// and are orphaned, not carried across. The MigrateStackIDsToRootPrefixed
// re-ID-plus-carry-across approach was considered and not chosen.
func (s *ScannerService) pruneStaleIDStacks(directories []models.Directory, activeDirs map[string]bool) error {
	dirRoots := make(map[string]string, len(directories))
	for _, dir := range directories {
		if activeDirs[dir.Path] {
			dirRoots[dir.Path] = dir.RootDir
		}
	}

	stacks, err := s.db.ListStacks()
	if err != nil {
		return err
	}

	type dirFileKey struct {
		dir  string
		file string
	}
	byDirFile := make(map[dirFileKey][]models.Stack)
	for _, stack := range stacks {
		if !activeDirs[stack.Directory] {
			continue
		}
		key := dirFileKey{stack.Directory, stack.ComposeFile}
		byDirFile[key] = append(byDirFile[key], stack)
	}

	for key, group := range byDirFile {
		if len(group) < 2 {
			continue
		}

		effectiveRoot, ok := dirRoots[key.dir]
		if !ok || effectiveRoot == "" {
			continue
		}

		expectedID := s.expectedStackID(key.dir, effectiveRoot, key.file)

		hasExpected := false
		for _, st := range group {
			if st.ID == expectedID {
				hasExpected = true
				break
			}
		}
		if !hasExpected {
			// No row in this group is proven current yet — the scan may not
			// have visited this directory this pass. Leave every row alone.
			continue
		}

		for _, st := range group {
			if st.ID == expectedID {
				continue
			}
			if delErr := s.db.DeleteStack(st.ID); delErr != nil {
				slog.Warn("Failed to delete stale-ID stack row", "stackID", st.ID, "directory", st.Directory, "error", delErr)
			}
		}
	}

	return nil
}

// expectedStackID recomputes the ID the scanner would mint right now for a
// stack, given the directory's currently-recorded effective root and a
// compose filename. It duplicates, rather than shares, the relPath/
// stackPathID derivation in ScanDirectoryWithRoot (scanner.go:373-382,
// 445-446) — a considered tradeoff, not an oversight: if the two ever drift
// apart, this function computes an ID matching no row in the group, so
// pruneStaleIDStacks's hasExpected check comes back false and nothing gets
// deleted. Drift therefore fails closed (rows survive, at worst stranding a
// stale one a little longer) rather than open (rows get wrongly deleted),
// which is why the duplication is acceptable here even though DRY is the
// default elsewhere in this file.
func (s *ScannerService) expectedStackID(dirPath, effectiveRoot, composeFile string) string {
	relPath := ""
	if effectiveRoot != "" {
		if rel, err := filepath.Rel(effectiveRoot, dirPath); err == nil {
			relPath = rel
		}
	}
	if relPath == "" {
		relPath = filepath.Base(dirPath)
	}
	stackPathID := strings.ReplaceAll(relPath, string(filepath.Separator), "-")
	name := extractStackName(composeFile)
	return StackID(effectiveRoot, s.config.StacksDir, s.config.ExtraStacksDirs, stackPathID, name)
}

func collectActiveDirs(path string, maxDepth int, currentDepth int, active map[string]bool) {
	active[path] = true
	entries, err := os.ReadDir(path)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dirPath := filepath.Join(path, entry.Name())
		active[dirPath] = true
		if currentDepth < maxDepth {
			collectActiveDirs(dirPath, maxDepth, currentDepth+1, active)
		}
	}
}

func (s *ScannerService) ScanDirectory(path string) error {
	return s.ScanDirectoryWithRoot(path, "")
}

// directoryRecord is what RegisterDirectory and ScanDirectoryWithRoot both
// derive from a path on disk: the models.Directory row plus the git fields
// ScanDirectoryWithRoot also needs when it builds any stack it discovers
// underneath. Extracted so the two callers share one source of truth instead
// of two copies of the same git/root-path probing drifting apart.
type directoryRecord struct {
	directory models.Directory
	isGitRepo bool
	gitBranch string
}

// resolveEffectiveRoot picks which configured root contains path, for the
// rootDir="" case: ScanDirectory (and through it, watcher.go's performRescan
// on every debounced file change) does not know which configured root it was
// called for and must work it out itself.
//
// Containment is tested with filepath.Rel plus a ".." check — the same idiom
// handlers/stack_crud.go's Create path-traversal guard uses — rather than a
// bare strings.HasPrefix, which has no path-separator boundary and so would
// match a root like ".../stacks" against an unrelated sibling directory like
// ".../stacks-extra" (agent-os-509).
//
// Multiple configured roots can legitimately contain the same path when one
// root is nested inside another (e.g. ".../stacks" and ".../stacks/team" are
// both configured). The LONGEST matching root wins, since it is always the
// most specific one regardless of the order roots appear in config — a first-
// match rule would make the outcome depend on GetAllStacksDirs' order, which
// is config-file order, not path specificity.
func resolveEffectiveRoot(path string, roots []string) string {
	best := ""
	for _, root := range roots {
		if root == "" || !rootContainsPath(root, path) {
			continue
		}
		if len(root) > len(best) {
			best = root
		}
	}
	return best
}

// rootContainsPath reports whether path is root itself or lies strictly
// beneath it, using filepath.Abs + filepath.Rel so relative and
// trailing-separator spellings of root compare correctly (mirrors
// handlers/stack_crud.go's Create guard).
func rootContainsPath(root, path string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	if absRoot == absPath {
		return true
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *ScannerService) buildDirectoryRecord(path string, rootDir string) directoryRecord {
	dirName := filepath.Base(path)
	isGitRepo := false
	gitRemote := ""
	gitBranch := ""

	gitPath := filepath.Join(path, ".git")
	if _, err := os.Stat(gitPath); err == nil {
		isGitRepo = true

		headPath := filepath.Join(gitPath, "HEAD")
		//nolint:gosec // path is reached only by recursing from the configured stacks directories (ScanAll -> scanDirectoryRecursive), never external input
		if content, err := os.ReadFile(headPath); err == nil {
			headContent := string(content)
			if strings.HasPrefix(headContent, "ref: refs/heads/") {
				gitBranch = strings.TrimPrefix(headContent, "ref: refs/heads/")
				gitBranch = strings.TrimSpace(gitBranch)
			}
		}
	}

	effectiveRoot := rootDir
	if effectiveRoot == "" {
		effectiveRoot = resolveEffectiveRoot(path, s.config.GetAllStacksDirs())
	}

	return directoryRecord{
		directory: models.Directory{
			Path:      path,
			Name:      dirName,
			RootDir:   effectiveRoot,
			IsGitRepo: isGitRepo,
			GitRemote: gitRemote,
			GitBranch: gitBranch,
		},
		isGitRepo: isGitRepo,
		gitBranch: gitBranch,
	}
}

// RegisterDirectory ensures a directories row exists for path without scanning
// it for compose files or touching the stacks table. It exists so a caller that
// is about to insert a stacks row referencing this path (stacks.directory has
// an FK to directories.path, ON DELETE CASCADE — see migrations.go) can
// satisfy that FK first, without paying for a full ScanDirectoryWithRoot pass
// (agent-os-jcu: POST /api/v1/stacks was inserting the stack row for a
// brand-new directory before any row for that directory existed, which
// 500'd under pool-wide foreign_keys enforcement).
//
// created reports whether this call inserted the row, so the caller can undo
// its own registration via UnregisterDirectory without touching a row that was
// already there. That distinction matters because UnregisterDirectory cascades:
// see its doc comment.
func (s *ScannerService) RegisterDirectory(path string, rootDir string) (created bool, err error) {
	if _, err := os.ReadDir(path); err != nil {
		return false, err
	}
	rec := s.buildDirectoryRecord(path, rootDir)
	return s.db.InsertDirectoryIfAbsent(rec.directory)
}

// UnregisterDirectory removes the directories row for path. It is the
// rollback counterpart to RegisterDirectory in ONE case only: where that call
// reported created == true. If a caller registers the directory to satisfy the
// stacks FK, inserts it itself, and then fails to insert the stack row, this
// undoes the registration so a partial Create does not leave a directory row
// with no stack behind it.
//
// It is NOT a general undo, and calling it for a row this caller did not insert
// is a data-loss bug. stacks.directory has ON DELETE CASCADE onto
// directories.path, so removing the row also removes EVERY stacks row whose
// directory equals path — and one directory legitimately holds several stacks,
// one per compose file (agent-os-w8o).
func (s *ScannerService) UnregisterDirectory(path string) error {
	return s.db.DeleteDirectory(path)
}

func (s *ScannerService) ScanDirectoryWithRoot(path string, rootDir string) error {
	_, err := os.ReadDir(path)
	if err != nil {
		return err
	}

	rec := s.buildDirectoryRecord(path, rootDir)
	dirName := rec.directory.Name
	isGitRepo := rec.isGitRepo
	gitBranch := rec.gitBranch
	effectiveRoot := rec.directory.RootDir

	relPath := ""
	if effectiveRoot != "" {
		rel, err := filepath.Rel(effectiveRoot, path)
		if err == nil {
			relPath = rel
		}
	}
	if relPath == "" {
		relPath = dirName
	}

	if err := s.db.UpsertDirectory(rec.directory); err != nil {
		return err
	}

	patterns := []string{
		"compose*.yaml",
		"compose*.yml",
		"docker-compose*.yaml",
		"docker-compose*.yml",
	}

	// seenNames tracks, per extracted stack name, the filename that already
	// claimed it — so a later file mapping to the same name can be skipped
	// with a warning that names both files (see the guard below).
	seenNames := make(map[string]string)

	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(path, pattern))
		if err != nil {
			continue
		}

		for _, match := range matches {
			filename := filepath.Base(match)

			if strings.HasPrefix(filename, ".") {
				continue
			}

			if strings.HasSuffix(filename, ".override.yaml") || strings.HasSuffix(filename, ".override.yml") {
				continue
			}

			name := extractStackName(filename)

			// extractStackName strips both the "compose." and
			// "docker-compose." prefixes, so e.g. compose.api.yaml and
			// docker-compose.api.yml both yield "api". Without this guard the
			// second file's UpsertStack (INSERT OR REPLACE) would silently
			// overwrite the first file's row, leaving the other compose file
			// on disk but untracked: invisible in the UI, never brought
			// down, and left behind on delete (agent-os-4yy). This mirrors
			// the "default" handling that already existed here, generalized
			// to every stack name.
			//
			// The winner is whichever file the patterns loop above reaches
			// first, which is deterministic and NOT directory-read order:
			// the patterns slice fixes compose*.yaml > compose*.yml >
			// docker-compose*.yaml > docker-compose*.yml (Docker Compose's
			// own file precedence), and filepath.Glob sorts each pattern's
			// matches alphabetically. So the outcome is stable across
			// filesystems.
			if winner, seen := seenNames[name]; seen {
				slog.Warn("Duplicate stack name, skipping", "directory", path, "name", name, "kept", winner, "skipped", filename)
				continue
			}

			seenNames[name] = filename

			envFile := determineEnvFile(path, name)

			stackPathID := strings.ReplaceAll(relPath, string(filepath.Separator), "-")
			stackID := StackID(effectiveRoot, s.config.StacksDir, s.config.ExtraStacksDirs, stackPathID, name)

			// context.Background(): the scan has no cancellable caller today
			// (ScanAll is driven from boot and from a POST that does not thread
			// one through). ResolveComposeProjectName takes a ctx anyway because
			// the create path DOES have one, and per-file compose loads are the
			// only thing in this loop worth cancelling.
			projectName, projectNameSource := ResolveComposeProjectName(context.Background(), match, dirName, name)
			if projectName == "" {
				// Compose derives NOTHING from this directory name, and refuses
				// to run with an empty project name, so no compose project can
				// exist for it. A row with project_name "" would be unlistable
				// (the label filter matches nothing) and unstartable (-p ""
				// relays compose's error with less context). Warn and skip.
				slog.Warn("Directory name yields an empty compose project name, skipping",
					"directory", path, "file", filename,
					"rule", "compose keeps only lowercase letters, digits, '-' and '_' and trims leading '-'/'_'")
				continue
			}

			stack := models.Stack{
				ID:          stackID,
				Directory:   path,
				ComposeFile: filename,
				EnvFile:     envFile,
				ProjectName: projectName,
				Status:      "unknown",
				IsGitRepo:   isGitRepo,
				GitBranch:   gitBranch,
			}

			if err := s.db.UpsertStack(stack); err != nil {
				return err
			}

			slog.Info("Discovered stack", "id", stackID, "project", projectName, "projectSource", projectNameSource, "root", effectiveRoot)
		}
	}

	return nil
}

func extractStackName(filename string) string {
	name := filename

	name = strings.TrimSuffix(name, ".yaml")
	name = strings.TrimSuffix(name, ".yml")

	if name == "compose" || name == "docker-compose" {
		return "default"
	}

	name = strings.TrimPrefix(name, "compose.")
	name = strings.TrimPrefix(name, "docker-compose.")

	if name == "" {
		return "default"
	}

	return name
}

func determineEnvFile(dirPath, stackName string) string {
	if stackName == "default" {
		envPath := filepath.Join(dirPath, ".env")
		if _, err := os.Stat(envPath); err == nil {
			return ".env"
		}
		return ""
	}

	envPath := filepath.Join(dirPath, fmt.Sprintf(".env.%s", stackName))
	if _, err := os.Stat(envPath); err == nil {
		return fmt.Sprintf(".env.%s", stackName)
	}

	envPath = filepath.Join(dirPath, ".env")
	if _, err := os.Stat(envPath); err == nil {
		return ".env"
	}

	return ""
}
