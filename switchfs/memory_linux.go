//go:build linux

package switchfs

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// availableMemory is the memory the app can still take: the least of what the system has
// available and what its container (cgroup v2 or v1) allows beyond what it uses.
func availableMemory() (uint64, bool) {
	var free uint64
	found := false
	take := func(value uint64) {
		if !found || value < free {
			free, found = value, true
		}
	}
	if file, err := os.Open("/proc/meminfo"); err == nil {
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 2 && fields[0] == "MemAvailable:" {
				if kb, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
					take(kb << 10)
				}
			}
		}
		file.Close()
	}
	read := func(path string) (uint64, bool) {
		data, err := os.ReadFile(path)
		if err != nil {
			return 0, false
		}
		value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		return value, err == nil
	}
	// cgroup v2, then v1; "max" or a huge number is no limit
	for _, pair := range [][2]string{{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory.current"}, {"/sys/fs/cgroup/memory/memory.limit_in_bytes", "/sys/fs/cgroup/memory/memory.usage_in_bytes"}} {
		limit, ok := read(pair[0])
		if !ok || limit >= 1<<60 {
			continue
		}
		used, _ := read(pair[1])
		if used < limit {
			take(limit - used)
		} else {
			take(0)
		}
		break
	}
	return free, found
}
