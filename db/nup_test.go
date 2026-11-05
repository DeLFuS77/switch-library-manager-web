package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/testnup"
)

// A NUP holds a decrypted game with its updates and DLC: the scan reads what it holds without
// the keys, as one file with several titles, and a damaged one is listed as an issue.
func TestScanReadsNupFiles(t *testing.T) {
	romDir := t.TempDir()
	game := testnup.Build([]testnup.Package{
		{TitleId: 0x040074A01BF12000, Kind: testnup.Base, Meta: []testnup.Record{
			{Type: testnup.TitleName, Payload: []byte("Bananza")},
			{Type: testnup.Icon, Payload: testnup.Jpeg},
		}},
		{TitleId: 0x040074A01BF12000, Kind: testnup.Update, Version: 65536},
		{TitleId: 0x040074A01BF13001, Kind: testnup.Addon},
	})
	if err := os.WriteFile(filepath.Join(romDir, "dump.nup"), game, 0o644); err != nil {
		t.Fatal(err)
	}
	broken := append([]byte{}, game...)
	broken[0x90]++
	if err := os.WriteFile(filepath.Join(romDir, "Other [0400C3F00006E000][v0].nup"), broken, 0o644); err != nil {
		t.Fatal(err)
	}

	manager, err := NewLocalSwitchDBManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	dataFolder := t.TempDir()
	for scan := 0; scan < 2; scan++ {
		// the second scan finds what the first read in the cache
		localDB, err := manager.CreateLocalSwitchFilesDB(nil, dataFolder, []string{romDir}, nil, true, scan == 0)
		if err != nil {
			t.Fatal(err)
		}
		title := localDB.TitlesMap["040074a01bf12"]
		if title == nil || !title.BaseExist || !title.MultiContent || title.File.ExtendedInfo.FileName != "dump.nup" {
			t.Fatalf("scan %d: the game of the NUP: %#v", scan, title)
		}
		if name := title.File.Metadata.Ncap.TitleName["AmericanEnglish"].Title; name != "Bananza" {
			t.Errorf("scan %d: name %q", scan, name)
		}
		if update, ok := title.Updates[65536]; !ok || update.Metadata.TitleId != "040074a01bf12800" || title.LatestUpdate != 65536 {
			t.Errorf("scan %d: update %#v", scan, title.Updates)
		}
		if _, ok := title.Dlc["040074a01bf13001"]; !ok {
			t.Errorf("scan %d: DLC %#v", scan, title.Dlc)
		}

		// the damaged one is still listed by its name, and told as an issue
		other := localDB.TitlesMap["0400c3f00006e"]
		if other == nil || !other.BaseExist {
			t.Fatalf("scan %d: the damaged NUP is identified by its name: %#v", scan, other)
		}
		reason := localDB.Skipped[other.File.ExtendedInfo]
		if reason.ReasonCode != REASON_FILENAME_FALLBACK || !strings.Contains(reason.ReasonText, "NUP") {
			t.Errorf("scan %d: reason %#v", scan, reason)
		}
	}
	if !IsGameFileType(".NUP") {
		t.Error("a NUP is a game")
	}

	// a NUP edited in place, its size the same (a name of the same length): read again
	edited := testnup.Build([]testnup.Package{
		{TitleId: 0x040074A01BF12000, Kind: testnup.Base, Meta: []testnup.Record{
			{Type: testnup.TitleName, Payload: []byte("Banana!")},
			{Type: testnup.Icon, Payload: testnup.Jpeg},
		}},
		{TitleId: 0x040074A01BF12000, Kind: testnup.Update, Version: 65536},
		{TitleId: 0x040074A01BF13001, Kind: testnup.Addon},
	})
	if len(edited) != len(game) {
		t.Fatalf("the edited NUP has another size: %d and %d", len(edited), len(game))
	}
	path := filepath.Join(romDir, "dump.nup")
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	localDB, err := manager.CreateLocalSwitchFilesDB(nil, dataFolder, []string{romDir}, nil, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if name := localDB.TitlesMap["040074a01bf12"].File.Metadata.Ncap.TitleName["AmericanEnglish"].Title; name != "Banana!" {
		t.Errorf("the NUP edited in place: name %q", name)
	}
}
