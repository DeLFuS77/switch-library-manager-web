package switchfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/testnup"
	"github.com/dtrunk90/switch-library-manager-web/internal/testnx"
)

// writeSplit writes data split in parts of size bytes (the last one shorter) in a folder named
// name, as the FAT32 mode of the tools that install games does, and gives the folder.
func writeSplit(t *testing.T, name string, data []byte, size int) string {
	t.Helper()
	folder := filepath.Join(t.TempDir(), name)
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	for part := 0; len(data) > 0; part++ {
		n := min(size, len(data))
		if err := os.WriteFile(filepath.Join(folder, fmt.Sprintf("%02d", part)), data[:n], 0o644); err != nil {
			t.Fatal(err)
		}
		data = data[n:]
	}
	return folder
}

func TestIsNupFileOfSplitParts(t *testing.T) {
	for path, nup := range map[string]bool{
		filepath.Join("lib", "Game.nup"):              true,
		filepath.Join("lib", "Game.nup", "00"):        true,
		filepath.Join("lib", "Game.NUP", "03"):        true,
		filepath.Join("lib", "Game.nsp", "00"):        false,
		filepath.Join("lib", "Game.nup", "notes.txt"): false,
		"00": false,
	} {
		if IsNupFile(path) != nup {
			t.Errorf("IsNupFile(%s) = %v", path, !nup)
		}
	}
	if !IsNxFile(filepath.Join("lib", "Game.nx", "01")) || IsNxFile(filepath.Join("lib", "Game.nup", "00")) {
		t.Error("IsNxFile of the parts of a split .nx")
	}
	if got := WholeFile(filepath.Join("lib", "Game.nup", "00")); got != filepath.Join("lib", "Game.nup") {
		t.Errorf("WholeFile: %s", got)
	}
	if got := WholeFile(filepath.Join("lib", "Game.nup")); got != filepath.Join("lib", "Game.nup") {
		t.Errorf("WholeFile of a whole file: %s", got)
	}
}

// A NUP split in parts reads as the NUP whole, given its folder or its first part, and its check
// goes through every part.
func TestSplitNupReadsAsOne(t *testing.T) {
	data := testnup.Build(nupGame())
	whole, err := ReadNupMetadata(writeNup(t, data))
	if err != nil {
		t.Fatal(err)
	}
	folder := writeSplit(t, "Made-up Game [0400486024DEC000][v65536].nup", data, len(data)/3+1)
	for _, path := range []string{folder, filepath.Join(folder, "00")} {
		if !IsNupFile(path) {
			t.Fatalf("%s is not told a NUP", path)
		}
		metadata, err := ReadNupMetadata(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if !reflect.DeepEqual(metadata, whole) {
			t.Fatalf("%s reads otherwise than the NUP whole: %v, want %v", path, metadata, whole)
		}
		size, err := WholeSize(path)
		if err != nil || size != int64(len(data)) {
			t.Fatalf("%s: size %d (%v), want %d", path, size, err, len(data))
		}
		var last, total int64
		check, err := VerifyFile(WithThreads(context.Background(), 4), path, func(done, all int64) { last, total = done, all })
		if err != nil || check.Partial || last != total || total == 0 {
			t.Fatalf("%s: check %+v, %v, %d of %d", path, check, err, last, total)
		}
	}
}

// A damaged byte in a part other than the first is found.
func TestSplitNupDamagedInALaterPart(t *testing.T) {
	packages := nupGame()
	data := testnup.Build(packages)
	offset, _ := testnup.Extent(data, packages, 0, packages[0].Files[len(packages[0].Files)-1].Path)
	size := offset/2 + 1
	data[offset] ^= 0x55
	folder := writeSplit(t, "Game.nup", data, size)
	_, err := VerifyFile(context.Background(), filepath.Join(folder, "00"), func(int64, int64) {})
	var damaged *NupDamagedError
	if !errors.As(err, &damaged) {
		t.Fatalf("the damage in part %d is not found: %v", offset/size, err)
	}
}

// A part missing in the middle makes the file unreadable, not a shorter NUP.
func TestSplitNupWithAPartMissing(t *testing.T) {
	data := testnup.Build(nupGame())
	folder := writeSplit(t, "Game.nup", data, len(data)/4+1)
	if err := os.Remove(filepath.Join(folder, "01")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadNup(folder); err == nil {
		t.Fatal("read with a part missing")
	}
}

// A NUP split in parts is not written in place: it is joined first.
func TestSplitNupIsNotWrittenInPlace(t *testing.T) {
	folder := writeSplit(t, "Game.nup", testnup.Build(nupGame()), 1<<14)
	before, _ := os.ReadFile(filepath.Join(folder, "00"))
	if _, err := FixNupZeroBased(filepath.Join(folder, "00")); !errors.Is(err, errSplitWrite) {
		t.Fatalf("fixed in place: %v", err)
	}
	if _, err := SetNupIcon(folder, testJpeg); !errors.Is(err, errSplitWrite) {
		t.Fatalf("icon set in place: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(folder, "00"))
	if !reflect.DeepEqual(before, after) {
		t.Fatal("the first part changed")
	}
}

// A .nx split in parts reads as the .nx whole.
func TestSplitNxReadsAsOne(t *testing.T) {
	data := testnx.Build(testnx.Game{Id: 0x0400AAAA00010000, Platform: 2, Title: "Made-up Game", NacpName: "Made-up Game",
		Snapshots: []testnx.Snapshot{{Kind: testnx.Base}, {Kind: testnx.Update, Version: 65536}}})
	plain := filepath.Join(t.TempDir(), "Game.nx")
	if err := os.WriteFile(plain, data, 0o644); err != nil {
		t.Fatal(err)
	}
	whole, err := ReadNxMetadata(plain)
	if err != nil {
		t.Fatal(err)
	}
	folder := writeSplit(t, "Made-up Game.nx", data, 5000)
	for _, path := range []string{folder, filepath.Join(folder, "00")} {
		if !IsNxFile(path) {
			t.Fatalf("%s is not told a .nx", path)
		}
		metadata, err := ReadNxMetadata(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if !reflect.DeepEqual(metadata, whole) {
			t.Fatalf("%s reads otherwise than the .nx whole: %v, want %v", path, metadata, whole)
		}
		// the builder of the tests stores no file of the game: the data of the split .nx opens as
		// the whole one does
		_, plainErr := OpenNxData(plain)
		data, err := OpenNxData(path)
		if err == nil {
			data.Close()
		}
		if fmt.Sprint(err) != fmt.Sprint(plainErr) {
			t.Fatalf("%s: opened %v, the whole file %v", path, err, plainErr)
		}
	}
}
