package switchfs

import (
	"context"
	"runtime"
	"testing"
)

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

// The LZ4 blocks of one file are made again on the threads of that file: at least one, never
// more than every processor but one, and every processor but one when none are set.
func TestLz4RebuildWorkersKeepToTheThreads(t *testing.T) {
	most := runtime.NumCPU() - 1
	if most < 1 {
		most = 1
	}
	for threads, want := range map[int]int{0: most, -3: most, 1: 1, most: most, most + 5: most} {
		if got := lz4RebuildWorkers(threads); got != want {
			t.Errorf("%d threads: %d workers, want %d", threads, got, want)
		}
	}
	if most > 2 && lz4RebuildWorkers(2) != 2 {
		t.Error("2 threads: not 2 workers")
	}
}

// The threads of a context are the ones WithThreads gave it, 0 when none.
func TestWithThreadsReachesTheWork(t *testing.T) {
	ctx := context.Background()
	if threadsOf(ctx) != 0 {
		t.Error("threads without WithThreads")
	}
	inner, cancel := context.WithCancel(WithThreads(ctx, 3))
	defer cancel()
	if threadsOf(inner) != 3 {
		t.Errorf("threads of a context made from one with 3: %d", threadsOf(inner))
	}
}
