package switchfs

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
)

// linuxAvailableMemory is the memory the app can still take on Linux, from the files of the
// system as readFile reads them: the least of what the system has available and what its
// container (cgroup v2 or v1) allows beyond what it uses. The container counts the files it
// read in its use, cached in its memory; the inactive ones are given back as soon as memory is
// needed, so they are not counted, as docker stats leaves them out: after a game was read, its
// cache took the whole limit and a compression got one thread.
func linuxAvailableMemory(readFile func(string) ([]byte, error)) (uint64, bool) {
	var free uint64
	found := false
	take := func(value uint64) {
		if !found || value < free {
			free, found = value, true
		}
	}
	if kb, ok := memoryField(readFile, "/proc/meminfo", "MemAvailable:"); ok {
		take(kb << 10)
	}
	read := func(path string) (uint64, bool) {
		data, err := readFile(path)
		if err != nil {
			return 0, false
		}
		value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		return value, err == nil
	}
	// cgroup v2, then v1: the limit, the use, and the inactive cache of files in the use; "max"
	// or a huge number is no limit
	for _, files := range [][4]string{
		{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory.current", "/sys/fs/cgroup/memory.stat", "inactive_file"},
		{"/sys/fs/cgroup/memory/memory.limit_in_bytes", "/sys/fs/cgroup/memory/memory.usage_in_bytes", "/sys/fs/cgroup/memory/memory.stat", "total_inactive_file"},
	} {
		limit, ok := read(files[0])
		if !ok || limit >= 1<<60 {
			continue
		}
		used, _ := read(files[1])
		if inactive, ok := memoryField(readFile, files[2], files[3]); ok {
			if inactive < used {
				used -= inactive
			} else {
				used = 0
			}
		}
		if used < limit {
			take(limit - used)
		} else {
			take(0)
		}
		break
	}
	return free, found
}

// memoryField is the number after a name in a file of the system made of lines "name value",
// as /proc/meminfo and memory.stat are.
func memoryField(readFile func(string) ([]byte, error), path string, name string) (uint64, bool) {
	data, err := readFile(path)
	if err != nil {
		return 0, false
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == name {
			value, err := strconv.ParseUint(fields[1], 10, 64)
			return value, err == nil
		}
	}
	return 0, false
}
