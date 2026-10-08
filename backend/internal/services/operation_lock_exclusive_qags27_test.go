package services

import (
	"strings"
	"testing"
	"time"
)

// agent-os-qags.27: the exclusive hold a container prune takes so it never
// overlaps a stack operation.

func TestOperationLock_ExclusiveRefusedWhileAStackIsHeld(t *testing.T) {
	lock := NewOperationLock()
	token, err := lock.Acquire("s1", OpKindBackup)
	if err != nil {
		t.Fatal(err)
	}

	_, err = lock.AcquireExclusive(OpKindContainerPrune)
	if err == nil || !strings.Contains(err.Error(), "backup in progress since") {
		t.Fatalf("exclusive taken over a held stack, or wrong message: %v", err)
	}

	lock.Release("s1", token)
	exTok, err := lock.AcquireExclusive(OpKindContainerPrune)
	if err != nil {
		t.Fatalf("idle lock refused the exclusive: %v", err)
	}
	lock.ReleaseExclusive(exTok)
}

func TestOperationLock_ExclusiveNamesTheOldestHolder(t *testing.T) {
	lock := NewOperationLock()
	if _, err := lock.Acquire("old", OpKindRestore); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := lock.Acquire("new", OpKindStart); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 20; i++ {
		_, err := lock.AcquireExclusive(OpKindContainerPrune)
		if err == nil || !strings.HasPrefix(err.Error(), "restore in progress since") {
			t.Fatalf("attempt %d named the wrong holder: %v", i, err)
		}
	}
}

func TestOperationLock_StackAcquireRefusedWhileExclusive(t *testing.T) {
	lock := NewOperationLock()
	exTok, err := lock.AcquireExclusive(OpKindContainerPrune)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := lock.Acquire("s1", OpKindStart); err == nil || !strings.Contains(err.Error(), "container prune in progress since") {
		t.Fatalf("stack acquired during the exclusive, or wrong message: %v", err)
	}
	if _, err := lock.AcquireExclusive(OpKindContainerPrune); err == nil {
		t.Fatal("a second exclusive was granted")
	}

	lock.ReleaseExclusive(exTok)
	token, err := lock.Acquire("s1", OpKindStart)
	if err != nil {
		t.Fatalf("stack refused after the exclusive was released: %v", err)
	}
	lock.Release("s1", token)
}

func TestOperationLock_StaleExclusiveReleaseKeepsTheHolder(t *testing.T) {
	lock := NewOperationLock()
	first, err := lock.AcquireExclusive(OpKindContainerPrune)
	if err != nil {
		t.Fatal(err)
	}
	lock.ReleaseExclusive(first)
	second, err := lock.AcquireExclusive(OpKindContainerPrune)
	if err != nil {
		t.Fatal(err)
	}

	lock.ReleaseExclusive(first) // late duplicate release of the first hold

	if _, err := lock.Acquire("s1", OpKindStart); err == nil {
		t.Fatal("a stale release freed the second exclusive hold")
	}
	lock.ReleaseExclusive(second)
}
