package services

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-rbmi round-2 review F2: a locked session's logs and inspect are
// masked with what the stack's containers really hold, not only with the
// values as the files write them.

type rtSecretsFake struct {
	containerUpdateAPI // unused methods panic

	mu           sync.Mutex
	list         []container.Summary
	listErr      error
	inspects     map[string]container.InspectResponse
	listCalls    int
	inspectCalls int
	lastFilter   string
	lastDeadline time.Duration
}

func (f *rtSecretsFake) ContainerList(ctx context.Context, opts container.ListOptions) ([]container.Summary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	f.lastFilter = strings.Join(opts.Filters.Get("label"), ",")
	if d, ok := ctx.Deadline(); ok {
		f.lastDeadline = time.Until(d)
	}
	if !opts.All {
		return nil, errors.New("fake: the lookup must list stopped containers too")
	}
	return f.list, f.listErr
}

func (f *rtSecretsFake) ContainerInspect(_ context.Context, id string) (container.InspectResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inspectCalls++
	return f.inspects[id], nil
}

func rtInspect(env, cmd, entrypoint, health []string) container.InspectResponse {
	cfg := &container.Config{Env: env, Cmd: cmd, Entrypoint: entrypoint}
	if health != nil {
		cfg.Healthcheck = &container.HealthConfig{Test: health}
	}
	return container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{}, Config: cfg}
}

// Every class the file list misses (review Q1 K, K3, K5, L, L2, L3, T, N):
// the compose file writes the value in a form the list cannot read as the
// container gets it, and the container's own Env and argv hold it.
func TestLockedSecretValues_MasksWhatTheContainersHold(t *testing.T) {
	dir := writeStackFiles(t, map[string]string{
		"compose.yaml": `services:
  app:
    image: app:1
    command: app --password=kkPw12qq --token k3Pw12qq
    entrypoint: sh -c "PASSWORD=k5Pw12qq exec app"
    healthcheck:
      test: ["CMD", "check", "--password", "hcPw22qq"]
    environment:
      DB_PASSWORD: ${DB_PASSWORD:-llPw13qq}
      API_TOKEN: l3Pw$$13qq
      SMTP_PASSWORD: ttPre21${SFX}
      DATABASE_URL: postgres://u:nnPw15qq@db/x
      TZ: Europe/Amsterdam
      SHORT_PASSWORD: abc
`,
		".env": "SFX=ttSuf21\n",
	})
	stack := models.Stack{Directory: dir, ComposeFile: "compose.yaml", ProjectName: "rtproj"}
	fake := &rtSecretsFake{
		list: []container.Summary{{ID: "c1", Created: 100}},
		inspects: map[string]container.InspectResponse{"c1": rtInspect(
			[]string{"DB_PASSWORD=llPw13qq", "API_TOKEN=l3Pw$13qq", "SMTP_PASSWORD=ttPre21ttSuf21",
				"DATABASE_URL=postgres://u:nnPw15qq@db/x", "TZ=Europe/Amsterdam", "SHORT_PASSWORD=abc", "PATH=/usr/bin"},
			[]string{"app", "--password=kkPw12qq", "--token", "k3Pw12qq"},
			[]string{"sh", "-c", "PASSWORD=k5Pw12qq exec app"},
			[]string{"CMD", "check", "--password", "hcPw22qq"},
		)},
	}
	svc := &DockerService{updateClient: fake}

	got, err := svc.LockedSecretValues(context.Background(), stack)
	require.NoError(t, err)
	for _, v := range []string{"kkPw12qq", "k3Pw12qq", "k5Pw12qq", "hcPw22qq", "llPw13qq", "l3Pw$13qq", "ttPre21ttSuf21", "nnPw15qq"} {
		line, _ := MaskSecrets("app started, connecting with "+v+" ok", "", got)
		require.NotContains(t, line, v, "a locked log line shows %q, which the container holds", v)
	}
	require.NotContains(t, got, "Europe/Amsterdam", "a value under a non-sensitive name is not a secret")
	require.NotContains(t, got, "/usr/bin")
	require.NotContains(t, got, "abc", "the list's own length filter applies (composeSecretMinLen)")
	require.Equal(t, "com.docker.compose.project=rtproj", fake.lastFilter, "only the stack's own containers")
	require.Positive(t, fake.lastDeadline, "the lookup is bounded")
	require.LessOrEqual(t, fake.lastDeadline, runtimeSecretLookupTimeout)
}

// A container is inspected once per ID and creation time; one that is gone
// from the list is forgotten, and a recreated one is inspected again.
func TestRuntimeSecretValues_InspectsEachContainerOnce(t *testing.T) {
	fake := &rtSecretsFake{
		list:     []container.Summary{{ID: "c1", Created: 1}},
		inspects: map[string]container.InspectResponse{"c1": rtInspect([]string{"API_TOKEN=cachedTok1qq"}, nil, nil, nil)},
	}
	svc := &DockerService{updateClient: fake}
	stack := models.Stack{ProjectName: "rtcache"}
	for range 3 {
		got, err := svc.RuntimeSecretValues(context.Background(), stack)
		require.NoError(t, err)
		require.Contains(t, got, "cachedTok1qq")
	}
	require.Equal(t, 3, fake.listCalls)
	require.Equal(t, 1, fake.inspectCalls, "an unchanged container is not inspected again")

	fake.list = []container.Summary{{ID: "c1", Created: 2}}
	_, err := svc.RuntimeSecretValues(context.Background(), stack)
	require.NoError(t, err)
	require.Equal(t, 2, fake.inspectCalls, "a new creation time is inspected again")

	fake.list = nil
	_, err = svc.RuntimeSecretValues(context.Background(), stack)
	require.NoError(t, err)
	require.Empty(t, svc.runtimeSecrets.project("rtcache"), "a container no longer listed is forgotten")
}

// A failed lookup never shrinks the list: the file values stay, and so do
// the values cached from the last good lookup.
func TestLockedSecretValues_LookupErrorKeepsFileAndCachedValues(t *testing.T) {
	dir := writeStackFiles(t, map[string]string{
		"compose.yaml": "services:\n  app:\n    environment:\n      DB_PASSWORD: fileValue61qq\n",
	})
	stack := models.Stack{Directory: dir, ComposeFile: "compose.yaml", ProjectName: "rterr"}
	fake := &rtSecretsFake{
		list:     []container.Summary{{ID: "c1", Created: 1}},
		inspects: map[string]container.InspectResponse{"c1": rtInspect([]string{"API_TOKEN=runtimeTok62qq"}, nil, nil, nil)},
	}
	svc := &DockerService{updateClient: fake}
	got, err := svc.LockedSecretValues(context.Background(), stack)
	require.NoError(t, err)
	require.Contains(t, got, "runtimeTok62qq", "CONTROL: the good lookup finds the container's value")

	fake.listErr = errors.New("daemon gone")
	got, err = svc.LockedSecretValues(context.Background(), stack)
	require.NoError(t, err, "a lookup failure masks with what is known, it does not refuse")
	require.Contains(t, got, "fileValue61qq")
	require.Contains(t, got, "runtimeTok62qq", "the cached container value stays after a failed lookup")
}

// C3 (orch-dm-62): masking a line with the values read from the masker's
// output (spanValues over maskedSpans) hides at least what the masker hides,
// for every shape the line-up meets.
func TestMaskedSpans_HidesWhatTheMaskerHides(t *testing.T) {
	// needleOnly masks text with the values alone, no name or flag pass, as
	// a log line that prints a value bare is masked.
	needleOnly := func(text string, values []string) string {
		var set secretSet
		for _, v := range values {
			set.add(v)
		}
		out, _ := maskNeedleUnion(text, maskedNeedleForms(secretNeedles(set.sorted())), secretMask)
		return out
	}
	viaMaskArgs := []struct {
		name    string
		args    []string
		secrets []string
	}{
		{"single mask", []string{"app", "--password=singleA11qq"}, []string{"singleA11qq"}},
		{"several masks in one item", []string{"sh", "-c", "PASSWORD=sevA11qq API_TOKEN=sevB22qq app --secret sevC33qq"},
			[]string{"sevA11qq", "sevB22qq", "sevC33qq"}},
		{"after-flag two-item form", []string{"app", "--password", "twoItem44qq"}, []string{"twoItem44qq"}},
		{"URL credential", []string{"app", "postgres://u:urlPw55qq@db/x"}, []string{"urlPw55qq"}},
		{"orig already holds the mask", []string{"app", "--password=***pre66qq"}, []string{"***pre66qq"}},
		// R3-1 (rv-dm-rbmi2 round 3): shell-form lines, where the masker
		// hides the rest of the line and the value ends at a shell
		// separator, not a space. Separators covered: space, ";", "&", "|",
		// ")", attached to the value or after it.
		{"sh -c: value then ; then a command", []string{"sh", "-c", "PASSWORD=sc1Pw123; exec app"}, []string{"sc1Pw123"}},
		{"sh -c: value then ; with no space", []string{"sh", "-c", "PASSWORD=sc2Pw123;exec app"}, []string{"sc2Pw123"}},
		{"sh -c: --flag=value then ;", []string{"sh", "-c", "app --password=sc3Pw123; sleep 1"}, []string{"sc3Pw123"}},
		{"sh -c: value then && attached", []string{"sh", "-c", "PASSWORD=sc10Pw123&& exec app"}, []string{"sc10Pw123"}},
		{"sh -c: value then | attached", []string{"sh", "-c", "PASSWORD=sc11Pw123| tee /log"}, []string{"sc11Pw123"}},
		{"sh -c: value in a subshell", []string{"sh", "-c", "(export PASSWORD=sc12Pw123) && exec app"}, []string{"sc12Pw123"}},
		{"sh -c: URL credential inside a longer line", []string{"sh", "-c", "app --db-url postgres://u:sc8Pw123@db/x --verbose"}, []string{"sc8Pw123"}},
		// Controls the reviewer found already hidden; they stay so.
		{"control: value then && after a space", []string{"sh", "-c", "app --password=sc4Pw123 && sleep 1"}, []string{"sc4Pw123"}},
		{"control: two exports", []string{"sh", "-c", "export DB_PASSWORD=sc5Pw123 API_TOKEN=sc6Tok123 && exec app"}, []string{"sc5Pw123", "sc6Tok123"}},
		{"control: mongodb URL with a port and query", []string{"app", "mongodb://u:sc7Pw123@h:27017/db?authSource=admin"}, []string{"sc7Pw123"}},
	}
	for _, c := range viaMaskArgs {
		t.Run(c.name, func(t *testing.T) {
			masked := MaskArgs(c.args, nil)
			var values []string
			for i := range c.args {
				values = append(values, spanValues(c.args[i], masked[i], spanValuesDepth)...)
			}
			for _, s := range c.secrets {
				require.NotContains(t, strings.Join(masked, " "), s, "CONTROL: MaskArgs hides %q", s)
				line := needleOnly("log: "+s+" end", values)
				require.NotContains(t, line, s, "maskedSpans' values %q leave %q visible", values, s)
			}
		})
	}

	// An env value is read the way containerSecretValues reads one: its
	// spans under MaskSecrets.
	for name, c := range map[string]struct{ value, secret string }{
		"env: URL credential then more words":  {"postgres://u:sc9Pw123@db/x --verbose", "sc9Pw123"},
		"env: KEY=value then ; in an opts var": {"-Dx=1 DB_PASSWORD=sc13Pw123; -Xmx1g", "sc13Pw123"},
	} {
		t.Run(name, func(t *testing.T) {
			masked, _ := MaskSecrets(c.value, "", nil)
			require.NotContains(t, masked, c.secret, "CONTROL: the masker hides %q", c.secret)
			values := spanValues(c.value, masked, spanValuesDepth)
			require.NotContains(t, needleOnly("log: "+c.secret+" end", values), c.secret, "values %q leave %q visible", values, c.secret)
		})
	}

	// The line-up itself, on masked text built by hand: where a piece
	// between two masks also sits inside the first span, only the rightmost
	// line-up finds the first span whole.
	t.Run("piece inside the span before it", func(t *testing.T) {
		values := maskedSpans("k=aaaa-bbbb-cccc", "k=***-***")
		for _, s := range []string{"aaaa-bbbb", "cccc"} {
			require.NotContains(t, needleOnly("log: "+s+" end", values), "bbbb", "values %q leave part of %q visible", values, s)
		}
	})
	t.Run("no line-up returns the whole value", func(t *testing.T) {
		require.Equal(t, []string{"origVal77qq"}, maskedSpans("origVal77qq", "zz***"))
		require.Equal(t, []string{"aa***b88qq", "aa", "b88qq"}, maskedSpans("aa***b88qq", "***"))
		require.Nil(t, maskedSpans("same99qq", "same99qq"), "nothing masked, nothing returned")
	})
}
