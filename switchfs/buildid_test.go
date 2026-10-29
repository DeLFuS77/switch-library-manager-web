package switchfs

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// The build ID is the start of the module ID in the header of "main" in the ExeFS.
func TestBuildIdOfExefs(t *testing.T) {
	nso := make([]byte, 0x100)
	copy(nso, "NSO0")
	copy(nso[nsoBuildIdOffset:], []byte{0xE8, 0xD5, 0xA3, 0x86, 0x59, 0x7A, 0x9C, 0x4C, 0xFF})
	exefs := makePFS0(pfs0Magic, []string{"rtld", "main", "main.npdm"}, [][]byte{bytes.Repeat([]byte{1}, 0x40), nso, []byte("npdm")})
	build, err := buildIdOfExefs(exefs)
	if err != nil || build != "E8D5A386597A9C4C" {
		t.Fatalf("build %q, %v", build, err)
	}
	if _, err := buildIdOfExefs(makePFS0(pfs0Magic, []string{"rtld"}, [][]byte{{1}})); err == nil {
		t.Fatal("an ExeFS without main")
	}
	bad := makePFS0(pfs0Magic, []string{"main"}, [][]byte{make([]byte, 0x100)})
	if _, err := buildIdOfExefs(bad); err == nil {
		t.Fatal("main that is not an NSO")
	}
	// a damaged offset of main near its largest: added to the offset of the module ID it
	// wrapped around to a small one, and the read past the ExeFS stopped the server
	parsed, err := readPfs0(bytes.NewReader(exefs), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, start := range []uint64{^uint64(0) - 0x3F, 1 << 63, uint64(len(exefs)) - 0x20} {
		damaged := append([]byte(nil), exefs...)
		entry := damaged[0x10+PfsfileEntryTableSize:]
		binary.LittleEndian.PutUint64(entry[0:8], start-parsed.HeaderLen)
		binary.LittleEndian.PutUint64(entry[8:16], 0)
		if _, err := buildIdOfExefs(damaged); err == nil {
			t.Fatalf("main at %#x", start)
		}
	}
}
