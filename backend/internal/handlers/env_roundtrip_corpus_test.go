package handlers

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One corpus for every reader and writer of an env file (agent-os-m7qr,
// agent-os-rd1l, agent-os-fw2q, agent-os-jx88): the stack env table and the
// global env page share one parser (EnvHandler.parseEnvFile) and one writer
// (serializeEnvFile, plus mergeGlobalEnv on the global page), and each fixture
// is checked against all of them, and against compose's own parser.

type envCorpusFixture struct {
	name, content string
	// secrets are values the unlocked global page shows and the locked one
	// must not.
	secrets []string
	// tableExact: an unedited stack-table save is byte-identical. The table
	// parser trims every line, so indentation, CRLF line ends and a missing
	// final newline are not kept (a known gap, filed as a follow-up).
	tableExact bool
	// composeDiffers names why the table's keys differ from compose's, when
	// they do on purpose; "" means they must match.
	composeDiffers string
}

func envCorpus() []envCorpusFixture {
	return []envCorpusFixture{
		{name: "comments", content: "# header\n\nA=1\n# note about B\nB=two words\n\n", tableExact: true},
		{name: "colon", content: "FOO\nBAR: 1\nAPI_TOKEN: a=corpuscolonsecret\n",
			secrets: []string{"corpuscolonsecret"}, tableExact: true},
		{name: "quotes", content: "Q1=\"a b\" # c\nQ2='x#y'\nU=v #note\nDB_PASSWORD=\"corpusquotedsecret\" # pw\n",
			secrets: []string{"corpusquotedsecret"}, tableExact: true},
		{name: "sensitive", content: "API_KEY=corpusplainsecret\nDATABASE_URL=postgres://u:corpusurlsecret@h/db\nTZ=UTC\n",
			secrets: []string{"corpusplainsecret", "corpusurlsecret"}, tableExact: true},
		{name: "long-line", content: "Z=0\nLONG=" + strings.Repeat("y", 70*1024) + "\nB=1\n", tableExact: true},
		{name: "export", content: "export EXP_TOKEN=corpusexportsecret\nPLAIN=1\n",
			secrets: []string{"corpusexportsecret"}, tableExact: true,
			composeDiffers: "pre-existing: the table keys an export line with its prefix (\"export EXP_TOKEN\"); compose reads EXP_TOKEN. Not in this cluster's beads."},
		{name: "multiline-quote", content: "A=1\nCERT_KEY=\"-----BEGIN\nabc: def\nTOP_SECRET_PASSWORD: corpusmultiinner\n-----END\" # pem\nPEM='l1\nl2=corpusmultisingle'\nB=2\n",
			secrets: []string{"corpusmultiinner"}, tableExact: true},
		{name: "duplicate-key", content: "A=1\n# between\nA=2\nB=3\nA=4\n", tableExact: true},
		{name: "empty", content: "", tableExact: true},
		{name: "indent-crlf-no-final-newline", content: "  A=1\r\n# c\r\nB=2"},
	}
}

// tableKeys is the variables the stack table shows: every row with a key.
func tableKeys(content string) []string {
	keys := []string{}
	for _, e := range (&EnvHandler{}).parseEnvFile(content) {
		if e.Key != "" {
			keys = append(keys, e.Key)
		}
	}
	slices.Sort(keys)
	// compose keeps one value per key; a repeated key is one variable.
	return slices.Compact(keys)
}

func TestEnvCorpus_StackTableUneditedSaveIsByteIdentical(t *testing.T) {
	for _, f := range envCorpus() {
		if !f.tableExact {
			continue
		}
		t.Run(f.name, func(t *testing.T) {
			got := serializeEnvFile((&EnvHandler{}).parseEnvFile(f.content))
			assert.True(t, got == f.content, "serialize(parse(x)) != x:\n got %.200q\nwant %.200q", got, f.content)
		})
	}
}

func TestEnvCorpus_GlobalUnchangedSaveIsByteIdentical(t *testing.T) {
	for _, f := range envCorpus() {
		t.Run(f.name, func(t *testing.T) {
			dataDir, router := globalEnvTestRouter(t)
			p := writeGlobalEnvFile(t, dataDir, f.content)
			vars, _ := getGlobalVars(t, router, "/unlocked")
			w := putGlobalVars(t, router, vars)
			require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
			got := readGlobalEnvTestFile(t, p)
			assert.True(t, got == f.content, "global save changed the file:\n got %.200q\nwant %.200q", got, f.content)
		})
	}
}

// Redaction parity (orch-dm-53 ruling D1a): the locked global page shows no
// secret of any fixture, while the unlocked page does (the positive side, so a
// fixture that never carried the secret cannot pass).
func TestEnvCorpus_GlobalLockedShowsNoSecret(t *testing.T) {
	for _, f := range envCorpus() {
		if len(f.secrets) == 0 {
			continue
		}
		t.Run(f.name, func(t *testing.T) {
			dataDir, router := globalEnvTestRouter(t)
			writeGlobalEnvFile(t, dataDir, f.content)
			_, unlocked := getGlobalVars(t, router, "/unlocked")
			_, locked := getGlobalVars(t, router, "/locked")
			for _, s := range f.secrets {
				require.Contains(t, unlocked, s, "positive side")
				assertBodyOmits(t, locked, s)
			}
		})
	}
}

// The table shows the variables compose reads, by key. Fixtures that differ
// on purpose say why; parity beyond this cluster's beads is not chased.
func TestEnvCorpus_KeysMatchCompose(t *testing.T) {
	inherit := func(string) (string, bool) { return "inherited", true }
	for _, f := range envCorpus() {
		t.Run(f.name, func(t *testing.T) {
			vars, err := dotenv.UnmarshalWithLookup(f.content, inherit)
			require.NoError(t, err, "compose reads the fixture")
			composeKeys := []string{}
			for k := range vars {
				composeKeys = append(composeKeys, k)
			}
			slices.Sort(composeKeys)
			if f.composeDiffers != "" {
				assert.NotEqual(t, composeKeys, tableKeys(f.content), "listed as differing (%s) but matches: drop the entry", f.composeDiffers)
				return
			}
			assert.Equal(t, composeKeys, tableKeys(f.content))
		})
	}
}
