package switchfs

import "testing"

// The threads of a compression fit in the memory free: never more than asked, never none, one
// when many files at once leave little memory each; the encoder in Go is left as set.
func TestLimitThreadsFitsTheMemory(t *testing.T) {
	for _, level := range []string{"", LevelBalanced, LevelMax} {
		for _, threads := range []int{1, 2, 8, 64} {
			got := LimitThreads(level, threads, 1)
			if got < 1 || got > threads {
				t.Errorf("%q %d threads: %d", level, threads, got)
			}
		}
	}
	if LimitThreads(LevelFast, 8, 1000000) != 8 {
		t.Error("the fast level, in Go, is left as set")
	}
	if free, ok := availableMemory(); ok && NativeZstd {
		if LimitThreads(LevelMax, 8, 1<<30) != 1 {
			t.Error("a billion files at once leave one thread each")
		}
		t.Logf("memory free: %d MiB, max level with 8 threads: %d threads", free>>20, LimitThreads(LevelMax, 8, 1))
	}
}
