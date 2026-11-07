package web

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/internal/testnup"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// The check of the files goes through the NUP files too: every file of the NUP against its
// SHA-256, without keys and without signatures, which decrypted files do not have.
func TestVerifyChecksNupFiles(t *testing.T) {
	web, _ := compressWeb(t)
	web.HandleVerify()
	web.HandleTasks()

	packages := []testnup.Package{
		{TitleId: 0x0400486024DEC000, Kind: testnup.Base, Version: 65536, Files: []testnup.File{
			{Path: "exefs/main", Data: bytes.Repeat([]byte("code "), 2000)},
			{Path: "romfs/Data/stage.bin", Data: bytes.Repeat([]byte("stage "), 50000), Zstd: true}}},
		{TitleId: 0x0400486024DEC000, Kind: testnup.Update, Version: 131072, Files: []testnup.File{
			{Path: "romfs/Data/patch.bin", Data: []byte("patched")}}},
	}
	nup := testnup.Build(packages)
	folder := t.TempDir()
	path := filepath.Join(folder, "Made-up Quest [0400486024DEC000][v131072].nup")
	if err := os.WriteFile(path, nup, 0o644); err != nil {
		t.Fatal(err)
	}
	_, localDB := web.state.get()
	localDB.TitlesMap["0400486024dec"] = &db.SwitchGameFiles{BaseExist: true, MultiContent: true,
		File: db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: filepath.Base(path), BaseFolder: folder, Size: int64(len(nup))},
			Metadata: &switchfs.ContentMetaAttributes{TitleId: "0400486024dec000", Version: 65536, Type: "BASE"}},
		Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}}

	var mutex sync.Mutex
	signed := []string{}
	original := checkFileSignatures
	t.Cleanup(func() { checkFileSignatures = original })
	checkFileSignatures = func(file string) (switchfs.SignatureCheck, error) {
		mutex.Lock()
		signed = append(signed, file)
		mutex.Unlock()
		return original(file)
	}

	// a check of the signatures alone has nothing to do with a NUP; one of the hashes has
	if !web.nothingToVerify([]string{path}, tasklog.VERIFY_LEVEL_SIGNATURE) || web.nothingToVerify([]string{path}, tasklog.VERIFY_LEVEL_HASH) {
		t.Fatal("a NUP has its hashes checked, not its signatures")
	}
	web.startVerificationAt(tasklog.VERIFY_LEVEL_BOTH, nil, verifyRecheckAll, tasklog.TRIGGER_MANUAL)
	task := waitForTask(t, web, tasklog.TASK_VERIFY)
	// the tile of the NUP by its name: two workers finish the two files in any order
	nupTile := -1
	for i, tile := range task.Tiles {
		if tile.Name == filepath.Base(path) {
			nupTile = i
		}
	}
	if task.Files != 2 || task.Damaged != 0 || len(task.Tiles) != 2 || nupTile < 0 || task.Tiles[nupTile].Failed {
		t.Fatalf("the NSP and the NUP of the library: %+v", task)
	}
	// the NUP is whole, but its game has no main.npdm: Eglantine and Lichii cannot start it, told
	// on a line of its own, its tile sound
	if task.Status != tasklog.TASK_WARNING || len(task.Warnings) != 1 || task.Warnings[0].Text != NOTE_VERIFY_EMULATORS_NO_START ||
		task.Warnings[0].Detail != filepath.Base(path)+": the ExeFS of the game lacks: main.npdm" || task.Tiles[nupTile].Modified || task.Tiles[nupTile].Skipped {
		t.Fatalf("the emulators: %+v", task)
	}
	for _, file := range signed {
		if switchfs.IsNupFile(file) {
			t.Fatal("the signatures of a NUP were asked for")
		}
	}
	info, _ := os.Stat(path)
	if record, ok := web.verifications().current(path, info); !ok || !record.OK {
		t.Fatalf("the NUP is sound: %+v %v", record, ok)
	}
	if !web.nothingToVerify([]string{path}, tasklog.VERIFY_LEVEL_BOTH) {
		t.Fatal("nothing new to check in the NUP")
	}

	// a byte of the update changed
	offset, _ := testnup.Extent(nup, packages, 1, "romfs/Data/patch.bin")
	nup[offset] ^= 0x20
	if err := os.WriteFile(path, nup, 0o644); err != nil {
		t.Fatal(err)
	}
	web.startVerificationAt(tasklog.VERIFY_LEVEL_HASH, []string{path}, verifyRecheckAll, tasklog.TRIGGER_MANUAL)
	task = waitForTask(t, web, tasklog.TASK_VERIFY)
	info, _ = os.Stat(path)
	record, ok := web.verifications().current(path, info)
	if task.Damaged != 1 || !ok || record.OK || !strings.Contains(record.Reason, "update v131072/romfs/Data/patch.bin: its SHA-256 does not match") {
		t.Fatalf("the damaged update: %+v, %+v", task, record)
	}
}

// A NUP made of a dump that lost bytes of its code has hashes that match, as the files were
// packed: the check reads the code modules by their headers, and tells the game to be dumped
// again. A NUP found sound before the code was read is checked again.
func TestVerifyTellsTheDamagedCodeOfANup(t *testing.T) {
	web, _ := compressWeb(t)
	web.HandleVerify()
	web.HandleTasks()

	module := testnup.Nso(bytes.Repeat([]byte("text "), 3000), bytes.Repeat([]byte("ro "), 1000), bytes.Repeat([]byte("data "), 500))
	cut := append(append([]byte{}, module[:0x400]...), module[0x400+980:]...)
	packages := []testnup.Package{
		{TitleId: 0x0400A3A01EE4C000, Kind: testnup.Base, Version: 0, Files: []testnup.File{
			{Path: "exefs/main", Data: module, Zstd: true},
			{Path: "romfs/Data/stage.bin", Data: bytes.Repeat([]byte("stage "), 5000), Zstd: true}}},
		{TitleId: 0x0400A3A01EE4C000, Kind: testnup.Update, Version: 655360, Files: []testnup.File{
			{Path: "exefs/main", Data: cut, Zstd: true}}},
	}
	nup := testnup.Build(packages)
	folder := t.TempDir()
	path := filepath.Join(folder, "Made-up Island [0400A3A01EE4C000][v655360].nup")
	if err := os.WriteFile(path, nup, 0o644); err != nil {
		t.Fatal(err)
	}
	_, localDB := web.state.get()
	localDB.TitlesMap["0400a3a01ee4c"] = &db.SwitchGameFiles{BaseExist: true, MultiContent: true,
		File: db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: filepath.Base(path), BaseFolder: folder, Size: int64(len(nup))},
			Metadata: &switchfs.ContentMetaAttributes{TitleId: "0400a3a01ee4c000", Version: 0, Type: "BASE"}},
		Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}}

	// found sound by the version before, which did not read the code
	info, _ := os.Stat(path)
	store := web.verifications()
	store.set(path, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: true, Checked: time.Now(), Checker: verifyChecker - 1})
	if _, ok := store.current(path, info); ok || store.why(path, info) != VERIFY_WHY_IMPROVED {
		t.Fatal("a NUP found sound before the code was read is checked again")
	}
	if web.nothingToVerify([]string{path}, tasklog.VERIFY_LEVEL_HASH) {
		t.Fatal("the NUP is due")
	}

	web.startVerificationAt(tasklog.VERIFY_LEVEL_HASH, []string{path}, verifyRecheckAll, tasklog.TRIGGER_MANUAL)
	task := waitForTask(t, web, tasklog.TASK_VERIFY)
	if task.Damaged != 1 || len(task.Warnings) != 1 || task.Warnings[0].Text != NOTE_VERIFY_CODE ||
		!strings.Contains(task.Warnings[0].Detail, "update v655360/exefs/main: it is 980 bytes shorter than its header says") {
		t.Fatalf("the damaged code: %+v", task)
	}
	if len(task.Tiles) != 1 || !task.Tiles[0].Failed || task.Tiles[0].Note != NOTE_VERIFY_CODE {
		t.Fatalf("its cover: %+v", task.Tiles)
	}
	record, ok := store.current(path, info)
	if !ok || record.OK || !strings.HasPrefix(record.Reason, "damaged file: "+switchfs.NupCodeDamaged) {
		t.Fatalf("the record: %+v %v", record, ok)
	}
	if cause := issueCause(ISSUE_DAMAGED, record.Reason, "es"); cause != i18n.Translate("es", NOTE_VERIFY_CODE) || cause == NOTE_VERIFY_CODE {
		t.Fatalf("the cause in Issues: %q", cause)
	}
	// other damaged files keep their note
	if damagedNote("game v0/romfs/a.bin: its SHA-256 does not match") != NOTE_VERIFY_DAMAGED {
		t.Fatal("a damaged file")
	}
}

// Sound files other than NUP files keep the results of the version before.
func TestSoundFilesKeepTheirResults(t *testing.T) {
	record := verifyRecord{OK: true, Checker: verifyChecker - 1}
	if record.stale("Game [0100000000010000][v0].nsp") || !record.stale("Game.nup") || !record.stale("GAME.NUP") {
		t.Fatal("only a NUP is checked again")
	}
	record.Checker = verifyChecker
	if record.stale("Game.nup") {
		t.Fatal("a NUP checked by this version stands")
	}
}

// A NUP and an NSP or XCI of a game are never deleted for each other: the NUP holds the game
// decrypted, for emulators, the NSP to install it, and they may not hold the same version. The
// Space page does not offer either, and the cleanup of Organize and of the automations keeps it.
func TestNupAndNspAreNeverDeletedForEachOther(t *testing.T) {
	web, dir := spaceWeb(t)
	write := func(name string, size int) db.ExtendedFileInfo {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		return db.ExtendedFileInfo{FileName: name, BaseFolder: dir, Size: int64(size)}
	}
	kept := filepath.Join(dir, "Other [0100000000020000][v0].nsp")
	nup := write("Other [0100000000020000][v0].nup", 80)
	third := write("Third [0100000000030000][v0].nup", 90)
	bundle := write("Bundle.nsp", 160)
	_, localDB := web.state.get()
	nupReason := db.SkippedFile{ReasonCode: db.REASON_DUPLICATE, ReasonText: "duplicate base file (" + kept + ")"}
	localDB.Skipped[nup] = nupReason
	// a pack whose second game the library keeps only in a NUP
	localDB.Skipped[bundle] = db.SkippedFile{ReasonCode: db.REASON_DUPLICATE, ReasonText: "duplicate base file (" + kept + ")"}
	localDB.TitlesMap["0100000000030"] = &db.SwitchGameFiles{BaseExist: true,
		File:    db.SwitchFileInfo{ExtendedInfo: third, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000030000"}},
		Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}}
	holds(web, filepath.Join(dir, nup.FileName), "0100000000020000")
	holds(web, filepath.Join(dir, bundle.FileName), "0100000000020000", "0100000000030000")
	sound := func(path string) {
		info, _ := os.Stat(path)
		web.verifications().set(path, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: true, Checked: time.Now(), Checker: verifyChecker})
	}
	sound(kept)
	sound(filepath.Join(dir, third.FileName))

	files := map[string]SpaceFile{}
	for _, file := range spaceGroup(web.spaceGroups(), SPACE_DUPLICATES).Files {
		files[filepath.Base(file.Path)] = file
	}
	if _, listed := files[nup.FileName]; listed {
		t.Fatal("the NUP of a game kept in an NSP is no copy to free")
	}
	if copy := files["Other [0100000000020000][v0] (copy).nsp"]; !copy.Deletable {
		t.Fatalf("a copy of the same format still goes: %+v", files)
	}
	if pack := files[bundle.FileName]; pack.Deletable || pack.Note != NOTE_SPACE_OTHER_FORMAT {
		t.Fatalf("the pack holds a game the library keeps only in a NUP: %+v", pack)
	}
	if note := web.cleanupRefusal(nup, nupReason); note != NOTE_SPACE_OTHER_FORMAT {
		t.Fatalf("the cleanup keeps the NUP: %q", note)
	}
	if note := web.cleanupRefusal(bundle, localDB.Skipped[bundle]); note != NOTE_SPACE_OTHER_FORMAT {
		t.Fatalf("the cleanup keeps the pack: %q", note)
	}
}
