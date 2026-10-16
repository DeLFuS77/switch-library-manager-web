package web

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// Of an NSP and its NSZ found as duplicates, the cleanup keeps the NSZ once it was checked
// and holds the same contents, whichever the scan found first.
func TestCleanupKeepsTheCheckedCompressedCopy(t *testing.T) {
	web := newTestWeb(t)
	dir := t.TempDir()
	nspPath := filepath.Join(dir, "Game [0100000000010000][v0].nsp")
	nszPath := filepath.Join(dir, "Game [0100000000010000][v0].nsz")
	os.WriteFile(nspPath, testPfs0("game.nca"), 0644)
	os.WriteFile(nszPath, testPfs0("game.ncz"), 0644)
	nspInfo, _ := os.Stat(nspPath)
	nszInfo, _ := os.Stat(nszPath)
	nsp := db.ExtendedFileInfo{FileName: filepath.Base(nspPath), BaseFolder: dir, Size: nspInfo.Size()}
	nsz := db.ExtendedFileInfo{FileName: filepath.Base(nszPath), BaseFolder: dir, Size: nszInfo.Size()}
	localDB := &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{
			"0100000000010": {BaseExist: true, File: db.SwitchFileInfo{ExtendedInfo: nsp, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000010000"}},
				Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}},
		},
		Skipped: map[db.ExtendedFileInfo]db.SkippedFile{
			nsz: {ReasonCode: db.REASON_DUPLICATE, ReasonText: "duplicate base file (" + nspPath + ")"},
		},
	}
	options := settings.OrganizeOptions{DeleteDuplicateFiles: true}
	deleted := func() string {
		ops := process.DeleteOldUpdates("", web.preferCompressed(localDB), options, true, nil, web.cleanupRefusal)
		for _, op := range ops {
			if op.Kind == process.OP_DELETE && op.Error == "" {
				return op.From
			}
		}
		return ""
	}

	// not checked yet: the NSZ is not preferred, and the NSP kept was not checked either
	if got := deleted(); got != "" {
		t.Fatalf("nothing is deleted before a check: %q", got)
	}
	// the NSZ checked and sound: it stays, the NSP goes
	web.verifications().set(nszPath, verifyRecord{Size: nszInfo.Size(), ModTime: nszInfo.ModTime().UnixNano(), OK: true, Checked: time.Now(), Checker: verifyChecker})
	if got := deleted(); got != nspPath {
		t.Fatalf("the NSP is deleted, not the checked NSZ: %q", got)
	}
	// the library itself is not changed
	if localDB.TitlesMap["0100000000010"].File.ExtendedInfo != nsp {
		t.Fatal("the library is left as it is")
	}
	// an NSZ that lacks part of the NSP is not preferred
	os.WriteFile(nspPath, testPfs0("game.nca", "update.nca"), 0644)
	nspInfo, _ = os.Stat(nspPath)
	localDB.TitlesMap["0100000000010"].File.ExtendedInfo.Size = nspInfo.Size()
	if got := deleted(); got == nspPath {
		t.Fatal("an NSZ holding less than the NSP does not replace it")
	}
}
