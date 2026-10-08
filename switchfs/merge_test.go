package switchfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergePackagesMakesOnePackage(t *testing.T) {
	folder := t.TempDir()
	base := filepath.Join(folder, "base.nsp")
	update := filepath.Join(folder, "update.nsp")
	os.WriteFile(base, makePFS0(pfs0Magic, []string{"a.nca", "common.cert"}, [][]byte{[]byte("base-content"), []byte("cert")}), 0644)
	os.WriteFile(update, makePFS0(pfs0Magic, []string{"b.nca", "common.cert"}, [][]byte{[]byte("update-content!"), []byte("cert")}), 0644)

	target := filepath.Join(folder, "pack.nsp")
	var last, total int64
	if _, err := MergePackages(context.Background(), []string{base, update}, target, true, func(done, all int64) { last, total = done, all }); err != nil {
		t.Fatal(err)
	}
	if last != total || total != int64(len("base-content")+len("cert")+len("update-content!")) {
		t.Fatalf("progress %d of %d", last, total)
	}

	reader, err := OpenFile(target)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	pkg, err := openGamePackage(reader)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a.nca": "base-content", "common.cert": "cert", "b.nca": "update-content!"}
	if len(pkg.files) != len(want) {
		t.Fatalf("each file once: %+v", pkg.files)
	}
	for _, file := range pkg.files {
		data := make([]byte, file.size)
		reader.ReadAt(data, file.offset)
		if !bytes.Equal(data, []byte(want[file.name])) {
			t.Fatalf("%s holds %q", file.name, data)
		}
	}
	if compressed, err := PackHasCompressedFiles([]string{base, update}); err != nil || compressed {
		t.Fatalf("no NCZ: %v %v", compressed, err)
	}
}

func TestMergePackagesLeavesNothingOnError(t *testing.T) {
	folder := t.TempDir()
	base := filepath.Join(folder, "base.nsp")
	os.WriteFile(base, makePFS0(pfs0Magic, []string{"a.nca"}, [][]byte{[]byte("x")}), 0644)
	broken := filepath.Join(folder, "broken.nsp")
	os.WriteFile(broken, []byte("not a package"), 0644)
	target := filepath.Join(folder, "pack.nsp")
	if _, err := MergePackages(context.Background(), []string{base, broken}, target, true, nil); err == nil {
		t.Fatal("a broken package")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("no pack left behind")
	}
}

func TestCnmtDeltaFragments(t *testing.T) {
	cnmt := make([]byte, 0x20+0x10+3*0x38)
	binary.LittleEndian.PutUint16(cnmt[0xE:0x10], 0x10)
	binary.LittleEndian.PutUint16(cnmt[0x10:0x12], 3)
	for i, kind := range []byte{1, 6, 6} {
		entry := cnmt[0x30+i*0x38:]
		entry[0x20] = byte(0xa0 + i)
		entry[0x36] = kind
	}
	data := makePFS0(pfs0Magic, []string{"Patch.cnmt"}, [][]byte{cnmt})
	pfs0, err := readPfs0(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatal(err)
	}
	ids := cnmtDeltaFragments(pfs0, data)
	if len(ids) != 2 || !strings.HasPrefix(ids[0], "a1") || !strings.HasPrefix(ids[1], "a2") {
		t.Fatalf("the two delta fragments: %v", ids)
	}
}

// A pack is read by the scan like the files it is made of: a game and its update in one
// file, with the icon of the game, and without the delta fragment of the update.
func TestPackIsReadLikeItsPackages(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	var encoded bytes.Buffer
	jpeg.Encode(&encoded, img, nil)
	icon := encoded.Bytes()
	base := writeNspWithIcon(t, icon)

	program := [16]byte{0x9a, 1}
	delta := [16]byte{0xde, 1}
	cnmt := make([]byte, 0x20+2*0x38)
	binary.LittleEndian.PutUint64(cnmt[0:8], 0x0100000000010800)
	binary.LittleEndian.PutUint32(cnmt[8:12], 65536)
	cnmt[0xC] = ContentMetaType_Patch
	binary.LittleEndian.PutUint16(cnmt[0x10:0x12], 2)
	copy(cnmt[0x20+0x20:], program[:])
	cnmt[0x20+0x36] = 1
	copy(cnmt[0x20+0x38+0x20:], delta[:])
	cnmt[0x20+0x38+0x36] = 6
	meta := makeSyntheticNCA(t, pad512(makePFS0(pfs0Magic, []string{"Patch.cnmt"}, [][]byte{cnmt})), 1)
	deltaName := hex.EncodeToString(delta[:]) + ".nca"
	update := filepath.Join(t.TempDir(), "Update.nsp")
	os.WriteFile(update, makePFS0(pfs0Magic, []string{"update.cnmt.nca", hex.EncodeToString(program[:]) + ".nca", deltaName},
		[][]byte{meta, []byte("program"), []byte("delta fragment")}), 0o644)

	pack := filepath.Join(t.TempDir(), "pack.nsp")
	skipped, err := MergePackages(context.Background(), []string{base, update}, pack, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != int64(len("delta fragment")) {
		t.Fatalf("the delta fragment is left out: %d", skipped)
	}
	reader, _ := OpenFile(pack)
	pkg, err := openGamePackage(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range pkg.files {
		if file.name == deltaName {
			t.Fatal("the delta fragment is in the pack")
		}
	}

	contents, err := ReadNspMetadata(pack)
	if err != nil {
		t.Fatal(err)
	}
	if contents["0100000000010000"] == nil || contents["0100000000010800"] == nil || contents["0100000000010800"].Version != 65536 {
		t.Fatalf("the game and its update: %v", contents)
	}
	if got, err := ExtractIcon(pack); err != nil || !bytes.Equal(got, icon) {
		t.Fatalf("the icon of the game: %v", err)
	}

	// kept when asked
	all := filepath.Join(t.TempDir(), "all.nsp")
	if skipped, err := MergePackages(context.Background(), []string{base, update}, all, false, nil); err != nil || skipped != 0 {
		t.Fatalf("with the deltas: %d %v", skipped, err)
	}
}
