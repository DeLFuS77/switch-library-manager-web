package switchfs

import (
	"os"
	"strconv"
	"testing"
)

// The memory free in a container with a limit leaves out the files it read that stay in its
// cache, inactive: after a game was read they took the whole limit, and a compression was given
// one thread.
func TestMemoryOfAContainerLeavesOutItsCache(t *testing.T) {
	const gib = uint64(1) << 30
	number := func(value uint64) string { return strconv.FormatUint(value, 10) + "\n" }
	meminfo := "MemTotal:       32000000 kB\nMemAvailable:   16777216 kB\nCached:          9000000 kB\n"
	tests := []struct {
		name  string
		files map[string]string
		free  uint64
	}{
		{"cgroup v2 after reading a game", map[string]string{
			"/proc/meminfo":                 meminfo,
			"/sys/fs/cgroup/memory.max":     number(4 * gib),
			"/sys/fs/cgroup/memory.current": number(4*gib - 1<<20),
			"/sys/fs/cgroup/memory.stat":    "anon 524288000\nfile 3700000000\nactive_file 200000000\ninactive_file " + strconv.FormatUint(3*gib, 10) + "\n",
		}, 3*gib + 1<<20},
		{"cgroup v1 after reading a game", map[string]string{
			"/proc/meminfo": meminfo,
			"/sys/fs/cgroup/memory/memory.limit_in_bytes": number(4 * gib),
			"/sys/fs/cgroup/memory/memory.usage_in_bytes": number(4 * gib),
			"/sys/fs/cgroup/memory/memory.stat":           "cache 3700000000\ninactive_file 1\ntotal_inactive_file " + strconv.FormatUint(2*gib, 10) + "\n",
		}, 2 * gib},
		{"a cache bigger than the use", map[string]string{
			"/sys/fs/cgroup/memory.max":     number(4 * gib),
			"/sys/fs/cgroup/memory.current": number(gib),
			"/sys/fs/cgroup/memory.stat":    "inactive_file " + strconv.FormatUint(2*gib, 10) + "\n",
		}, 4 * gib},
		{"no memory.stat", map[string]string{
			"/sys/fs/cgroup/memory.max":     number(4 * gib),
			"/sys/fs/cgroup/memory.current": number(3 * gib),
		}, gib},
		{"a use past the limit", map[string]string{
			"/sys/fs/cgroup/memory.max":     number(4 * gib),
			"/sys/fs/cgroup/memory.current": number(5 * gib),
			"/sys/fs/cgroup/memory.stat":    "inactive_file 0\n",
		}, 0},
		{"no limit", map[string]string{
			"/proc/meminfo":                 meminfo,
			"/sys/fs/cgroup/memory.max":     "max\n",
			"/sys/fs/cgroup/memory.current": number(4 * gib),
		}, 16 * gib},
		{"the system has less than the container", map[string]string{
			"/proc/meminfo":                 "MemAvailable:   1048576 kB\n",
			"/sys/fs/cgroup/memory.max":     number(4 * gib),
			"/sys/fs/cgroup/memory.current": number(gib),
		}, gib},
	}
	for _, test := range tests {
		readFile := func(path string) ([]byte, error) {
			if content, ok := test.files[path]; ok {
				return []byte(content), nil
			}
			return nil, os.ErrNotExist
		}
		if free, ok := linuxAvailableMemory(readFile); !ok || free != test.free {
			t.Errorf("%s: %d MiB (%v), want %d MiB", test.name, free>>20, ok, test.free>>20)
		}
	}
	if _, ok := linuxAvailableMemory(func(string) ([]byte, error) { return nil, os.ErrNotExist }); ok {
		t.Error("no file of the system tells nothing")
	}
}
