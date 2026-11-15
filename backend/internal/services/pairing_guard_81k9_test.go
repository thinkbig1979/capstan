package services

import (
	"go/ast"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"
)

// agent-os-81k9 guard: a value is judged secret by IsSensitiveEnvVar, which
// is the key rule widened by the value (URL, opaque and JSON credentials). A
// hand-written `IsSensitiveEnvKey(k) || HasURLUserinfo(v)` is a narrower copy
// of it that drifted once already: this flags any && / || expression holding
// both calls, outside IsSensitiveEnvVar itself.
func TestKeyOrURLPairingOnlyInIsSensitiveEnvVar_81k9(t *testing.T) {
	var sites []string
	for file, f := range parseServicesSource0mwy(t) {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil || fd.Name.Name == "IsSensitiveEnvVar" {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				b, ok := n.(*ast.BinaryExpr)
				if !ok || (b.Op != token.LOR && b.Op != token.LAND) {
					return true
				}
				if callsFunc81k9(b, "IsSensitiveEnvKey") && callsFunc81k9(b, "HasURLUserinfo") {
					sites = append(sites, file+": "+fd.Name.Name)
					return false
				}
				return true
			})
		}
	}
	require.Empty(t, sites, "IsSensitiveEnvKey paired with HasURLUserinfo: call IsSensitiveEnvVar")
}

func callsFunc81k9(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(m ast.Node) bool {
		if call, ok := m.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == name {
				found = true
			}
		}
		return !found
	})
	return found
}
