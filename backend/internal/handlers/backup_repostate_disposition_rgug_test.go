package handlers

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// readableRepoStates are the states in which the repository can be read, or
// is known not to exist: the snapshot routes answer them without
// repoCouldNotBeRead's 503.
var readableRepoStates = map[services.RepoState]bool{
	services.RepoStateOK:            true,
	services.RepoStateUninitialized: true,
}

// frontendTypesPath is the hand-written frontend declaration of the repoState
// unions. It is TypeScript, so a go test -overlay mutant has nothing to do
// with it and reading it from disk is fine.
var frontendTypesPath = filepath.Join("..", "..", "..", "frontend", "src", "types", "index.ts")

// repoStateUnion matches one `repoState:` union and captures its members.
var (
	repoStateUnion  = regexp.MustCompile(`repoState:((?:\s*\|\s*'[a-z_]*')+)`)
	repoStateMember = regexp.MustCompile(`'([a-z_]*)'`)
)

// TestEveryRepoStateIsDispositioned (agent-os-rgug): repoCouldNotBeRead is a
// whitelist, so a RepoState it does not name falls through to the snapshot
// routes and answers 500. Every state in services.AllRepoStates (kept equal to
// the declared constants by services' own guard) must be either in
// repoCouldNotBeRead or on readableRepoStates, never both, and must appear in
// every repoState union in frontend/src/types/index.ts.
func TestEveryRepoStateIsDispositioned(t *testing.T) {
	src, err := os.ReadFile(frontendTypesPath) //nolint:gosec // a fixed path inside this repository, not input
	require.NoError(t, err, "the frontend types file must be present (run from a full checkout, not a backend-only copy)")
	unions := repoStateUnion.FindAllStringSubmatch(string(src), -1)
	// Positive control: AvailabilityResponse and BackupStatus each declare one.
	require.Len(t, unions, 2, "expected the two repoState unions in %s; the matcher is blind or the file changed", frontendTypesPath)
	members := make([]map[string]bool, len(unions))
	for i, u := range unions {
		members[i] = map[string]bool{}
		for _, m := range repoStateMember.FindAllStringSubmatch(u[1], -1) {
			members[i][m[1]] = true
		}
	}

	known := map[string]bool{"": true}
	for _, s := range services.AllRepoStates {
		known[string(s)] = true
		unreadable := repoCouldNotBeRead(services.BackupAvailability{RepoState: s})
		switch {
		case unreadable && readableRepoStates[s]:
			t.Errorf("RepoState %q is both in repoCouldNotBeRead and on readableRepoStates", s)
		case !unreadable && !readableRepoStates[s]:
			t.Errorf("RepoState %q is in neither repoCouldNotBeRead nor readableRepoStates: the snapshot routes would answer it with a 500 (agent-os-rgug)", s)
		}
		for i := range members {
			assert.True(t, members[i][string(s)], "RepoState %q is missing from repoState union #%d in %s", s, i+1, frontendTypesPath)
		}
	}
	for i := range members {
		for m := range members[i] {
			assert.True(t, known[m], "repoState union #%d in %s has %q, which is not a services.RepoState", i+1, frontendTypesPath, m)
		}
	}
	assert.GreaterOrEqual(t, len(services.AllRepoStates), 7, "positive control: AllRepoStates lists the seven known states")
}
