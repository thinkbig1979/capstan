package services

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// agent-os-w6wx: every shape the orch ruling lists must mask, the masked text
// keeps its line count, and what holds no secret stays byte-identical.
func TestMaskSecretLines_MasksEveryShape(t *testing.T) {
	const s = "hunter2W6WX"
	cases := map[string]struct {
		path, text string
	}{
		"block mapping":         {"compose.yaml", "services:\n  db:\n    environment:\n      POSTGRES_PASSWORD: " + s + "\n      TZ: Europe/Amsterdam\n"},
		"flow mapping":          {"compose.yaml", "services:\n  db:\n    environment: { POSTGRES_PASSWORD: " + s + ", TZ: UTC }\n"},
		"flow sequence":         {"compose.yaml", "services:\n  db:\n    environment: [DB_PASSWORD=" + s + ", TZ=UTC]\n"},
		"list form":             {"compose.yaml", "services:\n  db:\n    environment:\n      - DB_PASSWORD=" + s + "\n      - TZ=UTC\n"},
		"quoted list item":      {"compose.yaml", "services:\n  db:\n    environment:\n      - \"DB_PASSWORD=" + s + "\"\n"},
		"double quoted value":   {"compose.yaml", "x:\n  API_TOKEN: \"" + s + "\" # note\n"},
		"single quoted value":   {"compose.yaml", "x:\n  API_TOKEN: '" + s + "'\n"},
		"next-line value":       {"compose.yaml", "x:\n  DB_PASSWORD:\n    " + s + "\n  TZ: UTC\n"},
		"literal block scalar":  {"compose.yaml", "x:\n  PRIVATE_KEY: |\n    -----BEGIN-----\n    " + s + "\n    -----END-----\n  TZ: UTC\n"},
		"folded block scalar":   {"compose.yaml", "x:\n  SECRET: >-\n    " + s + "\n    more\n"},
		"multi-line plain":      {"compose.yaml", "x:\n  DB_PASSWORD: first\n    " + s + "\n  TZ: UTC\n"},
		"multi-line quoted":     {"compose.yaml", "x:\n  DB_PASSWORD: \"first\n    " + s + "\"\n  TZ: UTC\n"},
		"nested under secret":   {"compose.yaml", "x:\n  SECRETS:\n    a: " + s + "\n    b: [" + s + "]\n"},
		"commented-out key":     {"compose.yaml", "x:\n  # DB_PASSWORD: " + s + "\n  TZ: UTC\n"},
		"unparseable yaml":      {"compose.yaml", "x:\n  DB_PASSWORD: " + s + "\n  : : bad [\n"},
		"default reference":     {"compose.yaml", "x:\n  DB_PASSWORD: ${DB_PASSWORD:-" + s + "}\n"},
		"dash default":          {"compose.yaml", "x:\n  DB_PASSWORD: ${DB_PASSWORD-" + s + "}\n"},
		"dotenv line":           {"app.env", "DB_PASSWORD=" + s + "\nTZ=UTC\n"},
		"export line":           {"run.sh", "#!/bin/sh\nexport API_KEY=" + s + "\n"},
		"conf colon":            {"app.conf", "auth_token: " + s + "\n"},
		"flag in command":       {"compose.yaml", "x:\n  command: [\"serve\", \"--db-password=" + s + "\"]\n"},
		"flag in shell":         {"run.sh", "exec app --db-password=" + s + " --port=80\n"},
		"url credential":        {"compose.yaml", "x:\n  DATABASE_URL: postgres://u:" + s + "@db/app\n"},
		"url under plain key":   {"compose.yaml", "x:\n  command: [\"connect\", \"postgres://u:" + s + "@db/app\"]\n"},
		"secret value verbatim": {"notes.txt", "remember " + s + " later\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, n := MaskSecrets(tc.text, tc.path, []string{s})
			assert.NotContains(t, got, s, "leaked:\n%s", got)
			assert.Contains(t, got, secretMask)
			assert.Positive(t, n)
			assert.Equal(t, strings.Count(tc.text, "\n"), strings.Count(got, "\n"), "line count kept:\n%s", got)
		})
	}
}

// The value pass covers "secret value verbatim"; without it that row leaks.
// This keeps the secrets argument from being dropped unnoticed.
func TestMaskSecretLines_ValuePassIsWhatCoversFreeText(t *testing.T) {
	got, n := MaskSecrets("remember hunter2W6WX later\n", "notes.txt", nil)
	assert.Contains(t, got, "hunter2W6WX")
	assert.Zero(t, n)
}

func TestMaskSecretLines_LeavesNonSecretsByteIdentical(t *testing.T) {
	texts := map[string]string{
		"compose.yaml": "services:\n  db:\n    image: postgres:16\n    environment:\n      TZ: Europe/Amsterdam\n      LOG_LEVEL: info\n    ports: [\"5432:5432\"]\n  web: {a: {}}\n",
		"app.env":      "TZ=UTC\nMONKEY=banana\nKEYCLOAK_URL=https://kc.example\n",
		"run.sh":       "exec app --port=80 --verbose\n",
	}
	for p, text := range texts {
		got, n := MaskSecrets(text, p, nil)
		assert.Equal(t, text, got, p)
		assert.Zero(t, n, p)
	}
}

func TestMaskSecretLines_ReferencesStayVisible(t *testing.T) {
	text := "x:\n  A_PASSWORD: ${A_PASSWORD}\n  B_PASSWORD: $B_PASSWORD\n  C_PASSWORD: ${C_PASSWORD:?set it}\n  D_PASSWORD: \"${D_PASSWORD?needed}\"\n  list:\n    - E_PASSWORD=${E_PASSWORD}\n"
	got, n := MaskSecrets(text, "compose.yaml", nil)
	assert.Equal(t, text, got)
	assert.Zero(t, n)
}

func TestMaskSecretLines_KeepsTheRestOfTheLine(t *testing.T) {
	got, n := MaskSecrets("x:\n  API_TOKEN: \"abcdefgh\" # rotate monthly\n", "compose.yaml", nil)
	assert.Equal(t, "x:\n  API_TOKEN: \"***\" # rotate monthly\n", got)
	assert.Equal(t, 1, n)
}
