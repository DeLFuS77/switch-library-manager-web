package switchfs

import (
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
)

// nxNativeDlcData is an open .nx of a game with a DLC from an NSP: its tree holds the NCA files
// given, each one raw block of an NCA header with the title ID given, decrypted as the packer of
// the format keeps it (DNCA), or else encrypted with testHeaderKey.
func nxNativeDlcData(t *testing.T, application uint64, names []string, ids []uint64, decrypted bool) *NxData {
	t.Helper()
	d := &NxData{NxArchive: &NxArchive{Applications: []NxApplication{{Id: application, Snapshots: []NxSnapshot{
		{Type: NxSnapshotBase, root: 1},
		{Type: NxSnapshotDlc, root: 0},
	}}}}}
	d.dirs = []nxDirectory{{first: 0, count: uint32(len(names))}, {first: 0, count: 0}}
	content := []byte{}
	for i, name := range names {
		plain := make([]byte, 0xC00)
		copy(plain[0x200:], "NCA3")
		binary.LittleEndian.PutUint64(plain[0x210:], ids[i])
		data := encryptNcaHeader(plain, testHeaderKey)
		if decrypted {
			copy(plain[0x200:], "DNCA")
			data = plain
		}
		d.entries = append(d.entries, nxEntry{name: name, target: uint32(i), kind: 0})
		d.files = append(d.files, NxFile{Size: uint64(len(data)), firstRef: uint32(i), blocks: 1, chunkLog2: 16})
		d.refs = append(d.refs, uint32(i))
		d.blocks = append(d.blocks, nxBlock{offset: uint64(len(content)), stored: uint32(len(data)),
			logical: uint32(len(data)), firstCheck: uint32(len(d.checks))})
		d.checks = append(d.checks, crc32.Checksum(data, nxCastagnoli))
		content = append(content, data...)
	}
	path := filepath.Join(t.TempDir(), "game.nx")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d.file = file
	t.Cleanup(func() { file.Close() })
	return d
}

func TestReadNativeDlcIds(t *testing.T) {
	const game = 0x0100000000010000
	// the CNMT tells the ID first; an NCA of another title is not taken
	d := nxNativeDlcData(t, game, []string{"aa.nca", "bb.cnmt.nca"}, []uint64{0x0100000000099000, 0x0100000000011002}, false)
	if got := d.Metadata(); len(got) != 1 {
		t.Fatalf("before reading the NCA files: %d titles, want the game alone", len(got))
	}
	d.ReadNativeDlcIds(testHeaderKey)
	meta := d.Metadata()
	dlc, ok := meta["0100000000011002"]
	if !ok || dlc.Type != "DLC" {
		t.Fatalf("the DLC is not read from its CNMT: %v", meta)
	}

	// an NCA still encrypted stays without an ID without prod.keys
	d = nxNativeDlcData(t, game, []string{"bb.cnmt.nca"}, []uint64{0x0100000000011002}, false)
	d.ReadNativeDlcIds("")
	if _, ok := d.Metadata()["0100000000011002"]; ok {
		t.Fatal("an encrypted NCA read without prod.keys")
	}

	// a decrypted one, as the packer of the format keeps it, needs no keys
	d = nxNativeDlcData(t, game, []string{"bb.cnmt.nca"}, []uint64{0x0100000000011002}, true)
	d.ReadNativeDlcIds("")
	if _, ok := d.Metadata()["0100000000011002"]; !ok {
		t.Fatal("a decrypted NCA (DNCA) not read")
	}

	// an NCA of another game is never taken for its DLC
	d = nxNativeDlcData(t, game, []string{"bb.cnmt.nca"}, []uint64{0x0100000000021001}, true)
	d.ReadNativeDlcIds(testHeaderKey)
	if len(d.Metadata()) != 1 {
		t.Fatalf("a DLC of another game taken: %v", d.Metadata())
	}
}

func TestNxDlcOf(t *testing.T) {
	cases := []struct {
		id   uint64
		want bool
	}{
		{0x0100000000011001, true},
		{0x01000000000117D0, true},
		{0x0100000000011000, false}, // no DLC number
		{0x0100000000010800, false}, // the update
		{0x0100000000010000, false}, // the game
		{0x0100000000021001, false}, // another game
	}
	for _, c := range cases {
		if got := nxDlcOf(c.id, 0x0100000000010000); got != c.want {
			t.Errorf("nxDlcOf(%016x) = %v, want %v", c.id, got, c.want)
		}
	}
}

func TestNxNative(t *testing.T) {
	d := nxNativeDlcData(t, 0x0100000000010000, []string{"bb.cnmt.nca"}, []uint64{0x0100000000011002}, true)
	if !d.Native(0, 1) || d.Native(0, 0) {
		t.Fatal("a version of NCA files not told from an empty one")
	}
}
