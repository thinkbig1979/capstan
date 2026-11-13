package services

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// agent-os-w6wx review round 1 (sec D2, D4, D5).

// D2: a hunk body is read by its header's counts. A removed "-- comment" line
// prints as "--- comment" and an added "++ x" line as "+++ x"; reading the
// body by line prefix ended the hunk there and printed the rest of the file
// unmasked.
func TestMaskFullContextDiff_RemovedDashDashLine_w6wx(t *testing.T) {
	const secret = "Sup3rS3cretPw"
	// The reviewer's diff, from `git diff HEAD --unified=999999999` (git 2.47.3).
	diff := "diff --git a/init.sql b/init.sql\n" +
		"index 2abba2c..097c718 100644\n" +
		"--- a/init.sql\n" +
		"+++ b/init.sql\n" +
		"@@ -1,3 +1,2 @@\n" +
		"--- setup\n" +
		" CREATE ROLE app LOGIN PASSWORD '" + secret + "';\n" +
		"-ALTER ROLE app SET x=1;\n" +
		"+ALTER ROLE app SET x=2;\n"
	got := MaskFullContextDiff(diff, []string{secret})
	assert.NotContains(t, got, secret, "leaked:\n%s", got)
	assert.Contains(t, got, "--- setup", "the removed line is a body line, kept:\n%s", got)
	assert.Contains(t, got, "-ALTER ROLE app SET x=1;", "the real change is shown:\n%s", got)
	assert.Contains(t, got, "+ALTER ROLE app SET x=2;", "%s", got)
	assert.Contains(t, got, "--- a/init.sql", "%s", got)
}

func TestMaskFullContextDiff_AddedPlusPlusLine_w6wx(t *testing.T) {
	diff := "diff --git a/app.env.txt b/app.env.txt\n" +
		"--- a/app.env.txt\n" +
		"+++ b/app.env.txt\n" +
		"@@ -1,2 +1,3 @@\n" +
		" TZ=UTC\n" +
		"+++ added\n" +
		" DB_PASSWORD=hunter2XYZ\n"
	got := MaskFullContextDiff(diff, nil)
	assert.NotContains(t, got, "hunter2XYZ", "leaked:\n%s", got)
	assert.Contains(t, got, "+++ added", "the added line is a body line:\n%s", got)
	assert.Contains(t, got, " DB_PASSWORD=***", "%s", got)
}

// A body that does not add up fails closed for the whole diff: only the
// "diff --git" lines are left.
func TestMaskFullContextDiff_BodyNotAddingUpFailsClosed_w6wx(t *testing.T) {
	cases := map[string]string{
		"header claims fewer lines": "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,1 +1,1 @@\n-a\n+b\n leak-me-1\n",
		"header claims more lines":  "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,3 +1,3 @@\n-a\n+b\n leak-me-1\n",
		"stray line in a body":      "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n-a\n+b\nleak-me-1\n",
		"unparseable header":        "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -x +y @@\n-a\n+leak-me-1\n",
	}
	for name, diff := range cases {
		t.Run(name, func(t *testing.T) {
			got := MaskFullContextDiff(diff, nil)
			assert.NotContains(t, got, "leak-me", "%s", got)
			assert.Equal(t, "diff --git a/x b/x", got)
		})
	}
}

// An omitted count means 1, and such a hunk is masked normally.
func TestMaskFullContextDiff_OmittedCountIsOne_w6wx(t *testing.T) {
	diff := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-API_TOKEN=old-secret-1\n+API_TOKEN=new-secret-2\n"
	got := MaskFullContextDiff(diff, nil)
	assert.NotContains(t, got, "secret-", "%s", got)
	assert.Contains(t, got, "-API_TOKEN=***\n+API_TOKEN=***", "%s", got)
}

// Two files, the second after a hunk whose last line is "\ No newline".
func TestMaskFullContextDiff_TwoFilesAndNoNewline_w6wx(t *testing.T) {
	diff := "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-x=1\n\\ No newline at end of file\n+x=2\n\\ No newline at end of file\n" +
		"diff --git a/b b/b\n--- a/b\n+++ b/b\n@@ -1,2 +1,2 @@\n TOKEN=abcd1234\n-y=1\n+y=2\n"
	got := MaskFullContextDiff(diff, nil)
	assert.NotContains(t, got, "abcd1234", "%s", got)
	assert.Contains(t, got, "-x=1\n\\ No newline at end of file\n+x=2\n\\ No newline at end of file\ndiff --git a/b b/b", "%s", got)
	assert.Contains(t, got, "-y=1\n+y=2", "%s", got)
}

// D4: an alias under a sensitive key shows the anchor's value where the
// anchor is defined; that value is masked at the anchor.
func TestMaskSecrets_AliasUnderSensitiveKeyMasksAnchor_w6wx(t *testing.T) {
	cases := map[string]string{
		"scalar anchor":  "x-pw: &pw hunter2XYZ\nservices:\n  db:\n    environment:\n      DB_PASSWORD: *pw\n",
		"mapping anchor": "x-creds: &creds\n  user: app\n  pass: hunter2XYZ\nservices:\n  db:\n    environment:\n      DB_SECRET: *creds\n",
		"in a sequence":  "x-pw: &pw hunter2XYZ\nx:\n  TOKENS_SECRET: [*pw]\n",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			got, n := MaskSecrets(text, "compose.yaml", nil)
			assert.NotContains(t, got, "hunter2XYZ", "%s", got)
			assert.Positive(t, n)
			assert.Equal(t, strings.Count(text, "\n"), strings.Count(got, "\n"))
		})
	}
	// An alias inside its own anchor terminates (each anchor is masked once).
	got, _ := MaskSecrets("x: &a\n  k: hunter2XYZ\n  DB_SECRET: *a\n", "compose.yaml", nil)
	assert.NotContains(t, got, "hunter2XYZ", "%s", got)

	got, _ = MaskSecrets(cases["scalar anchor"], "compose.yaml", nil)
	assert.Contains(t, got, "x-pw: &pw ***", "masked at the anchor's own line:\n%s", got)
}

// D5: a long flag whose value is the next token.
func TestMaskSecrets_LongFlagMasksNextToken_w6wx(t *testing.T) {
	cases := map[string]struct{ path, text, want string }{
		"shell form": {"compose.yaml", "x:\n  command: mysqld --password hunter2XYZ --port 3306\n",
			"command: mysqld --password *** --port 3306"},
		"exec form": {"compose.yaml", "x:\n  command: [\"mysqld\", \"--password\", \"hunter2XYZ\"]\n",
			`"--password", "***"`},
		"block list": {"compose.yaml", "x:\n  entrypoint:\n    - app\n    - --token\n    - hunter2XYZ\n",
			"    - --token\n    - ***\n"},
		"exec form, not yaml": {"run.json", `{"cmd": ["mysqld", "--db-password", "hunter2XYZ"]}` + "\n",
			`"--db-password", "***"`},
		"free text": {"run.sh", "exec app --api-key hunter2XYZ\n", "exec app --api-key ***"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, n := MaskSecrets(tc.text, tc.path, nil)
			assert.NotContains(t, got, "hunter2XYZ", "%s", got)
			assert.Contains(t, got, tc.want)
			assert.Positive(t, n)
		})
	}
}

// Short flags stay unmasked (-p is usually a port), and so does a long flag
// whose name holds no sensitive word, and a reference.
func TestMaskSecrets_ShortAndPlainFlagsStay_w6wx(t *testing.T) {
	for _, text := range []string{
		"x:\n  command: app -p 8080 --port 9000\n",
		"x:\n  command: mysql -u root -phunter2XYZ\n",
		"x:\n  command: [\"app\", \"-p\", \"8080\", \"--name\", \"web\"]\n",
		"x:\n  command: app --password ${DB_PASSWORD}\n",
		"x:\n  command: [\"app\", \"--password\", \"${DB_PASSWORD}\"]\n",
	} {
		got, n := MaskSecrets(text, "compose.yaml", nil)
		assert.Equal(t, text, got)
		assert.Zero(t, n, "%s", text)
	}
}
