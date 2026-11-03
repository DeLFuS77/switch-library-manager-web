package awake

import "testing"

// blockSleepCounted stands for BlockSleep in a test, and tells how many blocks are held.
func blockSleepCounted(t *testing.T) *int {
	held := 0
	blocked := BlockSleep
	BlockSleep = func() func() {
		held++
		return func() { held-- }
	}
	t.Cleanup(func() { BlockSleep = blocked })
	return &held
}

// The computer is kept awake once while tasks work, lets it sleep while they are all
// paused, and once none is left.
func TestHoldsKeepAwakeUntilReleased(t *testing.T) {
	held := blockSleepCounted(t)
	first := Keep()
	second := Keep()
	if *held != 1 {
		t.Fatalf("two tasks at work hold %d blocks, want 1", *held)
	}
	first.Pause(true)
	if *held != 1 {
		t.Fatalf("one task paused, the other at work: %d blocks, want 1", *held)
	}
	second.Pause(true)
	if *held != 0 {
		t.Fatalf("both tasks paused: %d blocks, want 0", *held)
	}
	second.Pause(false)
	if *held != 1 {
		t.Fatalf("a task going on again: %d blocks, want 1", *held)
	}
	second.Release()
	second.Release()
	if *held != 0 {
		t.Fatalf("only a paused task left: %d blocks, want 0", *held)
	}
	// a paused task that ends leaves nothing behind
	first.Release()
	first.Pause(false)
	if *held != 0 || state.active != 0 {
		t.Fatalf("no task left: %d blocks, %d active, want none", *held, state.active)
	}
	var none *Hold
	none.Pause(true)
	none.Release()
}

// Asking the system to keep the computer awake and letting it go works where the app runs,
// or does nothing where it cannot.
func TestPreventSleep(t *testing.T) {
	release := preventSleep()
	release()
}
