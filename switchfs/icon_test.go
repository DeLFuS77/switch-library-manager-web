package switchfs

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeRomfsWithFiles builds a RomFS section with the given files.
func makeRomfsWithFiles(t *testing.T, names []string, contents [][]byte) []byte {
	t.Helper()
	const fileTableOffset = 0x50
	table := []byte{}
	data := []byte{}
	for i, name := range names {
		entry := make([]byte, 0x20)
		binary.LittleEndian.PutUint64(entry[0x8:], uint64(len(data)))
		binary.LittleEndian.PutUint64(entry[0x10:], uint64(len(contents[i])))
		binary.LittleEndian.PutUint32(entry[0x1c:], uint32(len(name)*2))
		for _, r := range name {
			entry = binary.LittleEndian.AppendUint16(entry, uint16(r))
		}
		for len(entry)%4 != 0 {
			entry = append(entry, 0)
		}
		table = append(table, entry...)
		data = append(data, contents[i]...)
		for len(data)%0x10 != 0 {
			data = append(data, 0)
		}
	}
	dataOffset := (fileTableOffset + len(table) + 0xf) &^ 0xf
	section := make([]byte, (dataOffset+len(data)+0x1ff)&^0x1ff)
	putRomfsHeader(section, RomfsHeader{HeaderSize: 0x50, FileMetaTableOffset: fileTableOffset, FileMetaTableSize: uint64(len(table)), DataOffset: uint64(dataOffset)})
	copy(section[fileTableOffset:], table)
	copy(section[dataOffset:], data)
	return section
}

func pad512(data []byte) []byte {
	return append(data, make([]byte, (0x200-len(data)%0x200)%0x200)...)
}

func writeNspWithIcon(t *testing.T, icon []byte) string {
	t.Helper()
	nacp := make([]byte, 0x3080)
	copy(nacp, "Icon Game")
	control := makeSyntheticNCA(t, makeRomfsWithFiles(t,
		[]string{"control.nacp", "icon_Japanese.dat", "icon_AmericanEnglish.dat"},
		[][]byte{nacp, {0xFF, 0xD8, 0xFF, 0x00, 'j', 'p'}, icon}), 0)

	controlId := [16]byte{0xc0, 0x17, 0xc0, 0x17, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	cnmt := make([]byte, 0x20+0x38)
	binary.LittleEndian.PutUint64(cnmt[0:8], 0x0100000000010000)
	cnmt[0xC] = ContentMetaType_Application
	binary.LittleEndian.PutUint16(cnmt[0x10:0x12], 1)
	copy(cnmt[0x20+0x20:0x20+0x30], controlId[:])
	cnmt[0x20+0x36] = 3 // control
	meta := makeSyntheticNCA(t, pad512(makePFS0(pfs0Magic, []string{"Application.cnmt"}, [][]byte{cnmt})), 1)

	nsp := makePFS0(pfs0Magic, []string{"meta.cnmt.nca", hex.EncodeToString(controlId[:]) + ".nca"}, [][]byte{meta, control})
	path := filepath.Join(t.TempDir(), "Icon Game [0100000000010000][v0].nsp")
	if err := os.WriteFile(path, nsp, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractIconFromGameFiles(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for x := 0; x < 64; x++ {
		img.Set(x, x, color.RGBA{255, 0, 0, 255})
	}
	var encoded bytes.Buffer
	jpeg.Encode(&encoded, img, nil)
	icon := encoded.Bytes()

	path := writeNspWithIcon(t, icon)
	got, err := ExtractIcon(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, icon) {
		t.Fatalf("the English icon is preferred: got %d bytes", len(got))
	}

	// NSZ files keep the control content as it is (only program and data are compressed),
	// so they are read the same way
	nsz := strings.TrimSuffix(path, ".nsp") + ".nsz"
	if err := os.Rename(path, nsz); err != nil {
		t.Fatal(err)
	}
	if fromNsz, err := ExtractIcon(nsz); err != nil || !bytes.Equal(fromNsz, icon) {
		t.Fatalf("icon of the NSZ: %d bytes, %v", len(fromNsz), err)
	}

	if _, err := ExtractIcon(filepath.Join(t.TempDir(), "missing.nsp")); err == nil {
		t.Fatal("a missing file is an error")
	}
}

// packTitle is a game or an update of a pack written by writePackWithIcons, with its icon (none
// when nil).
type packTitle struct {
	kind    byte
	version uint32
	icon    []byte
}

// writePackWithIcons writes one NSP that holds the titles given, each with its meta and its
// control content, in their order.
func writePackWithIcons(t *testing.T, titles []packTitle) string {
	t.Helper()
	names, contents := []string{}, [][]byte{}
	for i, title := range titles {
		nacp := make([]byte, 0x3080)
		copy(nacp, "Pack Game")
		files, data := []string{"control.nacp"}, [][]byte{nacp}
		if title.icon != nil {
			files, data = append(files, "icon_AmericanEnglish.dat"), append(data, title.icon)
		}
		control := makeSyntheticNCA(t, makeRomfsWithFiles(t, files, data), 0)
		controlId := [16]byte{0xc0, 0x17, byte(i), 0x17, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
		cnmt := make([]byte, 0x20+0x38)
		id, metaName := uint64(0x0100000000010000), "Application.cnmt"
		if title.kind == ContentMetaType_Patch {
			id, metaName = 0x0100000000010800, "Patch.cnmt"
		}
		binary.LittleEndian.PutUint64(cnmt[0:8], id)
		binary.LittleEndian.PutUint32(cnmt[8:12], title.version)
		cnmt[0xC] = title.kind
		binary.LittleEndian.PutUint16(cnmt[0x10:0x12], 1)
		copy(cnmt[0x20+0x20:0x20+0x30], controlId[:])
		cnmt[0x20+0x36] = 3 // control
		meta := makeSyntheticNCA(t, pad512(makePFS0(pfs0Magic, []string{metaName}, [][]byte{cnmt})), 1)
		names = append(names, fmt.Sprintf("meta%d.cnmt.nca", i), hex.EncodeToString(controlId[:])+".nca")
		contents = append(contents, meta, control)
	}
	path := filepath.Join(t.TempDir(), "Pack Game [0100000000010000][v131072].nsp")
	if err := os.WriteFile(path, makePFS0(pfs0Magic, names, contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// shadeJpeg is a small JPEG image, of one shade of red.
func shadeJpeg(t *testing.T, shade uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := 0; x < 8; x++ {
		img.Set(x, x, color.RGBA{shade, 0, 0, 255})
	}
	var encoded bytes.Buffer
	jpeg.Encode(&encoded, img, nil)
	return encoded.Bytes()
}

// A pack of a game and its updates gives the icon of the game, or of its newest update when asked:
// the one the console shows once the update is installed.
func TestExtractUpdateIcon(t *testing.T) {
	game, older, newer := shadeJpeg(t, 10), shadeJpeg(t, 120), shadeJpeg(t, 240)
	// the newest update before the older one, as a pack may hold them
	pack := writePackWithIcons(t, []packTitle{
		{kind: ContentMetaType_Application, icon: game},
		{kind: ContentMetaType_Patch, version: 131072, icon: newer},
		{kind: ContentMetaType_Patch, version: 65536, icon: older},
	})
	if got, err := ExtractIcon(pack); err != nil || !bytes.Equal(got, game) {
		t.Fatalf("the icon of the game: %v", err)
	}
	if got, err := ExtractUpdateIcon(pack); err != nil || !bytes.Equal(got, newer) {
		t.Fatalf("the icon of the newest update: %v", err)
	}
	// the updates in the other order: still the newest
	pack = writePackWithIcons(t, []packTitle{
		{kind: ContentMetaType_Patch, version: 65536, icon: older},
		{kind: ContentMetaType_Application, icon: game},
		{kind: ContentMetaType_Patch, version: 131072, icon: newer},
	})
	if got, err := ExtractUpdateIcon(pack); err != nil || !bytes.Equal(got, newer) {
		t.Fatalf("the newest update, wherever it is: %v", err)
	}
	if got, err := ExtractIcon(pack); err != nil || !bytes.Equal(got, game) {
		t.Fatalf("the game first, wherever it is: %v", err)
	}

	// an update without an icon gives the one of the game
	pack = writePackWithIcons(t, []packTitle{
		{kind: ContentMetaType_Application, icon: game},
		{kind: ContentMetaType_Patch, version: 65536},
	})
	if got, err := ExtractUpdateIcon(pack); err != nil || !bytes.Equal(got, game) {
		t.Fatalf("the game, as its update has none: %v", err)
	}
	// and a game alone its own
	if got, err := ExtractUpdateIcon(writeNspWithIcon(t, game)); err != nil || !bytes.Equal(got, game) {
		t.Fatalf("a game alone: %v", err)
	}
}

func TestIconOrder(t *testing.T) {
	controls := []controlContent{{id: "u1", update: true, version: 65536}, {id: "g1"}, {id: "u2", update: true, version: 131072}, {id: "g2"}}
	if got := strings.Join(iconOrder(controls, false), " "); got != "g2 g1 u1 u2" {
		t.Errorf("the games first, as before: %s", got)
	}
	if got := strings.Join(iconOrder(controls, true), " "); got != "u2 u1 g2 g1" {
		t.Errorf("the newest update first: %s", got)
	}
}
