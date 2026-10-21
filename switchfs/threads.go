package switchfs

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
