package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/testnsp"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

func TestCleanupFollowsTheRulesOfTheSpacePage(t *testing.T) {
	web := newTestWeb(t)
	folder := t.TempDir()
	kept := filepath.Join(folder, "Game.nsp")
	os.WriteFile(kept, []byte("game"), 0644)
	info, _ := os.Stat(kept)
	duplicate := db.SkippedFile{ReasonCode: db.REASON_DUPLICATE, ReasonText: "duplicate base file (" + kept + ")"}

	if reason := web.cleanupRefusal(db.ExtendedFileInfo{}, duplicate); reason != NOTE_SPACE_VERIFY_KEPT {
		t.Fatalf("the copy that stays was never checked: %q", reason)
	}
	web.verifications().set(kept, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: true, Checker: verifyChecker})
	if reason := web.cleanupRefusal(db.ExtendedFileInfo{}, duplicate); reason != "" {
		t.Fatalf("the copy that stays is sound: %q", reason)
	}

	old := db.SkippedFile{ReasonCode: db.REASON_OLD_UPDATE, ReasonText: "old update file, newer update exist locally (" + kept + ")"}
	if reason := web.cleanupRefusal(db.ExtendedFileInfo{}, old); reason != "" {
		t.Fatalf("the newer update is sound: %q", reason)
	}
	web.verifications().set(kept, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: false, Checker: verifyChecker})
	if reason := web.cleanupRefusal(db.ExtendedFileInfo{}, old); reason != NOTE_SPACE_NEWER_DAMAGED {
		t.Fatalf("the newer update is damaged: %q", reason)
	}
}

func TestOrganizeAndFileTasksNeverRunTogether(t *testing.T) {
	web := newTestWeb(t)
	web.organizing.Store(true)
	if web.runFileTask(TASK_COMPRESS, nil, 1, nil) {
		t.Fatal("no compression while files are organized")
	}
	if web.startVerificationOf(nil, verifyRecheckChanged, TRIGGER_MANUAL) {
		t.Fatal("no check while files are organized")
	}
	web.organizing.Store(false)
	web.compressor.cancel = func() {}
	if _, err := web.runOrganizeAction(ORGANIZE_ACTION_CLEANUP, TRIGGER_MANUAL); err != errOrganizeBusy {
		t.Fatalf("no organizing while a file task runs: %v", err)
	}
	web.compressor.cancel = nil
}

// A duplicate with several titles (a cartridge with its update) goes only when the copies kept
// of all of them are sound, not only the one its reason names.
func TestCleanupChecksTheCopiesKeptOfEveryTitleOfADuplicate(t *testing.T) {
	web := newTestWeb(t)
	testnsp.WriteKeys(web.dataFolder)
	if _, err := settings.InitSwitchKeys(web.dataFolder); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { settings.InitSwitchKeys(t.TempDir()) })

	folder := t.TempDir()
	both := filepath.Join(folder, "Game with its update.nsp")
	if _, err := testnsp.WriteMultiTitleNsp(both, []testnsp.Title{
		{Id: 0x0100000000010000, Type: switchfs.ContentMetaType_Application, Content: "the game "},
		{Id: 0x0100000000010800, Type: switchfs.ContentMetaType_Patch, Version: 65536, Content: "its update "},
	}); err != nil {
		t.Fatal(err)
	}
	game, update := filepath.Join(folder, "Game.nsz"), filepath.Join(folder, "Update.nsz")
	os.WriteFile(game, []byte("game"), 0644)
	os.WriteFile(update, []byte("update"), 0644)
	file := func(path string) db.SwitchFileInfo {
		return db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: filepath.Base(path), BaseFolder: folder}}
	}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": {BaseExist: true, File: file(game), Updates: map[int]db.SwitchFileInfo{65536: file(update)}, LatestUpdate: 65536},
	}})
	sound := func(path string) {
		info, _ := os.Stat(path)
		web.verifications().set(path, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: true, Checker: verifyChecker})
	}
	duplicate := db.SkippedFile{ReasonCode: db.REASON_DUPLICATE, ReasonText: "duplicate base file (" + game + ")"}
	info := db.ExtendedFileInfo{FileName: filepath.Base(both), BaseFolder: folder}

	sound(game)
	if reason := web.cleanupRefusal(info, duplicate); reason != NOTE_SPACE_VERIFY_KEPT {
		t.Fatalf("the update kept was never checked: %q", reason)
	}
	sound(update)
	if reason := web.cleanupRefusal(info, duplicate); reason != "" {
		t.Fatalf("both copies kept are sound: %q", reason)
	}
}
