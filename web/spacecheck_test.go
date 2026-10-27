package web

import "testing"

// the space a task needs: every new file kept, or of the files whose originals are deleted only
// the ones written at once, with 64 MB kept free beside them
func TestWeighSpace(t *testing.T) {
	const mb = int64(1 << 20)
	writes := []spaceWrite{{"/a", 300 * mb, "/a"}, {"/a", 200 * mb, "/a"}, {"/b", 100 * mb, "/b"}}
	volume := func(folder string) string { return folder }
	free := func(left int64) func(string) (uint64, error) {
		return func(string) (uint64, error) { return uint64(left), nil }
	}
	for _, c := range []struct {
		name    string
		deleted bool
		atOnce  int
		left    int64
		short   int
		need    int64
	}{
		{"every file kept", false, 1, 563 * mb, 1, 500 * mb},
		{"fits with the room kept free", false, 1, 564 * mb, 0, 0},
		{"originals deleted, one at once", true, 1, 363 * mb, 1, 300 * mb},
		{"originals deleted, two at once", true, 2, 564 * mb, 0, 0},
	} {
		short := weighSpace(writes, c.deleted, c.atOnce, volume, free(c.left))
		// the volume /b always has room: the case is about /a
		if len(short) != c.short || c.short == 1 && (short[0].Folder != "/a" || short[0].Need != c.need) {
			t.Errorf("%s: %+v", c.name, short)
		}
	}
}
