package process

import (
	"go.uber.org/zap"
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
	if len(failed) != 1 || !strings.Contains(failed[0].Error, "already at the destination") {
		t.Fatalf("expected one 'already at the destination' error, got %+v", failed)
	}
	if data, _ := os.ReadFile(existing); string(data) != "another game" {
		t.Fatal("existing file was overwritten")
	}
	assertExists(t, filepath.Join(source, "base.nsp"), true)
}

// A copy of the same size where a file would go is a duplicate: the file is left where it
// is, without an error, and nothing is overwritten.
func TestOrganizeLeavesDuplicatesWhereTheyAre(t *testing.T) {
	baseFolder := t.TempDir()
	source := filepath.Join(baseFolder, "incoming")
	local, remote := fixtureLibrary(t, source)
	options := organizeOptions()
	options.RenameFiles = false
	options.UpdatesFolder = ""
	options.DlcFolder = ""

	original, _ := os.ReadFile(filepath.Join(source, "base.nsp"))
	existing := filepath.Join(baseFolder, gameTitle, "base.nsp")
	writeFixture(t, existing, strings.Repeat("x", len(original)))

	ops, err := OrganizeByFolders(baseFolder, local, remote, options, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if failed := failedOps(ops); len(failed) != 0 {
		t.Fatalf("a duplicate is no error: %+v", failed)
	}
	skipped := false
	for _, op := range ops {
		if op.Kind == OP_SKIP && op.Reason == REASON_DUPLICATE_AT_DESTINATION {
			skipped = true
		}
	}
	if !skipped {
		t.Fatalf("the duplicate is told: %+v", ops)
	}
	assertExists(t, filepath.Join(source, "base.nsp"), true)
}

// A game without an English name in the titles database or in itself is named by its name in
// another language before the name of its file; a placeholder name does not count.
func TestNameInAnotherLanguage(t *testing.T) {
	nacp := func(names map[string]string) *db.SwitchGameFiles {
		titles := map[string]switchfs.NacpTitle{}
		for language, name := range names {
			titles[language] = switchfs.NacpTitle{Title: name}
		}
		return &db.SwitchGameFiles{File: db.SwitchFileInfo{
			ExtendedInfo: db.ExtendedFileInfo{FileName: "d4r1k dun6630n 8453.nsp"},
			Metadata:     &switchfs.ContentMetaAttributes{Ncap: &switchfs.Nacp{TitleName: titles}}}}
	}
	if name := getTitleName(nil, nacp(map[string]string{"Spanish": "Mazmorra Oscura", "Japanese": "暗いダンジョン"})); name != "Mazmorra Oscura" {
		t.Fatalf("a Latin name first: %q", name)
	}
	if name := getTitleName(nil, nacp(map[string]string{"Japanese": "暗いダンジョン"})); name != "暗いダンジョン" {
		t.Fatalf("the Japanese name before the file name: %q", name)
	}
	placeholder := &db.SwitchTitle{Attributes: db.TitleAttributes{Name: "-"}}
	if name := getTitleName(placeholder, nacp(map[string]string{})); name != "d4r1k dun6630n 8453" {
		t.Fatalf("a placeholder name does not count: %q", name)
	}
}

// A file without a title ID in its name names its folder without its extension.
func TestNameFromAFileWithoutTitleId(t *testing.T) {
	files := &db.SwitchGameFiles{File: db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "h0t0t g4m3 8453.nsp"}}}
	if name := getTitleName(nil, files); name != "h0t0t g4m3 8453" {
		t.Fatalf("name: %q", name)
	}
	files.File.ExtendedInfo.FileName = "Game [0100000000010000][v0].nsp"
	if name := getTitleName(nil, files); name != "Game" {
		t.Fatalf("name: %q", name)
	}
}

// A name with no letter left by the names for the Switch (Chinese, Korean) takes the one of
// its file, or Unknown Title, instead of a folder called "_".
func TestAsciiFallbackName(t *testing.T) {
	if name := safeName("暗黑地牢"); name != "" {
		t.Fatalf("safe name: %q", name)
	}
	files := &db.SwitchGameFiles{File: db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "Unknown Title [0100CF6011A2C000][v0].nsz"}}}
	if name := asciiFallbackName(files); name != "Unknown Title" {
		t.Fatalf("from the file: %q", name)
	}
	files.File.ExtendedInfo.FileName = "暗黑地牢 [0100CF6011A2C000][v0].nsz"
	if name := asciiFallbackName(files); name != "Unknown Title" {
		t.Fatalf("no letter in the file either: %q", name)
	}
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
	// the split file and the update without its game are left where they are, both told
	if countKind(ops, OP_SKIP) != 2 || countKind(ops, OP_MOVE) != 0 {
		t.Fatalf("expected the split file and the orphan update to be skipped: %+v", ops)
	}
	for _, op := range ops {
		if op.From == filepath.Join(orphan.BaseFolder, orphan.FileName) && op.Reason != REASON_NO_BASE {
			t.Fatalf("the orphan update without its reason: %+v", op)
		}
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

// The folders the run took files from go when they are left empty, deepest first with the
// parents they leave empty; a folder with a file, a hidden one too, stays with its parents, and
// a folder the run did not touch is not looked at.
func TestDeleteEmptyFolders(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"a/b/c", "d", "e/f", "g", "untouched"} {
		os.MkdirAll(filepath.Join(root, dir), 0755)
	}
	os.WriteFile(filepath.Join(root, "e", "f", "game.nsp"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(root, "g", ".DS_Store"), []byte("x"), 0644)
	ops := &fileOperations{logger: zap.NewNop().Sugar()}
	// files moved out of these folders
	for _, dir := range []string{"a/b/c", "d", "e/f", "g"} {
		ops.record(Operation{Kind: OP_MOVE, From: filepath.Join(root, dir, "x.nsp"), To: filepath.Join(root, "x.nsp")}, nil)
	}
	moves := len(ops.operations)
	ops.deleteEmptyFolders(root)
	for _, gone := range []string{"a", "d"} {
		if _, err := os.Stat(filepath.Join(root, gone)); !os.IsNotExist(err) {
			t.Errorf("%s stays", gone)
		}
	}
	for _, kept := range []string{"e/f", "g", "untouched"} {
		if _, err := os.Stat(filepath.Join(root, kept)); err != nil {
			t.Errorf("%s was deleted", kept)
		}
	}
	if len(ops.operations)-moves != 4 {
		t.Fatalf("operations: %+v", ops.operations[moves:])
	}
}

// The folders left empty go with the parents they leave empty, up to the library folder, which
// stays; a folder with a hidden file stays, and nothing outside the library is touched.
func TestRemoveEmptyFolders(t *testing.T) {
	library := t.TempDir()
	outside := t.TempDir()
	for _, dir := range []string{"Game/Updates", "Game/DLC", "Other/DLC", "Kept"} {
		os.MkdirAll(filepath.Join(library, dir), 0o755)
	}
	os.WriteFile(filepath.Join(library, "Kept", ".DS_Store"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(outside, "empty"), 0o755)
	removed := RemoveEmptyFolders([]string{
		filepath.Join(library, "Game", "Updates"), filepath.Join(library, "Game", "DLC"),
		filepath.Join(library, "Kept"), filepath.Join(outside, "empty"), library,
	}, []string{library})
	if len(removed) != 3 {
		t.Fatalf("Game/Updates, Game/DLC and Game: %v", removed)
	}
	for _, kept := range []string{"", "Other/DLC", "Kept"} {
		if _, err := os.Stat(filepath.Join(library, kept)); err != nil {
			t.Errorf("%q stays", kept)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "empty")); err != nil {
		t.Error("nothing outside the library is deleted")
	}
}

// A cartridge with several games is named after all of them, once, in a folder of its own,
// and not moved again as the update of one of them.
func TestOrganizeNamesACartridgeWithSeveralGames(t *testing.T) {
	baseFolder := t.TempDir()
	card := writeFixture(t, filepath.Join(baseFolder, "Second", "Updates", "Second [0100000000020800][v65536].xcz"), "card")
	game := func(id string) *db.SwitchGameFiles {
		return &db.SwitchGameFiles{BaseExist: true, MultiContent: true, File: db.SwitchFileInfo{ExtendedInfo: card, Metadata: metadataForID(id, 0)},
			Updates: map[int]db.SwitchFileInfo{65536: {ExtendedInfo: card, Metadata: metadataForID(id[:13]+"800", 65536)}}, Dlc: map[string]db.SwitchFileInfo{}}
	}
	local := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": game("0100000000010000"),
		"0100000000020": game("0100000000020000"),
	}}
	remote := &db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		"0100000000010": {Attributes: db.TitleAttributes{Id: "0100000000010000", Name: "First Game"}},
		"0100000000020": {Attributes: db.TitleAttributes{Id: "0100000000020000", Name: "Second Game"}},
	}}
	result, err := OrganizeByFolders(baseFolder, local, remote, organizeOptions(), false, nil)
	if err != nil || len(failedOps(result)) != 0 {
		t.Fatalf("organize: %v %+v", err, failedOps(result))
	}
	if countKind(result, OP_MOVE) != 1 {
		t.Fatalf("the card is moved once: %+v", result)
	}
	assertExists(t, filepath.Join(baseFolder, "First Game + Second Game", "First Game + Second Game [0100000000010000][2 games].xcz"), true)
	assertExists(t, filepath.Join(card.BaseFolder, card.FileName), false)

	if got := collectionName([]string{"Alphadia Genesis 2", "Asdivine Cross", "Chroma Quaternion", "Ruinverse"}, 0); got != "Alphadia Genesis 2 + Asdivine Cross + Chroma Quaternion + Ruinverse" {
		t.Fatalf("four short names fit: %s", got)
	}
	long := []string{strings.Repeat("A", 50), strings.Repeat("B", 50), strings.Repeat("C", 50), strings.Repeat("D", 50)}
	if got := collectionName(long, 0); got != long[0]+" + "+long[1]+" + 2 more" {
		t.Fatalf("long names: %s", got)
	}
	// the two volumes of a collection fit
	if got := collectionName([]string{"Mega Man Battle Network Legacy Collection Vol. 1", "Mega Man Battle Network Legacy Collection Vol. 2"}, 0); strings.Contains(got, "more") {
		t.Fatalf("two volumes: %s", got)
	}
	// a game without a name that can be written counts in the more, and none at all is a Collection
	if got := collectionName([]string{"Mom Hid My Game 2"}, 1); got != "Mom Hid My Game 2 + 1 more" {
		t.Fatalf("unnamed: %s", got)
	}
	if got := collectionName(nil, 2); got != "Collection" {
		t.Fatalf("no names: %s", got)
	}
	// a Japanese name that loses its letters is not used, unless the English one is known
	japanese := &db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{"0100e8b01eb54": {Attributes: db.TitleAttributes{Name: "ファミコン探偵倶楽部"}}},
		Localized: map[string]map[string]db.LocalizedTitle{}}
	if got := collectionGameName(japanese, "0100e8b01eb54", nil, true); got != "" {
		t.Fatalf("a name that loses its letters: %q", got)
	}
	japanese.Localized["en"] = map[string]db.LocalizedTitle{"0100E8B01EB54000": {Name: "Famicom Detective Club"}}
	if got := collectionGameName(japanese, "0100e8b01eb54", nil, true); got != "Famicom Detective Club" {
		t.Fatalf("the English name: %q", got)
	}
}

// With folders by ID, the game folder has its ID, the update goes to a folder with the ID of
// the update and each DLC to its own folder with its ID, inside the DLC folder.
func TestOrganizeFoldersWithIds(t *testing.T) {
	for _, named := range []bool{true, false} {
		baseFolder := t.TempDir()
		local, remote := fixtureLibrary(t, filepath.Join(baseFolder, "incoming"))
		options := organizeOptions()
		options.FoldersWithIds = true
		updates, dlc := "Updates [0100E95004039800]", "DLC"
		if !named {
			options.UpdatesFolder, options.DlcFolder = "", ""
			updates = "Update [0100E95004039800]"
		}
		ops, err := OrganizeByFolders(baseFolder, local, remote, options, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		game := filepath.Join(baseFolder, "Test Game [0100E95004039000]")
		want := map[string]bool{
			game:                         true,
			filepath.Join(game, updates): true,
			filepath.Join(game, dlc, "Expansion [0100E9500403A001]"): true,
			filepath.Join(game, dlc, "Expansion [0100E9500403A002]"): true,
		}
		for _, op := range ops {
			if op.Kind != OP_MOVE {
				continue
			}
			if !want[filepath.Dir(op.To)] {
				t.Errorf("named %v: %s goes to %s", named, filepath.Base(op.From), filepath.Dir(op.To))
			}
			delete(want, filepath.Dir(op.To))
		}
		if len(want) != 0 {
			t.Errorf("named %v: folders without a file: %v", named, want)
		}
	}
	// a template with the ID already is left as it is
	options := organizeOptions()
	options.FoldersWithIds, options.FolderNameTemplate = true, "[{TITLE_ID}] {TITLE_NAME}"
	if name := getFolderName(options, map[string]string{settings.TEMPLATE_TITLE_NAME: "Game", settings.TEMPLATE_TITLE_ID: baseID}); name != "[0100E95004039000] Game" {
		t.Fatalf("the template with the ID: %s", name)
	}
}
