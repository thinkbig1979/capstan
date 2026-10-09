package services

import (
	_ "embed"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"
)

// Embedded rather than read from disk so a go test -overlay mutant of
// wire_types.go is visible to this test (agent-os-qags.14).
//
//go:embed wire_types.go
var wireTypesSource string

// tygo (enum_style: union) puts a constant in its type's union only when the
// constant's name starts with the type's name, and silently leaves it out
// otherwise (tygo/write_toplevel.go, detectEnumGroup). A constant added with
// another prefix would compile, serve, and never reach the frontend's
// compile-time check, so this pins the naming rule in wire_types.go.
func TestWireTypesConstNamesCarryTheirTypeName(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "wire_types.go", wireTypesSource, 0)
	if err != nil {
		t.Fatalf("parse wire_types.go: %v", err)
	}

	counts := map[string]int{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			typ, ok := vs.Type.(*ast.Ident)
			if !ok {
				t.Errorf("const %s in wire_types.go has no explicit named type", vs.Names[0].Name)
				continue
			}
			counts[typ.Name]++
			if !strings.HasPrefix(vs.Names[0].Name, typ.Name) {
				t.Errorf("const %s is of type %s but its name does not start with %q, so tygo leaves it out of the union",
					vs.Names[0].Name, typ.Name, typ.Name)
			}
		}
	}

	for _, typ := range []string{"Status", "Stream", "JobTargetType"} {
		if counts[typ] < 2 {
			t.Errorf("%s has %d constants in wire_types.go, want at least 2 (tygo emits a union only for a group of 2+)", typ, counts[typ])
		}
	}
}

// The retyped fields are named strings, so the JSON the frontend parses must
// stay the literal strings it already matches (agent-os-th4h).
func TestJobWireValuesUnchanged(t *testing.T) {
	job := Job{
		ID:         "j1",
		TargetType: JobTargetTypeStack,
		Status:     StatusRecreating,
		Lines:      []LogLine{{Ts: time.Unix(0, 0).UTC(), Text: "x", Stream: StreamStderr}},
	}
	raw, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		TargetType string `json:"targetType"`
		Status     string `json:"status"`
		Lines      []struct {
			Stream string `json:"stream"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.TargetType != "stack" || got.Status != "recreating" || len(got.Lines) != 1 || got.Lines[0].Stream != "stderr" {
		t.Fatalf("wire values changed: %s", raw)
	}
}
