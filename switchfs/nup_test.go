package switchfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/testnup"
)

var testJpeg = testnup.Jpeg

func buildNup(_ testing.TB, packages []testnup.Package) []byte { return testnup.Build(packages) }

func rehash(file []byte) { testnup.Rehash(file) }

func tlv(kind uint16, payload []byte) testnup.Record {
	return testnup.Record{Type: kind, Payload: payload}
}

func localized(entries ...string) []byte { return testnup.Localized(entries...) }

func writeNup(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Game.nup")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNupGameUpdatesAndDlc(t *testing.T) {
	path := writeNup(t, buildNup(t, []testnup.Package{
		{TitleId: 0x040074A01BF12000, Kind: NupKindBase, Version: 0, Meta: []testnup.Record{
			tlv(nupTlvTitleName, []byte("Bananza")),
			tlv(nupTlvPublisher, []byte("Nintendo")),
			tlv(nupTlvDisplayVersion, []byte("1.0.0")),
			tlv(nupTlvLocalizedTitles, localized("ja", "ドンキーコング バナンザ")),
			tlv(nupTlvIcon, testJpeg),
		}},
		{TitleId: 0x040074A01BF12000, Kind: NupKindUpdate, Version: 65536},
		{TitleId: 0x040074A01BF12000, Kind: NupKindUpdate, Version: 131072},
		{TitleId: 0x040074A01BF13001, Kind: NupKindAddon, Version: 0},
	}))

	metadata, err := ReadNupMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 3 {
		t.Fatalf("got %d titles, want the game, its newest update and the DLC: %v", len(metadata), metadata)
	}
	game := metadata["040074a01bf12000"]
	if game == nil || game.Type != "BASE" || game.Ncap == nil {
		t.Fatalf("game: %+v", game)
	}
	if name := game.Ncap.TitleName[Language(Japanese).String()].Title; name != "ドンキーコング バナンザ" {
		t.Errorf("Japanese name %q", name)
	}
	english := game.Ncap.TitleName[Language(AmericanEnglish).String()]
	if english.Title != "Bananza" || english.Publisher != "Nintendo" || game.Ncap.DisplayVersion != "1.0.0" {
		t.Errorf("default name, publisher or version: %+v %q", english, game.Ncap.DisplayVersion)
	}
	if !game.Ncap.NamesRead || !game.Ncap.AttributesRead || !game.Ncap.PublishersRead {
		t.Error("the names from a NUP are not to be read again")
	}
	update := metadata["040074a01bf12800"]
	if update == nil || update.Type != "UPD" || update.Version != 131072 || update.Ncap != nil {
		t.Fatalf("update: %+v", update)
	}
	if dlc := metadata["040074a01bf13001"]; dlc == nil || dlc.Type != "DLC" {
		t.Fatalf("DLC: %+v", dlc)
	}

	icon, err := ExtractIcon(path)
	if err != nil || !bytes.Equal(icon, testJpeg) {
		t.Fatalf("icon %v, %v", icon, err)
	}
}

func TestNupWithoutNamesOrIcon(t *testing.T) {
	// what the reference packer writes unless names are given: the ID and the version only
	path := writeNup(t, buildNup(t, []testnup.Package{
		{TitleId: 0x0400C3F00006E000, Kind: NupKindBase, Version: 0},
		{TitleId: 0, Kind: NupKindAddon, Version: 0},
	}))
	metadata, err := ReadNupMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	game := metadata["0400c3f00006e000"]
	if len(metadata) != 1 || game == nil || game.Ncap != nil {
		t.Fatalf("metadata %v", metadata)
	}
	if _, err := ExtractIcon(path); err == nil {
		t.Error("an icon was found in a NUP without one")
	}
}

// The table of contents may be anywhere after the header, as the format allows a writer.
func TestNupTableOfContentsAfterAGap(t *testing.T) {
	path := writeNup(t, testnup.BuildWith([]testnup.Package{{TitleId: 0x0400C3F00006E000, Kind: NupKindBase, Version: 0,
		Files: []testnup.File{{Path: "exefs/main.npdm", Data: []byte("npdm")}, {Path: "romfs/data.bin", Data: bytes.Repeat([]byte("data"), 100), Zstd: true}}}},
		testnup.Options{Dedup: true, TocGap: 0x80}))
	if metadata, err := ReadNupMetadata(path); err != nil || metadata["0400c3f00006e000"] == nil {
		t.Fatalf("%v %v", metadata, err)
	}
	if _, err := VerifyNup(context.Background(), path, nil); err != nil {
		t.Fatal(err)
	}
}

func TestNupUpdateWithoutGame(t *testing.T) {
	container := &NupContainer{Packages: []NupPackage{
		{TitleId: 0x0100152000022000, Kind: NupKindUpdate, Flags: 1, Version: 393216},
	}}
	if update := container.Metadata()["0100152000022800"]; update == nil || update.Version != 393216 {
		t.Fatalf("standalone update: %+v", container.Metadata())
	}
	// an update already stored with the ID of the eShop keeps it
	if id := NupTitleId(NupPackage{TitleId: 0x0100152000022800, Kind: NupKindUpdate}); id != "0100152000022800" {
		t.Errorf("update ID %s", id)
	}
}

func TestNupLocalizedIconAndLanguages(t *testing.T) {
	container, err := readNupBytes(buildNup(t, []testnup.Package{
		{TitleId: 0x0400000000010000, Kind: NupKindBase, Meta: []testnup.Record{
			tlv(nupTlvLocalizedTitles, localized("en", "Game", "zh-Hans", "游戏", "xx", "ignored")),
			tlv(nupTlvLocalizedIcons, localized("ja", "not an image", "fr", string(testJpeg))),
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	nacp := container.Packages[0].nacp()
	if nacp == nil || nacp.TitleName[Language(AmericanEnglish).String()].Title != "Game" ||
		nacp.TitleName[Language(Chinese).String()].Title != "游戏" || nacp.TitleName[Language(German).String()].Title != "" {
		t.Fatalf("names %+v", nacp)
	}
	if icon, err := container.Icon(); err != nil || !bytes.Equal(icon, testJpeg) {
		t.Fatalf("icon %v, %v", icon, err)
	}
}

func TestNupNacpRecord(t *testing.T) {
	raw := make([]byte, 0x4000)
	copy(raw, "Real Name")
	copy(raw[0x200:], "Real Publisher")
	copy(raw[0x3060:], "2.1.0")
	container, err := readNupBytes(buildNup(t, []testnup.Package{
		{TitleId: 0x0400000000010000, Kind: NupKindBase, Meta: []testnup.Record{
			tlv(nupTlvNacp, raw), tlv(nupTlvTitleName, []byte("Other Name")),
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	nacp := container.Packages[0].nacp()
	if english := nacp.TitleName[Language(AmericanEnglish).String()]; english.Title != "Real Name" ||
		english.Publisher != "Real Publisher" || nacp.DisplayVersion != "2.1.0" {
		t.Fatalf("the NACP of the file comes first: %+v %q", english, nacp.DisplayVersion)
	}
}

// The packer of the format counted the types of the metadata from 0 for a while: such a block is
// read with its types where the format puts them, so the publisher is not taken for the name.
func TestNupTypesCountedFromZero(t *testing.T) {
	path := writeNup(t, buildNup(t, []testnup.Package{{TitleId: 0x040074A01BF12000, Kind: NupKindBase, Meta: []testnup.Record{
		tlv(0, []byte("META npdm")), tlv(3, []byte("1.0.0")),
		tlv(4, []byte("Made-up Bananza")), tlv(5, []byte("Nobody")), tlv(6, bytes.Repeat([]byte{9}, 32)),
	}}}))
	metadata, err := ReadNupMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	game := metadata["040074a01bf12000"]
	if game == nil || game.Ncap == nil {
		t.Fatalf("game: %+v", game)
	}
	english := game.Ncap.TitleName[Language(AmericanEnglish).String()]
	if english.Title != "Made-up Bananza" || english.Publisher != "Nobody" || game.Ncap.DisplayVersion != "1.0.0" {
		t.Errorf("name, publisher or version: %+v %q", english, game.Ncap.DisplayVersion)
	}
	container, err := ReadNup(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta := container.Packages[0].Meta; string(meta[1]) != "META npdm" || meta[0] != nil {
		t.Errorf("the NPDM is not where the format puts it: %v", meta)
	}
	// Eglantine and Lichii read the types as they are written: told, in the metadata too
	if emulators := container.Emulators(); !container.Packages[0].ZeroBased || !emulators.ZeroBased || emulators.Ok() ||
		!strings.Contains(emulators.String(), "the types of its metadata are counted from 0") {
		t.Errorf("counted from 0: %+v", emulators)
	}
	if game.Emulators == nil || !game.Emulators.ZeroBased {
		t.Errorf("metadata: %+v", game.Emulators)
	}
}

func readNupBytes(data []byte) (*NupContainer, error) {
	return readNup(bytes.NewReader(data), int64(len(data)))
}

func TestNupRefusesDamagedFiles(t *testing.T) {
	good := buildNup(t, []testnup.Package{{TitleId: 0x0400000000010000, Kind: NupKindBase, Meta: []testnup.Record{
		tlv(nupTlvTitleName, []byte("Game"))}}})
	cases := map[string]struct {
		change func([]byte) []byte
		want   string
	}{
		"newer version": {func(b []byte) []byte { b[3] = 2; return b }, "version 2"},
		"not a NUP":     {func(b []byte) []byte { copy(b, "PFS0"); return b }, "not a NUP"},
		"damaged table": {func(b []byte) []byte { b[nupHeaderSize+0x31]++; return b }, "damaged"},
		"cut short":     {func(b []byte) []byte { return b[:len(b)-4] }, "cut short"},
		"longer":        {func(b []byte) []byte { return append(b, 0) }, "does not match"},
		"unknown flags": {func(b []byte) []byte { b[0x4C] = 1; return b }, "flags"},
		"unknown kind": {func(b []byte) []byte {
			b[nupHeaderSize+nupTocHeadSize+0x18] = 9
			rehash(b)
			return b
		}, "unknown kind"},
		"metadata outside": {func(b []byte) []byte {
			binary.LittleEndian.PutUint64(b[nupHeaderSize+nupTocHeadSize+0x20:], uint64(len(b)))
			rehash(b)
			return b
		}, "outside the file"},
		"record past its block": {func(b []byte) []byte {
			offset := binary.LittleEndian.Uint64(b[nupHeaderSize+nupTocHeadSize+0x20:])
			binary.LittleEndian.PutUint32(b[offset+4:], 1000)
			return b
		}, "runs past"},
		"too many packages": {func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b[0x48:], 5000)
			return b
		}, "number of packages"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := readNupBytes(c.change(append([]byte{}, good...)))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %v, want %q", err, c.want)
			}
		})
	}
	if _, err := readNupBytes(good[:len(good)-4]); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("a file cut short is told as such: %v", err)
	}
	if _, err := readNupBytes(good[:0x40]); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("a header cut short is told as such: %v", err)
	}
}

func FuzzReadNup(f *testing.F) {
	f.Add(buildNup(f, []testnup.Package{{TitleId: 0x0400000000010000, Kind: NupKindBase, Meta: []testnup.Record{
		tlv(nupTlvTitleName, []byte("Game")), tlv(nupTlvLocalizedTitles, localized("ja", "ゲーム")),
		tlv(nupTlvLocalizedIcons, localized("en-us", string(testJpeg)))}}}))
	f.Fuzz(func(t *testing.T, data []byte) {
		// whatever the bytes, reading never panics nor reads past the file
		container, err := readNupBytes(data)
		if err == nil {
			container.Metadata()
			container.Icon()
		}
		if len(data) > nupHeaderSize+nupTocHeadSize {
			rehashed := append([]byte{}, data...)
			if tocSize := binary.LittleEndian.Uint64(rehashed[0x10:]); tocSize <= uint64(len(rehashed)-nupHeaderSize) {
				rehash(rehashed)
				if container, err := readNupBytes(rehashed); err == nil {
					container.Metadata()
					container.Icon()
				}
			}
		}
	})
}

// Some packers store the file name as the name of the game: its title ID and version are left out.
func TestNupNamesWithoutFileTags(t *testing.T) {
	container, err := readNupBytes(buildNup(t, []testnup.Package{{TitleId: 0x0400245021D2C000, Kind: NupKindBase, Meta: []testnup.Record{
		tlv(nupTlvTitleName, []byte("Yoshi and the Mysterious Book [0400245021D2C000][v131072]")),
		tlv(nupTlvLocalizedTitles, localized("ja", "ヨッシー [0400245021D2C000]")),
	}}}))
	if err != nil {
		t.Fatal(err)
	}
	nacp := container.Packages[0].nacp()
	if english := nacp.TitleName[Language(AmericanEnglish).String()].Title; english != "Yoshi and the Mysterious Book" {
		t.Errorf("name %q", english)
	}
	if japanese := nacp.TitleName[Language(Japanese).String()].Title; japanese != "ヨッシー" {
		t.Errorf("Japanese name %q", japanese)
	}
	if name := nupName([]byte("Game [Demo]")); name != "Game [Demo]" {
		t.Errorf("other brackets are kept: %q", name)
	}
}
