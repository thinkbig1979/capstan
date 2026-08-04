package services

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestEnqueueSnapshotsBeforePublishing pins agent-os-9oe structurally, by
// parsing update_job_manager.go and checking that Enqueue takes its
// caller-facing snapshot before it publishes the job to the worker queue.
//
// Why structural rather than behavioural. Enqueue used to do:
//
//	m.queue <- queuedItem{...}   // publish
//	cp := js.deepCopyLocked()    // snapshot
//
// runJob sets js.job.StartedAt as its first action, so between those two
// statements the worker could dequeue and start the job, and the snapshot
// handed back to the caller already showed a start time on a job reported as
// queued. Both writes are guarded by js.mu, so this is an ordering bug, not a
// data race — `go test -race` sees nothing.
//
// TestUpdateJobManager_Enqueue_ReturnsQueuedJob asserts the right thing, but
// it cannot GUARD this: with the ordering wrong it fails only when the worker
// happens to win a nanosecond-wide race. Measured 2026-08-04 on the unfixed
// code: 400/400 passes across -cpu=1,2,8, and 3/3 passes of the full package
// under -race. It went red in CI roughly once a day. So relying on it to catch
// a regression means relying on a coin flip — a reverted fix would read as
// flakiness rather than as a broken guard, which is exactly how this defect
// survived in the first place.
//
// This test is deterministic instead: the ordering either holds in the source
// or it does not. It follows the precedent in
// handlers/stack_crud_concurrent_test.go (agent-os-zpg), which pins a
// lock-before-stat ordering the same way.
func TestEnqueueSnapshotsBeforePublishing(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "update_job_manager.go", nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("failed to parse update_job_manager.go: %v", err)
	}

	var enqueue *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "Enqueue" && fn.Recv != nil {
			enqueue = fn
			break
		}
	}
	if enqueue == nil {
		t.Fatal("no Enqueue method found in update_job_manager.go; this test can no longer guard the " +
			"snapshot-before-publish ordering and must be updated to follow it (agent-os-9oe)")
	}

	var snapshotPos, publishPos token.Pos

	ast.Inspect(enqueue, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "deepCopyLocked" && !snapshotPos.IsValid() {
				snapshotPos = node.Pos()
			}
		case *ast.SendStmt:
			// Any channel send in Enqueue is the publish to the worker.
			if !publishPos.IsValid() {
				publishPos = node.Pos()
			}
		}
		return true
	})

	// Both anchors must exist, or the test would pass vacuously after a
	// refactor that removed one — the failure mode this whole bead is about.
	if !snapshotPos.IsValid() {
		t.Fatal("found no deepCopyLocked call in Enqueue; cannot verify the snapshot-before-publish " +
			"ordering (agent-os-9oe)")
	}
	if !publishPos.IsValid() {
		t.Fatal("found no channel send in Enqueue; cannot verify the snapshot-before-publish " +
			"ordering (agent-os-9oe)")
	}

	if snapshotPos > publishPos {
		t.Errorf("Enqueue publishes to the worker queue at %s BEFORE snapshotting at %s. "+
			"runJob sets StartedAt as its first action, so the worker can start the job before the "+
			"snapshot is taken and Enqueue returns a 'queued' job that already reports a start time "+
			"(agent-os-9oe). Take the deep copy first.",
			fset.Position(publishPos), fset.Position(snapshotPos))
	}
}
