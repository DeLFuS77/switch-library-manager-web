package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/testnup"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// otherFormatNup is a NUP of a made-up game with its update and a DLC, as a dump of the Switch 2
// holds them; name makes NUP files of different sizes.
func otherFormatNup(name string) []byte {
	return testnup.Build([]testnup.Package{
		{TitleId: 0x0400AAAA00010000, Kind: testnup.Base, Meta: []testnup.Record{{Type: testnup.TitleName, Payload: []byte(name)}}},
		{TitleId: 0x0400AAAA00010000, Kind: testnup.Update, Version: 393216},
		{TitleId: 0x0400AAAA00011001, Kind: testnup.Addon},
	})
}

// scanOtherFormat scans a folder with the files given, by name: the NUP files are real ones, the
// others are read by their names (no keys are loaded).
func scanOtherFormat(t *testing.T, files map[string][]byte) (*LocalSwitchFilesDB, string) {
	t.Helper()
	if _, err := settings.InitSwitchKeys(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(folder, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := NewLocalSwitchDBManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	localDB, err := manager.CreateLocalSwitchFilesDB(nil, t.TempDir(), []string{folder}, nil, true, true)
	if err != nil {
		t.Fatal(err)
	}
	return localDB, folder
}

func skippedByName(localDB *LocalSwitchFilesDB) map[string]SkippedFile {
	byName := map[string]SkippedFile{}
	for file, reason := range localDB.Skipped {
		byName[file.FileName] = reason
	}
	return byName
}

// A NUP holds the game decrypted, for emulators, an NSZ or an NSP holds it to be installed: the
// same game in both is no duplicate. Neither the game, nor its update, nor its DLC of the NUP
// makes a separate file a duplicate or an older version, and the reverse; the copies of the same
// format are still duplicates of each other, the Library has the game once.
func TestNupAndNszOfAGameAreNoDuplicates(t *testing.T) {
	// both orders: the NUP found first and last
	for _, nupName := range []string{"a game.nup", "z game.nup"} {
		files := map[string][]byte{
			nupName: otherFormatNup("Made-up Game"),
			"Made-up Game [0400AAAA00010000][v0].nsz":          make([]byte, 300),
			"Made-up Game [0400AAAA00010800][v393216].nsz":     make([]byte, 40),
			"x [0400AAAA00010800][v393216].nsp":                make([]byte, 50),
			"Made-up Game DLC [0400AAAA00011001][v0].nsz":      make([]byte, 20),
			"Made-up Game Old [0400AAAA00010800][v196608].nsz": make([]byte, 30),
		}
		localDB, _ := scanOtherFormat(t, files)
		if len(localDB.TitlesMap) != 1 {
			t.Fatalf("%s: one game in the Library: %v", nupName, localDB.TitlesMap)
		}
		game := localDB.TitlesMap["0400aaaa00010"]
		if game == nil || !game.BaseExist || len(game.Dlc) != 1 {
			t.Fatalf("%s: the game: %#v", nupName, game)
		}
		byName := skippedByName(localDB)
		duplicates := 0
		for name, reason := range byName {
			if reason.ReasonCode == REASON_DUPLICATE {
				duplicates++
				if switchfs.IsNupFile(name) || switchfs.IsNupFile(KeptBy(reason.ReasonText)) {
					t.Fatalf("%s: a duplicate because of the NUP: %s %+v", nupName, name, reason)
				}
			}
			if reason.ReasonCode == REASON_OLD_UPDATE && (switchfs.IsNupFile(name) || switchfs.IsNupFile(KeptBy(reason.ReasonText))) {
				t.Fatalf("%s: an older version because of the NUP: %s %+v", nupName, name, reason)
			}
		}
		// the update NSZ and NSP of the same version are duplicates of each other, as always
		nsz, nsp := byName["Made-up Game [0400AAAA00010800][v393216].nsz"], byName["x [0400AAAA00010800][v393216].nsp"]
		if (nsz.ReasonCode == REASON_DUPLICATE) == (nsp.ReasonCode == REASON_DUPLICATE) || duplicates != 1 {
			t.Fatalf("%s: one of the update NSZ and NSP is the duplicate of the other: %+v", nupName, byName)
		}
		// the game and the DLC: one copy in the Library, the other kept next to it, in the other
		// format (or listed as read by its name only, keys of other tests loaded)
		for slot, pair := range map[string][2]string{
			game.File.ExtendedInfo.FileName:                    {nupName, "Made-up Game [0400AAAA00010000][v0].nsz"},
			game.Dlc["0400aaaa00011001"].ExtendedInfo.FileName: {nupName, "Made-up Game DLC [0400AAAA00011001][v0].nsz"},
		} {
			if slot != pair[0] && slot != pair[1] {
				t.Fatalf("%s: the Library keeps one of %v: %s", nupName, pair, slot)
			}
			for _, name := range pair {
				reason, listed := byName[name]
				if name == slot || !listed {
					continue
				}
				switch reason.ReasonCode {
				case REASON_OTHER_FORMAT:
					if filepath.Base(KeptBy(reason.ReasonText)) != slot || !KeptInOtherFormat(ExtendedFileInfo{FileName: name}, reason) {
						t.Fatalf("%s: %s names the copy of the Library: %+v", nupName, name, reason)
					}
				case REASON_FILENAME_FALLBACK:
				default:
					t.Fatalf("%s: %s is kept: %+v", nupName, name, reason)
				}
			}
		}
		// the older update NSZ is older than the NSZ of the update, never than the NUP alone
		if old := byName["Made-up Game Old [0400AAAA00010800][v196608].nsz"]; old.ReasonCode == REASON_DUPLICATE {
			t.Fatalf("%s: the older update: %+v", nupName, old)
		}
	}
}

// Two NUP files, or two NSZ, of the same game are still duplicates of each other, with a NUP and
// an NSZ of it found too.
func TestCopiesOfTheSameFormatAreStillDuplicates(t *testing.T) {
	for _, names := range [][2]string{{"a.nup", "b copy.nup"}, {"b copy.nup", "a.nup"}} {
		files := map[string][]byte{
			names[0]: otherFormatNup("Made-up Game"),
			names[1]: otherFormatNup("Made-up Game, a longer name"),
			"Made-up Game [0400AAAA00010000][v0].nsz":     make([]byte, 300),
			"Made-up Game [0400AAAA00010000][v0] (1).nsz": make([]byte, 310),
		}
		localDB, _ := scanOtherFormat(t, files)
		byName := skippedByName(localDB)
		nups, nszs := 0, 0
		for name, reason := range byName {
			if reason.ReasonCode != REASON_DUPLICATE {
				continue
			}
			kept := KeptBy(reason.ReasonText)
			if switchfs.IsNupFile(name) != switchfs.IsNupFile(kept) {
				t.Fatalf("a duplicate of the other format: %s %+v", name, reason)
			}
			if KeptInOtherFormat(ExtendedFileInfo{FileName: name}, reason) {
				t.Fatalf("a real duplicate can be freed: %s %+v", name, reason)
			}
			if switchfs.IsNupFile(name) {
				nups++
			} else {
				nszs++
			}
		}
		// the NUP that is the duplicate holds the game, its update and its DLC: one reason
		if nups != 1 || nszs != 1 {
			t.Fatalf("%v: one NUP and one NSZ are duplicates: %+v", names, byName)
		}
		if len(localDB.TitlesMap) != 1 {
			t.Fatalf("one game in the Library: %v", localDB.TitlesMap)
		}
	}
}

// The update NSZ and NSP of a game found with no NUP, and two NSZ of the game, stay duplicates
// (nothing changes without a NUP).
func TestDuplicatesWithoutNupAreUnchanged(t *testing.T) {
	localDB, _ := scanOtherFormat(t, map[string][]byte{
		"Made-up Game [0400AAAA00010000][v0].nsz":      make([]byte, 300),
		"Made-up Game [0400AAAA00010000][v0] (1).nsz":  make([]byte, 310),
		"Made-up Game [0400AAAA00010800][v393216].nsz": make([]byte, 40),
		"x [0400AAAA00010800][v393216].nsp":            make([]byte, 50),
	})
	duplicates := 0
	for _, reason := range localDB.Skipped {
		if reason.ReasonCode == REASON_OTHER_FORMAT {
			t.Fatalf("no other format without a NUP: %+v", localDB.Skipped)
		}
		if reason.ReasonCode == REASON_DUPLICATE {
			duplicates++
		}
	}
	if duplicates != 2 {
		t.Fatalf("the base and the update copies: %+v", localDB.Skipped)
	}
}

// otherFormatFile is a file of the folder /games of a test of the scan, with what it holds:
// title IDs and versions.
type otherFormatFile struct {
	name     string
	size     int64
	contents map[string]int
}

// addOtherFormatFiles puts the files in a library one by one, in the order given, as the scan
// does once it has read them.
func addOtherFormatFiles(t *testing.T, files []otherFormatFile) (*libraryScan, map[string]*SwitchGameFiles) {
	titles := map[string]*SwitchGameFiles{}
	scan := newLibraryScan(&LocalSwitchDBManager{}, nil, t.TempDir(), titles, map[ExtendedFileInfo]SkippedFile{})
	scan.keepDuplicate = settings.KEEP_AUTO
	for _, file := range files {
		metadata := map[string]*switchfs.ContentMetaAttributes{}
		for id, version := range file.contents {
			metadata[id] = &switchfs.ContentMetaAttributes{TitleId: id, Version: version}
		}
		scan.addFile(ExtendedFileInfo{FileName: file.name, BaseFolder: "/games", Size: file.size}, false, metadataResult{metadata: metadata})
	}
	resolveKeptChains(scan.skipped)
	return scan, titles
}

// The cases of a NUP that holds a game and its update next to the NSZ of the game, the NSZ of
// the update and the NSP of the same update, its DLC in both formats, in several orders: no file
// is a duplicate or an older version because of the NUP, each copy of the other format is kept
// next to the copy of the Library and never offered to be freed, and the update NSZ and NSP of
// the same version are duplicates of each other, as always.
func TestScanKeepsTheOtherFormatApart(t *testing.T) {
	nup := otherFormatFile{"Made-up Game.nup", 900, map[string]int{"0400aaaa00010000": 0, "0400aaaa00010800": 393216, "0400aaaa00011001": 0}}
	files := []otherFormatFile{
		nup,
		{"Made-up Game [0400AAAA00010000][v0].nsz", 300, map[string]int{"0400aaaa00010000": 0}},
		{"Made-up Game [0400AAAA00010800][v393216].nsz", 40, map[string]int{"0400aaaa00010800": 393216}},
		{"x_v393216.nsp", 50, map[string]int{"0400aaaa00010800": 393216}},
		{"Made-up Game DLC [0400AAAA00011001][v0].nsz", 20, map[string]int{"0400aaaa00011001": 0}},
		{"Made-up Game DLC [0400AAAA00011001][v65536].nsp", 25, map[string]int{"0400aaaa00011001": 65536}},
		{"Made-up Game [0400AAAA00010800][v196608].nsz", 30, map[string]int{"0400aaaa00010800": 196608}},
	}
	orders := [][]int{{0, 1, 2, 3, 4, 5, 6}, {1, 2, 3, 4, 5, 6, 0}, {6, 5, 4, 3, 2, 1, 0}, {2, 0, 3, 1, 5, 4, 6}, {3, 2, 6, 0, 4, 1, 5}}
	for _, order := range orders {
		ordered := []otherFormatFile{}
		for _, i := range order {
			ordered = append(ordered, files[i])
		}
		scan, titles := addOtherFormatFiles(t, ordered)
		if len(titles) != 1 {
			t.Fatalf("%v: the game once in the Library: %v", order, titles)
		}
		game := titles["0400aaaa00010"]
		if game == nil || !game.BaseExist || game.LatestUpdate != 393216 || len(game.Dlc) != 1 {
			t.Fatalf("%v: the game: %#v", order, game)
		}
		duplicates, otherFormat := map[string]SkippedFile{}, map[string]SkippedFile{}
		for file, reason := range scan.skipped {
			kept := KeptBy(reason.ReasonText)
			switch reason.ReasonCode {
			case REASON_DUPLICATE, REASON_OLD_UPDATE:
				if OtherFormat(file.FileName, kept) {
					t.Fatalf("%v: %s listed for the other format: %+v", order, file.FileName, reason)
				}
				if reason.ReasonCode == REASON_DUPLICATE {
					duplicates[file.FileName] = reason
				}
			case REASON_OTHER_FORMAT:
				otherFormat[file.FileName] = reason
				if !OtherFormat(file.FileName, kept) || !KeptInOtherFormat(file, reason) || scan.flagged(file) {
					t.Fatalf("%v: %s is kept in the other format: %+v", order, file.FileName, reason)
				}
			default:
				t.Fatalf("%v: %s: %+v", order, file.FileName, reason)
			}
			if switchfs.IsNupFile(file.FileName) && reason.ReasonCode != REASON_OTHER_FORMAT {
				t.Fatalf("%v: the NUP is listed: %+v", order, reason)
			}
		}
		// one of the update NSZ and NSP of the same version is the duplicate of the other
		_, nsz := duplicates["Made-up Game [0400AAAA00010800][v393216].nsz"]
		_, nsp := duplicates["x_v393216.nsp"]
		if len(duplicates) != 1 || nsz == nsp {
			t.Fatalf("%v: the duplicates: %+v", order, duplicates)
		}
		if len(otherFormat) == 0 {
			t.Fatalf("%v: nothing kept in the other format: %+v", order, scan.skipped)
		}
		// the copies of the Library of the game, the update and the DLC are not listed (the NUP
		// holds all three: it may be listed for one while it is the copy of another)
		for _, held := range []SwitchFileInfo{game.File, game.Updates[393216], game.Dlc["0400aaaa00011001"]} {
			if reason, listed := scan.skipped[held.ExtendedInfo]; listed && !switchfs.IsNupFile(held.ExtendedInfo.FileName) {
				t.Fatalf("%v: the copy of the Library %s is listed: %+v", order, held.ExtendedInfo.FileName, reason)
			}
		}
	}
}

// With the copy of the Library an NSZ, two NUP files of the game are still duplicates of each
// other, and the same with the NUP kept and two NSZ: each competes with its own format only.
func TestScanFindsTheDuplicatesOfEachFormat(t *testing.T) {
	cases := [][]otherFormatFile{
		{
			{"Made-up Game [0400AAAA00010000][v0].nsz", 300, map[string]int{"0400aaaa00010000": 0}},
			{"one.nup", 900, map[string]int{"0400aaaa00010000": 0}},
			{"two.nup", 800, map[string]int{"0400aaaa00010000": 0}},
		},
		{
			{"one.nup", 900, map[string]int{"0400aaaa00010000": 0}},
			{"Made-up Game [0400AAAA00010000][v0].nsz", 300, map[string]int{"0400aaaa00010000": 0}},
			{"Made-up Game [0400AAAA00010000][v0] (1).nsz", 200, map[string]int{"0400aaaa00010000": 0}},
		},
		{
			{"Made-up Game [0400AAAA00010800][v65536].nsz", 30, map[string]int{"0400aaaa00010800": 65536}},
			{"one.nup", 900, map[string]int{"0400aaaa00010800": 65536}},
			{"two.nup", 800, map[string]int{"0400aaaa00010800": 65536}},
		},
		{
			{"Made-up DLC [0400AAAA00011001][v0].nsz", 30, map[string]int{"0400aaaa00011001": 0}},
			{"one.nup", 900, map[string]int{"0400aaaa00011001": 0}},
			{"two.nup", 800, map[string]int{"0400aaaa00011001": 0}},
		},
	}
	for i, files := range cases {
		scan, _ := addOtherFormatFiles(t, files)
		duplicates, otherFormat := 0, 0
		for file, reason := range scan.skipped {
			switch reason.ReasonCode {
			case REASON_DUPLICATE:
				duplicates++
				if OtherFormat(file.FileName, KeptBy(reason.ReasonText)) || KeptInOtherFormat(file, reason) {
					t.Fatalf("case %d: the duplicate of its own format: %+v", i, reason)
				}
			case REASON_OTHER_FORMAT:
				otherFormat++
			}
		}
		if duplicates != 1 || otherFormat != 1 {
			t.Fatalf("case %d: one duplicate, one copy of the other format: %+v", i, scan.skipped)
		}
	}
}
