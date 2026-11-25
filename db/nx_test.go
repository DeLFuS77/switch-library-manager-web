package db

import (
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/testnx"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// otherFormatNx is a .nx of the made-up game of otherFormatNup, with its update and its DLC.
func otherFormatNx(name string) []byte {
	return testnx.Build(testnx.Game{Id: 0x0400AAAA00010000, Platform: 2, Title: name, NacpName: name,
		Snapshots: []testnx.Snapshot{
			{Kind: testnx.Base},
			{Kind: testnx.Update, Version: 393216},
			{Kind: testnx.Dlc, Addon: 0x0400AAAA00011001},
		}})
}

// A .nx is read into the Library like a NUP: the game, its update and its DLC, by their IDs.
func TestScanReadsNxFiles(t *testing.T) {
	localDB, _ := scanOtherFormat(t, map[string][]byte{"Made-up Game.nx": otherFormatNx("Made-up Game")})
	game := localDB.TitlesMap["0400aaaa00010"]
	if game == nil || !game.BaseExist || len(game.Dlc) != 1 || game.LatestUpdate != 393216 {
		t.Fatalf("the game: %#v (skipped %v)", game, localDB.Skipped)
	}
	if game.File.Metadata == nil || game.File.Metadata.Ncap == nil {
		t.Fatalf("no NACP: %#v", game.File.Metadata)
	}
}

// A .nx, a NUP and an NSZ of a game are three formats: none is a duplicate or an older version of
// another, and copies of the same format still are, whatever order they are found in.
func TestNxNupAndNszOfAGameAreNoDuplicates(t *testing.T) {
	for _, names := range [][2]string{{"a game.nx", "z game.nup"}, {"z game.nx", "a game.nup"}} {
		files := map[string][]byte{
			names[0]: otherFormatNx("Made-up Game"),
			names[1]: otherFormatNup("Made-up Game"),
			"m game copy.nx":                              otherFormatNx("Made-up Game, a copy"),
			"Made-up Game [0400AAAA00010000][v0].nsz":     make([]byte, 300),
			"Made-up Game DLC [0400AAAA00011001][v0].nsz": make([]byte, 20),
		}
		localDB, _ := scanOtherFormat(t, files)
		if len(localDB.TitlesMap) != 1 {
			t.Fatalf("%v: one game in the Library: %v", names, localDB.TitlesMap)
		}
		duplicates := 0
		for name, reason := range skippedByName(localDB) {
			if reason.ReasonCode != REASON_DUPLICATE && reason.ReasonCode != REASON_OLD_UPDATE {
				continue
			}
			kept := KeptBy(reason.ReasonText)
			if fileFormat(name) != fileFormat(kept) {
				t.Fatalf("%v: %s is a duplicate of a file of another format (%s): %+v", names, name, kept, reason)
			}
			if !switchfs.IsNxFile(name) {
				t.Fatalf("%v: only the copy of the .nx is a duplicate: %s %+v", names, name, reason)
			}
			duplicates++
		}
		if duplicates != 1 {
			t.Fatalf("%v: the two .nx files are duplicates of each other once: %+v", names, skippedByName(localDB))
		}
	}
}

func TestOtherFormat(t *testing.T) {
	for _, c := range []struct {
		a, b  string
		other bool
	}{
		{"a.nx", "b.NX", false}, {"a.nx", "b.nup", true}, {"a.nx", "b.nsp", true}, {"a.nup", "b.xcz", true},
		{"a.nsp", "b.xci", false}, {"a.nsz", "b.nsp", false},
	} {
		if OtherFormat(c.a, c.b) != c.other {
			t.Errorf("OtherFormat(%s, %s) = %v", c.a, c.b, !c.other)
		}
	}
}
