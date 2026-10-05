package process

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

const (
	baseID    = "0100e95004039000"
	updateID  = "0100e95004039800"
	dlcOneID  = "0100e9500403a001"
	dlcTwoID  = "0100e9500403a002"
	groupID   = "0100e95004039"
	gameTitle = "Test Game"
)

func metadataForID(id string, version int) *switchfs.ContentMetaAttributes {
	return &switchfs.ContentMetaAttributes{TitleId: id, Version: version}
}

func writeFixture(t *testing.T, path string, contents string) db.ExtendedFileInfo {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
	return db.ExtendedFileInfo{BaseFolder: filepath.Dir(path), FileName: filepath.Base(path), Size: int64(len(contents))}
}

func assertExists(t *testing.T, path string, want bool) {
	t.Helper()
	_, err := os.Stat(path)
	if want && err != nil {
		t.Errorf("expected %s to exist: %v", path, err)
	}
	if !want && err == nil {
		t.Errorf("expected %s not to exist", path)
	}
}

func fixtureLibrary(t *testing.T, source string) (*db.LocalSwitchFilesDB, *db.SwitchTitlesDB) {
	t.Helper()
	local := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		groupID: {
			File:      db.SwitchFileInfo{ExtendedInfo: writeFixture(t, filepath.Join(source, "base.nsp"), "base"), Metadata: metadataForID(baseID, 0)},
			BaseExist: true,
			Updates: map[int]db.SwitchFileInfo{
				65536: {ExtendedInfo: writeFixture(t, filepath.Join(source, "update.nsp"), "update"), Metadata: metadataForID(updateID, 65536)},
			},
			Dlc: map[string]db.SwitchFileInfo{
				dlcOneID: {ExtendedInfo: writeFixture(t, filepath.Join(source, "dlc-one.nsp"), "one"), Metadata: metadataForID(dlcOneID, 0)},
				dlcTwoID: {ExtendedInfo: writeFixture(t, filepath.Join(source, "dlc-two.nsp"), "two"), Metadata: metadataForID(dlcTwoID, 0)},
			},
		},
	}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}}
	remote := &db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		groupID: {Attributes: db.TitleAttributes{Id: "0100E95004039000", Name: gameTitle, Region: "US"}, Dlc: map[string]db.TitleAttributes{
			dlcOneID: {Id: dlcOneID, Name: gameTitle + " - Expansion"},
			dlcTwoID: {Id: dlcTwoID, Name: gameTitle + " - Expansion"},
		}},
	}}
	return local, remote
}

func organizeOptions() settings.OrganizeOptions {
	return settings.OrganizeOptions{
		CreateFolderPerGame: true,
		FolderNameTemplate:  "{TITLE_NAME}",
		RenameFiles:         true,
		FileNameTemplate:    "{TITLE_NAME} ({DLC_NAME})[{TITLE_ID}][{TYPE}][v{VERSION}]",
		UpdatesFolder:       "Updates",
		DlcFolder:           "DLC",
		SwitchSafeFileNames: true,
	}
}

func countKind(ops []Operation, kind string) int {
	n := 0
	for _, op := range ops {
		if op.Kind == kind && op.Error == "" {
			n++
		}
	}
	return n
}

func failedOps(ops []Operation) []Operation {
	failed := []Operation{}
	for _, op := range ops {
		if op.Error != "" {
			failed = append(failed, op)
		}
	}
	return failed
}

func TestOrganizePreviewThenRun(t *testing.T) {
	baseFolder := t.TempDir()
	source := filepath.Join(baseFolder, "incoming")
	local, remote := fixtureLibrary(t, source)
	options := organizeOptions()

	preview, err := OrganizeByFolders(baseFolder, local, remote, options, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if countKind(preview, OP_MOVE) != 4 || len(failedOps(preview)) != 0 {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	// a preview must not touch the disk
	assertExists(t, filepath.Join(source, "base.nsp"), true)
	assertExists(t, filepath.Join(baseFolder, gameTitle), false)

	result, err := OrganizeByFolders(baseFolder, local, remote, options, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(failedOps(result)) != 0 {
		t.Fatalf("unexpected errors: %+v", failedOps(result))
	}

	game := filepath.Join(baseFolder, gameTitle)
	expected := []string{
		filepath.Join(game, "Test Game [0100E95004039000][BASE][v0].nsp"),
		filepath.Join(game, "Updates", "Test Game [0100E95004039800][UPD][v65536].nsp"),
		filepath.Join(game, "DLC", "Test Game (Expansion)[0100E9500403A001][DLC][v0].nsp"),
		// same DLC name, different ID: numbered instead of overwriting the first one
		filepath.Join(game, "DLC", "Test Game (Expansion)[0100E9500403A002][DLC][v0].nsp"),
	}
	for _, path := range expected {
		assertExists(t, path, true)
	}
	assertExists(t, filepath.Join(source, "base.nsp"), false)

	// the preview listed exactly what was done
	for i, op := range preview {
		if op.Kind != result[i].Kind || op.From != result[i].From || op.To != result[i].To {
			t.Fatalf("preview %d (%+v) differs from result (%+v)", i, op, result[i])
		}
	}
}

func TestOrganizeNumbersCollidingDlcNames(t *testing.T) {
	baseFolder := t.TempDir()
	local, remote := fixtureLibrary(t, filepath.Join(baseFolder, "incoming"))
	options := organizeOptions()
	options.FileNameTemplate = "{TITLE_NAME} - {DLC_NAME} [{TYPE}]"

	ops, err := OrganizeByFolders(baseFolder, local, remote, options, false, nil)
	if err != nil || len(failedOps(ops)) != 0 {
		t.Fatalf("err=%v failed=%+v", err, failedOps(ops))
	}
	dlc := filepath.Join(baseFolder, gameTitle, "DLC")
	assertExists(t, filepath.Join(dlc, "Test Game - Expansion [DLC].nsp"), true)
	assertExists(t, filepath.Join(dlc, "Test Game - Expansion [DLC](1).nsp"), true)
}

func TestOrganizeNeverOverwritesFiles(t *testing.T) {
	baseFolder := t.TempDir()
	source := filepath.Join(baseFolder, "incoming")
	local, remote := fixtureLibrary(t, source)
	options := organizeOptions()
	options.RenameFiles = false
	options.UpdatesFolder = ""
	options.DlcFolder = ""

	existing := filepath.Join(baseFolder, gameTitle, "base.nsp")
	writeFixture(t, existing, "another game")

	ops, err := OrganizeByFolders(baseFolder, local, remote, options, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	failed := failedOps(ops)
	if len(failed) != 1 || !strings.Contains(failed[0].Error, "already exists") {
		t.Fatalf("expected one 'already exists' error, got %+v", failed)
	}
	if data, _ := os.ReadFile(existing); string(data) != "another game" {
		t.Fatal("existing file was overwritten")
	}
	assertExists(t, filepath.Join(source, "base.nsp"), true)
}

func TestOrganizeSubfoldersWithoutGameFolders(t *testing.T) {
	baseFolder := t.TempDir()
	source := filepath.Join(baseFolder, "incoming")
	local, remote := fixtureLibrary(t, source)
	options := organizeOptions()
	options.CreateFolderPerGame = false
	options.RenameFiles = false

	ops, err := OrganizeByFolders(baseFolder, local, remote, options, false, nil)
	if err != nil || len(failedOps(ops)) != 0 {
		t.Fatalf("err=%v failed=%+v", err, failedOps(ops))
	}
	// relative subfolders are resolved against the library folder, not the working directory
	assertExists(t, filepath.Join(source, "base.nsp"), true)
	assertExists(t, filepath.Join(baseFolder, "Updates", "update.nsp"), true)
	assertExists(t, filepath.Join(baseFolder, "DLC", "dlc-one.nsp"), true)
}

func TestOrganizeSkipsSplitFilesAndMissingBase(t *testing.T) {
	baseFolder := t.TempDir()
	split := writeFixture(t, filepath.Join(baseFolder, "Split.nsp", "00"), "part")
	orphan := writeFixture(t, filepath.Join(baseFolder, "orphan-update.nsp"), "upd")
	local := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": {BaseExist: true, IsSplit: true, File: db.SwitchFileInfo{ExtendedInfo: split, Metadata: metadataForID("0100000000010000", 0)}},
		"0100000000020": {Updates: map[int]db.SwitchFileInfo{65536: {ExtendedInfo: orphan, Metadata: metadataForID("0100000000020800", 65536)}}},
	}}
	options := organizeOptions()

	// no titles database available
	ops, err := OrganizeByFolders(baseFolder, local, nil, options, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if countKind(ops, OP_SKIP) != 1 || countKind(ops, OP_MOVE) != 0 {
		t.Fatalf("expected only the split file to be skipped: %+v", ops)
	}
	assertExists(t, filepath.Join(baseFolder, "Split.nsp", "00"), true)

	options.ProcessWhenMissingBaseGame = true
	ops, err = OrganizeByFolders(baseFolder, local, nil, options, false, nil)
	if err != nil || countKind(ops, OP_MOVE) != 1 {
		t.Fatalf("orphan update should be organized: err=%v ops=%+v", err, ops)
	}
	assertExists(t, filepath.Join(baseFolder, "orphan-update.nsp"), false)
}

func TestOrganizeRejectsInvalidOptions(t *testing.T) {
	local := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{}}
	options := organizeOptions()
	options.FileNameTemplate = "{VERSION}"
	if _, err := OrganizeByFolders(t.TempDir(), local, nil, options, true, nil); err == nil {
		t.Fatal("expected template validation error")
	}
	if _, err := OrganizeByFolders("", local, nil, organizeOptions(), true, nil); err == nil {
		t.Fatal("expected an error without library folder")
	}
}

func TestDeleteOldUpdates(t *testing.T) {
	baseFolder := t.TempDir()
	oldUpdate := writeFixture(t, filepath.Join(baseFolder, "Game", "old.nsp"), "old")
	duplicate := writeFixture(t, filepath.Join(baseFolder, "Other", "duplicate.nsp"), "dup")
	changed := writeFixture(t, filepath.Join(baseFolder, "changed.nsp"), "changed")
	unsupported := writeFixture(t, filepath.Join(baseFolder, "readme.txt"), "txt")
	changedInDB := changed
	changedInDB.Size = 1

	local := &db.LocalSwitchFilesDB{Skipped: map[db.ExtendedFileInfo]db.SkippedFile{
		oldUpdate:   {ReasonCode: db.REASON_OLD_UPDATE},
		duplicate:   {ReasonCode: db.REASON_DUPLICATE},
		changedInDB: {ReasonCode: db.REASON_OLD_UPDATE},
		unsupported: {ReasonCode: db.REASON_UNSUPPORTED_TYPE},
	}}
	options := settings.OrganizeOptions{DeleteEmptyFolders: true}

	preview := DeleteOldUpdates(baseFolder, local, options, true, nil)
	if countKind(preview, OP_DELETE) != 1 || len(failedOps(preview)) != 1 {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	assertExists(t, filepath.Join(baseFolder, "Game", "old.nsp"), true)

	ops := DeleteOldUpdates(baseFolder, local, options, false, nil)
	if len(failedOps(ops)) != 1 || !strings.Contains(failedOps(ops)[0].Error, "changed") {
		t.Fatalf("a file changed since the scan must not be deleted: %+v", ops)
	}
	assertExists(t, filepath.Join(baseFolder, "Game", "old.nsp"), false)
	assertExists(t, filepath.Join(baseFolder, "Game"), false)
	assertExists(t, filepath.Join(baseFolder, "changed.nsp"), true)
	assertExists(t, filepath.Join(baseFolder, "readme.txt"), true)
	// duplicates are only deleted on request
	assertExists(t, filepath.Join(baseFolder, "Other", "duplicate.nsp"), true)
	assertExists(t, baseFolder, true)

	options.DeleteDuplicateFiles = true
	DeleteOldUpdates(baseFolder, local, options, false, nil)
	assertExists(t, filepath.Join(baseFolder, "Other", "duplicate.nsp"), false)
}

func TestApplyTemplate(t *testing.T) {
	data := map[string]string{
		settings.TEMPLATE_TITLE_NAME: "Pokémon™: Let’s Go, Eevee!",
		settings.TEMPLATE_TITLE_ID:   "0100187003a36000",
		settings.TEMPLATE_VERSION:    "0",
		settings.TEMPLATE_DLC_NAME:   "",
	}
	got := applyTemplate(data, true, "{TITLE_NAME} ({DLC_NAME})[{TITLE_ID}][v{VERSION}]", 0)
	// unsafe characters (™ ’ ! :) are dropped and the empty "()" is removed
	if got != "Pokémon Lets Go, Eevee [0100187003A36000][v0]" {
		t.Fatalf("unexpected safe name %q", got)
	}
	if strings.ContainsAny(got, `/\?%*:;=|"<>`) || strings.Contains(got, "  ") {
		t.Fatalf("name contains illegal characters: %q", got)
	}
	if got := applyTemplate(data, false, "{TITLE_NAME}", 2); !strings.HasSuffix(got, "(2)") {
		t.Fatalf("name try suffix missing: %q", got)
	}
}
