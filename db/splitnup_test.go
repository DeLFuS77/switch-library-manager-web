package db

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// splitParts gives the parts of a file split for FAT32 in a folder named name: "name/00",
// "name/01"..., of size bytes each but the last.
func splitParts(name string, data []byte, size int) map[string][]byte {
	parts := map[string][]byte{}
	for part := 0; len(data) > 0; part++ {
		n := min(size, len(data))
		parts[filepath.Join(name, fmt.Sprintf("%02d", part))] = data[:n]
		data = data[n:]
	}
	return parts
}

// A NUP and a .nx split in parts are read whole, by their first part, as the files they are: the
// game, its update and its DLC, never an issue.
func TestScanReadsSplitNupAndNx(t *testing.T) {
	for _, c := range []struct {
		name string
		data []byte
	}{
		{"Made-up Game [0400AAAA00010000][v0].nup", otherFormatNup("Made-up Game")},
		{"Made-up Game.nx", otherFormatNx("Made-up Game")},
	} {
		files := splitParts(c.name, c.data, len(c.data)/3+1)
		localDB, _ := scanOtherFormat(t, files)
		game := localDB.TitlesMap["0400aaaa00010"]
		if game == nil || !game.BaseExist || len(game.Dlc) != 1 || game.LatestUpdate != 393216 || !game.IsSplit {
			t.Fatalf("%s: the game: %#v (skipped %v)", c.name, game, localDB.Skipped)
		}
		if game.File.ExtendedInfo.FileName != "00" || game.File.Metadata == nil || game.File.Metadata.Ncap == nil {
			t.Fatalf("%s: read from %s: %#v", c.name, game.File.ExtendedInfo.FileName, game.File.Metadata)
		}
		for file, reason := range localDB.Skipped {
			t.Errorf("%s: %s listed as an issue: %+v", c.name, file.FileName, reason)
		}
	}
}

// A NUP split in parts and an NSZ of the same game are two formats: neither is a duplicate or
// an older version of the other, in either order, as with a NUP whole.
func TestSplitNupAndNszOfAGameAreNoDuplicates(t *testing.T) {
	for _, folder := range []string{"a game.nup", "z game.nup"} {
		files := splitParts(folder, otherFormatNup("Made-up Game"), 4096)
		files["Made-up Game [0400AAAA00010000][v0].nsz"] = make([]byte, 300)
		files["Made-up Game [0400AAAA00010800][v393216].nsz"] = make([]byte, 40)
		files["Made-up Game DLC [0400AAAA00011001][v0].nsz"] = make([]byte, 20)
		localDB, _ := scanOtherFormat(t, files)
		if len(localDB.TitlesMap) != 1 {
			t.Fatalf("%s: one game in the Library: %v", folder, localDB.TitlesMap)
		}
		for file, reason := range localDB.Skipped {
			if reason.ReasonCode == REASON_DUPLICATE || reason.ReasonCode == REASON_OLD_UPDATE {
				t.Fatalf("%s: %s is listed to delete: %+v", folder, filepath.Join(file.BaseFolder, file.FileName), reason)
			}
			if reason.ReasonCode == REASON_OTHER_FORMAT && !KeptInOtherFormat(file, reason) {
				t.Fatalf("%s: %s may be deleted for the other format: %+v", folder, file.FileName, reason)
			}
		}
	}
}

// The format of a file split in parts is the one of its folder.
func TestOtherFormatOfSplitParts(t *testing.T) {
	nup := filepath.Join("lib", "Game.nup", "00")
	nsp := filepath.Join("lib", "Game.nsp", "00")
	nx := filepath.Join("lib", "Game.nx", "00")
	if !OtherFormat(nup, nsp) || !OtherFormat(nx, nsp) || !OtherFormat(nup, nx) || OtherFormat(nup, filepath.Join("b", "Other.nup")) {
		t.Fatal("OtherFormat of the parts of split files")
	}
	if fileFormat(nsp) != formatInstall || !strings.HasSuffix(fileFormat(nup), "nup") {
		t.Fatal("fileFormat of the parts of split files")
	}
}
