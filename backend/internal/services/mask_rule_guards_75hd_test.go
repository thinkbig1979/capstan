package services

import (
	"go/ast"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// identUsesByFunc75hd is, for every identifier named name in the package's
// non-test files, the function it sits in ("" for a top-level declaration).
func identUsesByFunc75hd(t *testing.T, name string) []string {
	t.Helper()
	var out []string
	for file, f := range parseServicesSource0mwy(t) {
		for _, d := range f.Decls {
			fn := ""
			if fd, ok := d.(*ast.FuncDecl); ok {
				fn = fd.Name.Name
			}
			ast.Inspect(d, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && id.Name == name {
					out = append(out, file+": "+fn)
				}
				return true
			})
		}
	}
	sort.Strings(out)
	return out
}

// agent-os-75hd item 1: urlTokenRe is read only by the two helpers that route
// every token through redactURLToken (agent-os-caeh). A raw use elsewhere
// misses a URL nested in a token.
func TestURLTokenReOnlyInItsHelpers_75hd(t *testing.T) {
	uses := identUsesByFunc75hd(t, "urlTokenRe")
	allowed := map[string]bool{
		"redact_compose.go: ":                        true, // its declaration
		"redact_compose.go: RedactURLUserinfoInText": true,
		"redact_compose.go: HasURLUserinfo":          true,
	}
	var bad []string
	for _, u := range uses {
		if !allowed[u] {
			bad = append(bad, u)
		}
	}
	require.Contains(t, uses, "redact_compose.go: RedactURLUserinfoInText", "the instrument must see the one known use")
	require.Empty(t, bad, "urlTokenRe outside RedactURLUserinfoInText/HasURLUserinfo: call one of them")
}

// agent-os-75hd item 2: the functions that build the stack's secret list
// judge a value by IsSensitiveEnvVar (key or value), never by the key alone
// (agent-os-vq3i): a URL credential under any name is a secret.
func TestSecretListNeverKeyRuleAlone_75hd(t *testing.T) {
	listFuncs := map[string]bool{
		"StackSecretValues": false, "sensitiveEnvValues": false,
		"composeYAMLSecretValues": false, "containerSecretValues": false,
	}
	var bad []string
	for file, f := range parseServicesSource0mwy(t) {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			if _, ok := listFuncs[fd.Name.Name]; !ok {
				continue
			}
			listFuncs[fd.Name.Name] = true
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "IsSensitiveEnvKey" {
						bad = append(bad, file+": "+fd.Name.Name)
					}
				}
				return true
			})
		}
	}
	for fn, seen := range listFuncs {
		require.True(t, seen, "secret-list function %s not found: renamed? update this guard", fn)
	}
	require.Empty(t, bad, "a secret-list function tests IsSensitiveEnvKey alone: use IsSensitiveEnvVar")
}
