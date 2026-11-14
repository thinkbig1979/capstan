package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// agent-os-6w3x: old container policies keyed on a container ID are re-keyed
// to the container's name only by the running scheduler (Start's one-off, scan
// ticks, passes). With the update scan off it never starts, so those rows were
// never re-keyed. Boot now starts the re-key on its own whenever the scheduler
// does not start.

func TestStartUpdateScheduler_RekeysWhenNotStarted_6w3x(t *testing.T) {
	cases := []struct {
		name        string
		db          *fakeSettings
		wantStarted bool
		wantRekeys  int
	}{
		{"interval 0", &fakeSettings{values: map[string]string{"update_scan_interval": "0"}}, false, 1},
		{"interval not set", &fakeSettings{}, false, 1},
		{"interval unreadable", &fakeSettings{errs: map[string]error{"update_scan_interval": errSettingsFault}}, false, 1},
		{"interval not a number", &fakeSettings{values: map[string]string{"update_scan_interval": "x"}}, false, 1},
		{"apply mode unreadable", &fakeSettings{errs: map[string]error{"update_apply_mode": errSettingsFault}}, false, 1},
		// Start runs its own re-key, so boot must not start a second one.
		{"interval 5", &fakeSettings{values: map[string]string{"update_scan_interval": "5"}}, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sched := &recordingScheduler{}
			log, _ := newCaptureLogger()

			started := startUpdateScheduler(sched, tc.db, log)

			if started != tc.wantStarted {
				t.Fatalf("started = %v, want %v", started, tc.wantStarted)
			}
			if sched.rekeys != tc.wantRekeys {
				t.Fatalf("re-key started %d time(s), want %d", sched.rekeys, tc.wantRekeys)
			}
		})
	}
}

// TestMain_StartsUpdateSchedulerWithTheService_6w3x pins that main.go hands
// the scheduler service to startUpdateScheduler, which is where the boot
// re-key is started; the test above covers what that call does.
func TestMain_StartsUpdateSchedulerWithTheService_6w3x(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", mainSource, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if fn, ok := call.Fun.(*ast.Ident); !ok || fn.Name != "startUpdateScheduler" || len(call.Args) == 0 {
			return true
		}
		if arg, ok := call.Args[0].(*ast.Ident); ok && arg.Name == "schedulerService" {
			calls++
		}
		return true
	})
	if calls != 1 {
		t.Fatalf("main.go calls startUpdateScheduler(schedulerService, ...) %d time(s), want 1", calls)
	}
}
