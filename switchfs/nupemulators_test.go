package switchfs

import (
	"context"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/testnup"
)

// nupPlayable is a game and its update as a dump gives them, with main and main.npdm in each
// ExeFS.
func nupPlayable() []testnup.Package {
	packages := nupGame()
	packages[1].Files = append(packages[1].Files, testnup.File{Path: "exefs/main.npdm", Data: []byte("NPDM of the update")})
	return packages
}

func emulatorsOf(t *testing.T, nup []byte) NupEmulators {
	t.Helper()
	container, err := readNupBytes(nup)
	if err != nil {
		t.Fatal(err)
	}
	return container.Emulators()
}

func TestNupEmulatorsOpenAGoodNup(t *testing.T) {
	got := emulatorsOf(t, testnup.Build(nupPlayable()))
	if !got.Ok() || got.String() != "" {
		t.Errorf("%+v", got)
	}
}

// What the format and the library let be, and the emulators refuse.
func TestNupEmulatorsRefuseWhatTheyDoNotKnow(t *testing.T) {
	le := binary.LittleEndian
	cases := map[string]struct {
		change func(nup []byte)
		want   string
	}{
		"kind 3": {func(nup []byte) { nup[nupHeaderSize+nupTocHeadSize+2*nupPackageSize+0x18] = 3; testnup.Rehash(nup) },
			"package 2 is of kind 3, which the format keeps for later"},
		"flags": {func(nup []byte) { nup[nupHeaderSize+nupTocHeadSize+0x19] = 0x02; testnup.Rehash(nup) },
			"package 0 has the flags 0x2, and only the first is known"},
		"codec flags": {func(nup []byte) { setNupFile(nup, 3, 0, func(record []byte) { record[0x41] = 1 }) },
			"a file has codec flags"},
		"empty with data": {func(nup []byte) {
			for i := range int(le.Uint64(nup[nupHeaderSize+0x10:])) {
				setNupFile(nup, 3, i, func(record []byte) {
					if le.Uint64(record[0x08:]) == 0 {
						le.PutUint64(record[0x10:], 0x1000)
					}
				})
			}
		}, "a file of 0 bytes points at data"},
	}
	for name, c := range cases {
		nup := testnup.Build(nupPlayable())
		c.change(nup)
		got := emulatorsOf(t, nup)
		if len(got.Refused) != 1 || !strings.Contains(got.Refused[0].String(), c.want) {
			t.Errorf("%s: %+v", name, got)
		}
		if !strings.HasPrefix(got.String(), "Eglantine and Lichii do not open it: ") {
			t.Errorf("%s: %q", name, got.String())
		}
	}
	// the files marked with the high bit, as a build of the packer of the format writes them
	tagged := testnup.BuildWith(nupPlayable(), testnup.Options{TagFiles: true, Dedup: true})
	if got := emulatorsOf(t, tagged); len(got.Refused) != 1 || !strings.Contains(got.Refused[0].String(), "high bit") {
		t.Errorf("tagged: %+v", got)
	}
	// names by language that run past their record, which Verify tells as damaged metadata
	packages := nupPlayable()
	packages[2].Meta = []testnup.Record{{Type: testnup.LocalizedTitles, Payload: []byte{5, 'e', 'n'}}}
	if got := emulatorsOf(t, testnup.Build(packages)); len(got.Refused) != 1 ||
		got.Refused[0].String() != "the names or icons by language of package 2 run past their record" {
		t.Errorf("languages: %+v", got)
	}
}

// A folder of the table inside itself is refused, even one no package holds.
func TestNupEmulatorsRefuseALoop(t *testing.T) {
	nup := testnup.Build(nupPlayable())
	le := binary.LittleEndian
	toc := nup[nupHeaderSize:]
	dirs, files := le.Uint64(toc[0x08:]), le.Uint64(toc[0x10:])
	children := nupTocHeadSize + 3*nupPackageSize + dirs*nupDirSize + files*nupFileSize
	le.PutUint32(toc[children:], 0)
	testnup.Rehash(nup)
	if got := emulatorsOf(t, nup); len(got.Refused) != 1 || !strings.Contains(got.Refused[0].String(), "inside itself") {
		t.Errorf("%+v", got)
	}
}

func TestNupEmulatorsNeedMainAndItsNpdm(t *testing.T) {
	// the newest update has an ExeFS without main.npdm: it is the one they take
	got := emulatorsOf(t, testnup.Build(nupGame()))
	if len(got.Refused) != 0 || got.NoStart == nil || got.NoStart.String() != "the ExeFS of update v131072 lacks: main.npdm" {
		t.Errorf("update: %+v", got)
	}
	if got.String() != "Eglantine and Lichii cannot start it: the ExeFS of update v131072 lacks: main.npdm" {
		t.Errorf("update: %q", got.String())
	}
	// an update without an ExeFS: the one of the game
	packages := nupPlayable()
	packages[1].Files = []testnup.File{{Path: "romfs/Data/patch.bin", Data: []byte("patched")}}
	packages[0].Files = packages[0].Files[1:]
	if got := emulatorsOf(t, testnup.Build(packages)); got.NoStart == nil || got.NoStart.String() != "the ExeFS of the game lacks: main" {
		t.Errorf("game: %+v", got.NoStart)
	}
	// DLC only, and a game without an ExeFS
	if got := emulatorsOf(t, testnup.Build(nupPlayable()[2:])); got.NoStart == nil || !strings.Contains(got.NoStart.String(), "no game or update") {
		t.Errorf("DLC: %+v", got.NoStart)
	}
	romfs := testnup.Build([]testnup.Package{{TitleId: 1, Files: []testnup.File{{Path: "romfs/a", Data: []byte("a")}}}})
	if got := emulatorsOf(t, romfs); got.NoStart == nil || got.NoStart.String() != "no game or update in it has an ExeFS" {
		t.Errorf("romfs only: %+v", got.NoStart)
	}
	// an update alone starts on its own
	alone := nupPlayable()[1:2]
	alone[0].Flags = 1
	if got := emulatorsOf(t, testnup.Build(alone)); !got.Ok() {
		t.Errorf("update alone: %+v", got)
	}
}

// A NUP the library does not read for a reason the emulators share says so.
func TestNupEmulatorsRefuseWhatTheLibraryRefuses(t *testing.T) {
	for name, change := range map[string]func(nup []byte){
		"flags":     func(nup []byte) { nup[0x4C] = 1 },
		"unaligned": func(nup []byte) { setNupFile(nup, 3, 0, func(record []byte) { record[0x10]++ }) },
		"codec":     func(nup []byte) { setNupFile(nup, 3, 0, func(record []byte) { record[0x40] = 2 }) },
		"kind":      func(nup []byte) { nup[nupHeaderSize+nupTocHeadSize+0x18] = 9; testnup.Rehash(nup) },
		"records":   breakLastNupRecord,
	} {
		nup := testnup.Build(nupPlayable())
		change(nup)
		_, err := readNupBytes(nup)
		if err == nil || !NupEmulatorsRefuse(err) {
			t.Errorf("%s: %v", name, err)
		}
		// a check says so after the reason
		_, err = VerifyFile(context.Background(), writeNup(t, nup), nil)
		if err == nil || !strings.HasSuffix(err.Error(), "; Eglantine and Lichii do not open it either") {
			t.Errorf("%s: check: %v", name, err)
		}
	}
	// a NUP cut short is not one they are said to refuse: it is a copy that stopped
	nup := testnup.Build(nupPlayable())
	if _, err := readNupBytes(nup[:len(nup)-1]); err == nil || NupEmulatorsRefuse(err) {
		t.Errorf("cut: %v", err)
	}
}

// breakLastNupRecord makes the size of the last record of the metadata of the game run past its
// block.
func breakLastNupRecord(nup []byte) {
	le := binary.LittleEndian
	record := nup[nupHeaderSize+nupTocHeadSize:]
	offset, size := le.Uint64(record[0x20:]), uint64(le.Uint32(record[0x28:]))
	block := nup[offset : offset+size]
	for at := 0; at+nupTlvHeadSize <= len(block); {
		length := int(le.Uint32(block[at+4:]))
		if at+nupTlvHeadSize+length == len(block) {
			le.PutUint32(block[at+4:], uint32(length+64))
			return
		}
		at += nupTlvHeadSize + length
	}
	panic("no last record")
}

// A check of a whole NUP and the metadata the library reads tell what the emulators make of it.
func TestNupEmulatorsInTheCheckAndTheMetadata(t *testing.T) {
	path := writeNup(t, testnup.Build(nupGame()))
	check, err := VerifyFile(context.Background(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if check.Emulators == nil || check.Emulators.NoStart == nil {
		t.Fatalf("check: %+v", check.Emulators)
	}
	metadata, err := ReadNupMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	for id, meta := range metadata {
		if meta.Emulators == nil || meta.Emulators.String() != check.Emulators.String() {
			t.Errorf("%s: %+v", id, meta.Emulators)
		}
	}
	// any other file has none
	if (FileCheck{}).Emulators != nil {
		t.Error("a file check tells the emulators of a NUP only")
	}
}
