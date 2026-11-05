package switchfs

import (
	"context"
	"runtime"
)

// threadsKey holds the threads of WithThreads in a context.
type threadsKey struct{}

// WithThreads gives ctx the threads each file may take for its LZ4 blocks while it is read or
// written under it, shared already among the files worked on at once (lz4RebuildWorkers):
// decompressing, converting, verifying and hashing make the blocks of an NCZSECT3 again on that
// many at once, and a compression with CompressOptions.Rebuild checks them on as many, when
// fewer than its Workers. Without them (or with 0), a file takes every processor but one when it
// is read, and the threads of its encoder when it is compressed.
func WithThreads(ctx context.Context, threads int) context.Context {
	return context.WithValue(ctx, threadsKey{}, threads)
}

// threadsOf are the threads of WithThreads in ctx, 0 when not set.
func threadsOf(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	threads, _ := ctx.Value(threadsKey{}).(int)
	return threads
}

// the memory one thread of the zstd library in C takes, measured on 4 GiB of a game with 8
// threads (577 MiB at 11, 1394 MiB at 19), with a margin
var nativeThreadMemory = map[string]uint64{LevelBalanced: 80 << 20, LevelMax: 190 << 20}

// LimitThreads lowers the threads of each file of a compression so that the files worked on at
// once fit in half the memory the system has free: on a NAS with little memory the max level
// with 8 threads took 1.4 GB. The encoder in Go, one thread per stream, is left as it is.
func LimitThreads(level string, threads int, files int) int {
	if level == "" {
		level = LevelBalanced
	}
	perThread, native := nativeThreadMemory[level]
	// a numbered level takes the memory of the named level it is nearest to
	if n, ok := levelNumber(level); ok {
		perThread, native = nativeThreadMemory[LevelBalanced], true
		if n > 13 {
			perThread = nativeThreadMemory[LevelMax]
		}
	}
	if !NativeZstd || !native || threads <= 1 {
		return threads
	}
	free, ok := availableMemory()
	if !ok {
		return threads
	}
	if files < 1 {
		files = 1
	}
	fit := int(free / 2 / uint64(files) / perThread)
	switch {
	case fit < 1:
		return 1
	case fit < threads:
		return fit
	}
	return threads
}

// UsableProcessors are the processors a compression works on unless set: all but an eighth of
// them, at least one left free so the rest of the computer keeps going (4 of 3, 16 of 14, 32
// of 28). The zstd library in C compresses one file on all of them: on a processor of 24, 24
// threads compressed 38 % faster than 12. The memory sets fewer when it is short (LimitThreads).
func UsableProcessors() int {
	processors := runtime.NumCPU()
	if processors <= 1 {
		return 1
	}
	// the max of this package compares bytes
	if processors < 8 {
		return processors - 1
	}
	return processors - processors/8
}
