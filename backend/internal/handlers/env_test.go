package handlers

import (
	"strings"
	"testing"
)

func TestParseEnvFile(t *testing.T) {
	handler := &EnvHandler{}

	tests := []struct {
		name     string
		content  string
		expected []EnvEntry
	}{
		{
			name:    "Simple key-value pairs",
			content: "DB_HOST=localhost\nDB_PORT=5432\n",
			expected: []EnvEntry{
				{Key: "DB_HOST", Value: "localhost", Line: 1, Sensitive: false, Comment: false},
				{Key: "DB_PORT", Value: "5432", Line: 2, Sensitive: false, Comment: false},
			},
		},
		{
			name:    "Comments and blank lines",
			content: "# Database settings\n\nDB_HOST=localhost\n",
			expected: []EnvEntry{
				{Key: "", Value: "# Database settings", Line: 1, Sensitive: false, Comment: true},
				{Key: "", Value: "", Line: 2, Sensitive: false, Comment: false},
				{Key: "DB_HOST", Value: "localhost", Line: 3, Sensitive: false, Comment: false},
			},
		},
		{
			name: "Quoted values",
			content: `DB_HOST="localhost"
DB_PORT='5432'
`,
			expected: []EnvEntry{
				{Key: "DB_HOST", Value: "localhost", Line: 1, Sensitive: false, Comment: false},
				{Key: "DB_PORT", Value: "5432", Line: 2, Sensitive: false, Comment: false},
			},
		},
		{
			name:    "Export prefix",
			content: "export DB_HOST=localhost\nexport API_KEY=secret\n",
			expected: []EnvEntry{
				{Key: "export DB_HOST", Value: "localhost", Line: 1, Sensitive: false, Comment: false},
				{Key: "export API_KEY", Value: "secret", Line: 2, Sensitive: true, Comment: false},
			},
		},
		{
			name:    "Empty values",
			content: "DB_HOST=\nDB_PASSWORD=\n",
			expected: []EnvEntry{
				{Key: "DB_HOST", Value: "", Line: 1, Sensitive: false, Comment: false},
				{Key: "DB_PASSWORD", Value: "", Line: 2, Sensitive: true, Comment: false},
			},
		},
		{
			name:    "Inline comments",
			content: "DB_HOST=localhost # database host\nDB_PORT=5432\n",
			expected: []EnvEntry{
				{Key: "DB_HOST", Value: "localhost", Line: 1, Sensitive: false, Comment: false},
				{Key: "DB_PORT", Value: "5432", Line: 2, Sensitive: false, Comment: false},
			},
		},
		{
			name:    "Sensitive keys",
			content: "API_KEY=secret\nDB_PASSWORD=pass\nSECRET_TOKEN=token\nMY_API_KEY=key\n",
			expected: []EnvEntry{
				{Key: "API_KEY", Value: "secret", Line: 1, Sensitive: true, Comment: false},
				{Key: "DB_PASSWORD", Value: "pass", Line: 2, Sensitive: true, Comment: false},
				{Key: "SECRET_TOKEN", Value: "token", Line: 3, Sensitive: true, Comment: false},
				{Key: "MY_API_KEY", Value: "key", Line: 4, Sensitive: true, Comment: false},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.parseEnvFile(tt.content)

			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d entries, got %d", len(tt.expected), len(result))
				return
			}

			for i, entry := range result {
				if entry.Key != tt.expected[i].Key {
					t.Errorf("Entry %d: expected key %q, got %q", i, tt.expected[i].Key, entry.Key)
				}
				if entry.Value != tt.expected[i].Value {
					t.Errorf("Entry %d: expected value %q, got %q", i, tt.expected[i].Value, entry.Value)
				}
				if entry.Line != tt.expected[i].Line {
					t.Errorf("Entry %d: expected line %d, got %d", i, tt.expected[i].Line, entry.Line)
				}
				if entry.Sensitive != tt.expected[i].Sensitive {
					t.Errorf("Entry %d: expected sensitive %v, got %v", i, tt.expected[i].Sensitive, entry.Sensitive)
				}
				if entry.Comment != tt.expected[i].Comment {
					t.Errorf("Entry %d: expected comment %v, got %v", i, tt.expected[i].Comment, entry.Comment)
				}
			}
		})
	}
}

func TestSerializeEnvFile(t *testing.T) {
	tests := []struct {
		name     string
		entries  []EnvEntry
		expected string
	}{
		{
			name: "Simple key-value pairs",
			entries: []EnvEntry{
				{Key: "DB_HOST", Value: "localhost", Line: 1, Comment: false},
				{Key: "DB_PORT", Value: "5432", Line: 2, Comment: false},
			},
			expected: "DB_HOST=localhost\nDB_PORT=5432\n",
		},
		{
			name: "Comments and blank lines",
			entries: []EnvEntry{
				{Key: "", Value: "# Database settings", Line: 1, Comment: true},
				{Key: "", Value: "", Line: 2, Comment: false},
				{Key: "DB_HOST", Value: "localhost", Line: 3, Comment: false},
			},
			expected: "# Database settings\n\nDB_HOST=localhost\n",
		},
		{
			name: "Only comments",
			entries: []EnvEntry{
				{Key: "", Value: "# Comment 1", Line: 1, Comment: true},
				{Key: "", Value: "# Comment 2", Line: 2, Comment: true},
			},
			expected: "# Comment 1\n# Comment 2\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := serializeEnvFile(tt.entries)

			if result != tt.expected {
				t.Errorf("Expected:\n%s\n\nGot:\n%s", tt.expected, result)
			}
		})
	}
}

func TestIsSensitiveKey(t *testing.T) {
	handler := &EnvHandler{}

	tests := []struct {
		key      string
		expected bool
	}{
		{"API_KEY", true},
		{"DB_PASSWORD", true},
		{"SECRET_TOKEN", true},
		{"AUTH_SECRET", true},
		{"MY_API_KEY", true},
		{"TEST_API_ENDPOINT", true},
		{"api_key", true},
		{"db_password", true},
		{"DB_HOST", false},
		{"API_ENDPOINT", false},
		{"PORT", false},
		{"DEBUG", false},
		{"HOSTNAME", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			result := handler.isSensitiveKey(tt.key)

			if result != tt.expected {
				t.Errorf("isSensitiveKey(%q): expected %v, got %v", tt.key, tt.expected, result)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	handler := &EnvHandler{}

	content := `# Database settings
DB_HOST=localhost
DB_PORT=5432
# Secrets
API_KEY=secret123
DB_PASSWORD=

export NODE_ENV=production
`

	entries := handler.parseEnvFile(content)
	result := serializeEnvFile(entries)

	expectedLines := strings.Split(content, "\n")
	resultLines := strings.Split(result, "\n")

	if len(resultLines) != len(expectedLines) {
		t.Errorf("Expected %d lines, got %d", len(expectedLines), len(resultLines))
		t.Errorf("Expected:\n%s\n\nGot:\n%s", content, result)
		return
	}

	for i, line := range resultLines {
		if line != expectedLines[i] {
			t.Errorf("Line %d: expected %q, got %q", i, expectedLines[i], line)
		}
	}
}

func TestUnquoteValue(t *testing.T) {
	handler := &EnvHandler{}

	tests := []struct {
		input    string
		expected string
	}{
		{`"value"`, "value"},
		{`'value'`, "value"},
		{`value`, "value"},
		{`"quoted value"`, "quoted value"},
		{`'quoted value'`, "quoted value"},
		{`"`, `"`},
		{``, ``},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := handler.unquoteValue(tt.input)

			if result != tt.expected {
				t.Errorf("unquoteValue(%q): expected %q, got %q", tt.input, tt.expected, result)
			}
		})
	}
}
