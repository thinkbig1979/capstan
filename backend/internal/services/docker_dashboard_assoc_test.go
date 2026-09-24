package services

import "testing"

// agent-os-yrgn. resolveDashboardStackAssociation is the dashboard's half of the
// discrimination agent-os-g482 established on the update path: an empty stack id
// has TWO causes, and answering a failed READ as if the project were simply not
// a stack is what sent compose-managed containers down the standalone apply path.
//
// The dashboard cannot refuse the way resolveUpdateStrategy does -- it is a poll,
// and failing the whole view because one table is briefly unreadable is worse
// than drawing it without stack associations -- so it defaults AND reports which
// case it hit. StackLookupFailed is that report, and ContainersOverviewTab.tsx
// now routes on it.
//
// THE THIRD ARM IS THE INSTRUMENT, and the first two are what give it meaning.
// Arms 2 and 3 both produce an empty StackID; they differ only in LookupFailed.
// An assertion on StackID alone cannot see the defect this exists to prevent,
// because the defect IS the two states sharing one value. TestArmsDisagree below
// pins that they disagree, so a change collapsing them fails on its own message
// rather than on a field comparison that still reads plausibly.
//
// Fixtures are reused verbatim from docker_update_dbfault_test.go (same package):
// g482HealthyDB holds exactly one stack under g482ProjectKnown, and g482ClosedDB
// seeds the same stack then closes the connection, so every read fails with a
// driver error rather than errdefs.ErrNotFound.

func TestResolveDashboardStackAssociation_KnownProjectResolvesToItsStack(t *testing.T) {
	assoc, err := resolveDashboardStackAssociation(g482HealthyDB(t), g482ProjectKnown)
	if err != nil {
		t.Fatalf("err: got %v, want nil", err)
	}
	if assoc.StackID != g482StackID {
		t.Errorf("StackID: got %q, want %q", assoc.StackID, g482StackID)
	}
	if assoc.LookupFailed {
		t.Errorf("LookupFailed: got true, want false — the read succeeded")
	}
}

func TestResolveDashboardStackAssociation_GenuinelyAbsentIsNotAFailure(t *testing.T) {
	assoc, err := resolveDashboardStackAssociation(g482HealthyDB(t), g482ProjectUnknown)
	if err != nil {
		t.Fatalf("err: got %v, want nil — an absent stack row is errdefs.ErrNotFound, an ordinary not-found", err)
	}
	if assoc.StackID != "" {
		t.Errorf("StackID: got %q, want empty", assoc.StackID)
	}
	if assoc.LookupFailed {
		t.Errorf("LookupFailed: got true, want false — nothing failed, the project simply is not a stack")
	}
}

func TestResolveDashboardStackAssociation_UnreadableTableIsFlagged(t *testing.T) {
	assoc, err := resolveDashboardStackAssociation(g482ClosedDB(t), g482ProjectKnown)
	if err == nil {
		t.Fatalf("err: got nil, want a driver error — the fixture's connection is closed, so this test is not measuring a failed read at all")
	}
	if assoc.StackID != "" {
		t.Errorf("StackID: got %q, want empty — a failed read must not invent an association", assoc.StackID)
	}
	if !assoc.LookupFailed {
		t.Errorf("LookupFailed: got false, want true — an unreadable stacks table would then be indistinguishable from a project that is genuinely not a stack, which is agent-os-g482's P2 defect reinstated on the dashboard path")
	}
}

// TestResolveDashboardStackAssociation_EmptyStackIDArmsDisagree is the whole
// point stated as one assertion: the two states that share an empty StackID must
// NOT share a LookupFailed. Written as a comparison rather than two literals so
// it cannot be satisfied by a constant.
func TestResolveDashboardStackAssociation_EmptyStackIDArmsDisagree(t *testing.T) {
	absent, _ := resolveDashboardStackAssociation(g482HealthyDB(t), g482ProjectUnknown)  //nolint:errcheck // agent-os-g482: this test compares the two ARMS' values; the error channel is deliberately not the subject here.
	unreadable, _ := resolveDashboardStackAssociation(g482ClosedDB(t), g482ProjectKnown) //nolint:errcheck // agent-os-g482: this test compares the two ARMS' values; the error channel is deliberately not the subject here.

	if absent.StackID != "" || unreadable.StackID != "" {
		t.Fatalf("premise broken: both arms must yield an empty StackID, got %q and %q", absent.StackID, unreadable.StackID)
	}
	if absent.LookupFailed == unreadable.LookupFailed {
		t.Errorf("both arms report LookupFailed=%v — an empty StackID is now ambiguous on the wire and the frontend cannot route on it safely", absent.LookupFailed)
	}
}

// agent-os-oafx. A caller that supplies NO database has not looked anything up,
// so it must not be told the project is genuinely not a stack. lookupStackByProject
// answers a nil db with (nil, nil) -- absence -- which is right for
// resolveUpdateStrategy (it guards nil db itself) but made GET
// /resources/containers, which passed nil, report every compose container as
// {StackID: "", LookupFailed: false}: the wire's "genuinely standalone" value.
//
// Two-sided on one instrument: the same nil db with NO compose project is still
// not a failure, because there is nothing to look up.
func TestResolveDashboardStackAssociation_NilDBIsNotReportedAsAbsence(t *testing.T) {
	assoc, err := resolveDashboardStackAssociation(nil, g482ProjectKnown)
	if err == nil {
		t.Errorf("err: got nil, want an error naming the missing database")
	}
	if assoc.StackID != "" {
		t.Errorf("StackID: got %q, want empty", assoc.StackID)
	}
	if !assoc.LookupFailed {
		t.Errorf("LookupFailed: got false, want true — with no database nothing was looked up, and false tells the frontend this compose project is genuinely not a stack")
	}

	noProject, err := resolveDashboardStackAssociation(nil, "")
	if err != nil {
		t.Errorf("no compose project: err got %v, want nil — there is nothing to look up", err)
	}
	if noProject.LookupFailed || noProject.StackID != "" {
		t.Errorf("no compose project: got %+v, want the zero association", noProject)
	}
}
