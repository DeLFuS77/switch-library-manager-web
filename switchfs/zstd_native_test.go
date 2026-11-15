//go:build cgo && !slm_purego

package switchfs

import "testing"

// the pieces a measure waits for are the ones the zstd library in C takes with the long
// window: 32 MiB above level 11, set by newZstdEncoder, and by the level below it
func TestNativeJobSizeLong(t *testing.T) {
	for level, want := range map[string]int64{LevelMax: 32 << 20, "18": 32 << 20, "12": 32 << 20, LevelBalanced: 16 << 20, "9": 8 << 20, "7": 4 << 20, "3": 2 << 20} {
		if got := nativeJobSize(level, true); got != want {
			t.Errorf("level %s: %d MiB, want %d MiB", level, got>>20, want>>20)
		}
	}
	// a measure on many threads compresses at most measureRoundMax a round
	if round := min(uint64(nativeJobSize(LevelMax, true))*28, measureRoundMax); round != 896<<20 {
		t.Errorf("a round on 28 threads is %d MiB", round>>20)
	}
}
