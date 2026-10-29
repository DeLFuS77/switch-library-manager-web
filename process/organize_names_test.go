package process

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// A file with several DLC (or an update and DLC without the game) is moved once, with its
// first content; the others are not errors.
func TestBundleFileIsMovedOnce(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	local, remote := fixtureLibrary(t, source)
	game := local.TitlesMap[groupID]
	bundle := writeFixture(t, filepath.Join(source, "dlc-bundle.nsp"), "both")
	game.Dlc[dlcOneID] = db.SwitchFileInfo{ExtendedInfo: bundle, Metadata: metadataForID(dlcOneID, 0)}
	game.Dlc[dlcTwoID] = db.SwitchFileInfo{ExtendedInfo: bundle, Metadata: metadataForID(dlcTwoID, 0)}

	ops, err := OrganizeByFolders(target, local, remote, organizeOptions(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if failed := failedOps(ops); len(failed) != 0 {
		t.Fatalf("no errors: %+v", failed)
	}
	assertExists(t, filepath.Join(source, "dlc-bundle.nsp"), false)
}

// Names stay within the 255 bytes of Linux file systems, keeping the title ID and the
// version, the parts that identify the file.
func TestLongNamesAreShortened(t *testing.T) {
	data := map[string]string{
		settings.TEMPLATE_TITLE_NAME: strings.Repeat("ゲームのなまえ", 20),
		settings.TEMPLATE_DLC_NAME:   strings.Repeat("ダウンロードコンテンツ", 10),
		settings.TEMPLATE_TITLE_ID:   dlcOneID,
		settings.TEMPLATE_TYPE:       "DLC",
		settings.TEMPLATE_VERSION:    "65536",
	}
	options := organizeOptions()
	options.SwitchSafeFileNames = false
	name := getFileName(options, "file.nsz", data, 3)
	if len(name) > maxNameBytes || !strings.Contains(name, "["+strings.ToUpper(dlcOneID)+"]") || !strings.Contains(name, "[v65536]") || !strings.HasSuffix(name, "(3).nsz") {
		t.Fatalf("%d bytes: %s", len(name), name)
	}
	if !strings.HasPrefix(name, "ゲーム") || strings.ContainsRune(name, '�') {
		t.Fatalf("cut on a character boundary: %s", name)
	}
	if folder := getFolderName(organizeOptions(), data); len(folder) > maxNameBytes {
		t.Fatalf("folder of %d bytes", len(folder))
	}
}

func TestReservedWindowsNamesAreAvoided(t *testing.T) {
	for _, reserved := range []string{"CON", "nul", "Aux", "COM1", "lpt9"} {
		data := map[string]string{settings.TEMPLATE_TITLE_NAME: reserved}
		options := settings.OrganizeOptions{RenameFiles: true, FileNameTemplate: "{TITLE_NAME}", FolderNameTemplate: "{TITLE_NAME}"}
		if name := getFileName(options, "a.nsp", data, 0); strings.EqualFold(name, reserved+".nsp") {
			t.Errorf("file %s", name)
		}
		if folder := getFolderName(options, data); strings.EqualFold(folder, reserved) {
			t.Errorf("folder %s", folder)
		}
	}
}

// A pack with the game and its first update, next to a newer separate update: the pack is
// named with the update it holds.
func TestPackIsNamedWithTheUpdateItHolds(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	local, remote := fixtureLibrary(t, source)
	game := local.TitlesMap[groupID]
	game.MultiContent = true
	game.Updates[65536] = db.SwitchFileInfo{ExtendedInfo: game.File.ExtendedInfo, Metadata: metadataForID(updateID, 65536)}
	game.Updates[131072] = db.SwitchFileInfo{ExtendedInfo: writeFixture(t, filepath.Join(source, "update2.nsp"), "update2"), Metadata: metadataForID(updateID, 131072)}
	game.LatestUpdate = 131072

	ops, err := OrganizeByFolders(target, local, remote, organizeOptions(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Kind == OP_MOVE && op.From == filepath.Join(source, "base.nsp") {
			if !strings.Contains(op.To, "[v65536]") {
				t.Fatalf("the pack holds v65536: %s", op.To)
			}
			return
		}
	}
	t.Fatalf("the pack is moved: %+v", ops)
}

// A file with several contents already named after its first one stays where it is: the
// other contents must not move it to their own names, which a second run would undo.
func TestBundleFileInPlaceStays(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	local, remote := fixtureLibrary(t, source)
	game := local.TitlesMap[groupID]
	bundle := writeFixture(t, filepath.Join(source, "dlc-bundle.nsp"), "both")
	game.Dlc[dlcOneID] = db.SwitchFileInfo{ExtendedInfo: bundle, Metadata: metadataForID(dlcOneID, 0)}
	game.Dlc[dlcTwoID] = db.SwitchFileInfo{ExtendedInfo: bundle, Metadata: metadataForID(dlcTwoID, 0)}
	ops, err := OrganizeByFolders(target, local, remote, organizeOptions(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	moved := ""
	for _, op := range ops {
		if op.Kind == OP_MOVE && op.From == filepath.Join(source, "dlc-bundle.nsp") {
			moved = op.To
		}
	}
	if moved == "" {
		t.Fatalf("the bundle is moved: %+v", ops)
	}

	// the next scan finds every file where the first run put it
	for id, dlc := range game.Dlc {
		dlc.ExtendedInfo.BaseFolder, dlc.ExtendedInfo.FileName = filepath.Dir(moved), filepath.Base(moved)
		game.Dlc[id] = dlc
	}
	for _, op := range ops {
		if op.Kind != OP_MOVE || op.From == filepath.Join(source, "dlc-bundle.nsp") {
			continue
		}
		relocate(local, op.From, op.To)
	}
	ops, err = OrganizeByFolders(target, local, remote, organizeOptions(), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Kind == OP_MOVE && op.From == moved {
			t.Errorf("the bundle in place is moved again: %+v", op)
		}
	}
}

// relocate points the files of the library found at from to their new path.
func relocate(local *db.LocalSwitchFilesDB, from string, to string) {
	fix := func(info *db.ExtendedFileInfo) {
		if filepath.Join(info.BaseFolder, info.FileName) == from {
			info.BaseFolder, info.FileName = filepath.Dir(to), filepath.Base(to)
		}
	}
	for _, title := range local.TitlesMap {
		fix(&title.File.ExtendedInfo)
		for version, update := range title.Updates {
			fix(&update.ExtendedInfo)
			title.Updates[version] = update
		}
		for id, dlc := range title.Dlc {
			fix(&dlc.ExtendedInfo)
			title.Dlc[id] = dlc
		}
	}
}

// Once organized, a second run has nothing to do: folders are created only for the files
// that move into them, so none is created and deleted again as empty on every run.
func TestSecondOrganizeRunIsQuiet(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	local, remote := fixtureLibrary(t, source)
	ops, err := OrganizeByFolders(target, local, remote, organizeOptions(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	// the next run finds every file in place
	for _, op := range ops {
		if op.Kind == OP_MOVE {
			relocate(local, op.From, op.To)
		}
	}
	ops, err = OrganizeByFolders(target, local, remote, organizeOptions(), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Kind == OP_MKDIR || op.Kind == OP_MOVE {
			t.Errorf("nothing to do on the second run: %+v", op)
		}
	}
}

// A part of a split file is never moved or deleted alone, whatever it holds: the other
// parts would be left behind.
func TestSplitPartsStay(t *testing.T) {
	folder := t.TempDir()
	part := writeFixture(t, filepath.Join(folder, "Game Update.nsp.00"), "both")
	ops := newFileOperations(false)
	if err := ops.move(filepath.Join(folder, "Game Update.nsp.00"), filepath.Join(folder, "Updates", "Game [v1].nsp")); err != nil {
		t.Fatal(err)
	}
	if err := ops.remove(part, "old update"); err != nil {
		t.Fatal(err)
	}
	assertExists(t, filepath.Join(folder, "Game Update.nsp.00"), true)
	for _, op := range ops.operations {
		if op.Kind != OP_SKIP || op.Reason != REASON_SPLIT {
			t.Errorf("only skipped: %+v", op)
		}
	}
}

// Without the names for the Switch, a cartridge with several games is named without the
// characters a disk refuses or a path splits on, and within what a disk takes: "Fate/EXTELLA"
// made two folders on Linux and "NieR:Automata?" no folder on Windows, and long Japanese names,
// longer still decomposed, a name the disk refused.
func TestCartridgeNamesFitTheDisk(t *testing.T) {
	baseFolder := t.TempDir()
	card := writeFixture(t, filepath.Join(baseFolder, "card.xci"), "card")
	game := func(id string) *db.SwitchGameFiles {
		return &db.SwitchGameFiles{BaseExist: true, MultiContent: true, File: db.SwitchFileInfo{ExtendedInfo: card, Metadata: metadataForID(id, 0)},
			Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}}
	}
	local := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": game("0100000000010000"),
		"0100000000020": game("0100000000020000"),
	}}
	remote := &db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		"0100000000010": {Attributes: db.TitleAttributes{Id: "0100000000010000", Name: "Fate/EXTELLA "}},
		"0100000000020": {Attributes: db.TitleAttributes{Id: "0100000000020000", Name: "NieR:Automata?"}},
	}}
	options := organizeOptions()
	options.SwitchSafeFileNames = false
	ops, err := OrganizeByFolders(baseFolder, local, remote, options, false, nil)
	if err != nil || len(failedOps(ops)) != 0 {
		t.Fatalf("organize: %v %+v", err, failedOps(ops))
	}
	assertExists(t, filepath.Join(baseFolder, "FateEXTELLA + NieRAutomata", "FateEXTELLA + NieRAutomata [0100000000010000][2 games].xci"), true)

	long := strings.Repeat("ガ", 60)
	name := collectionName([]string{long, "Second"}, 0)
	if NameBytes(name) > 130 || !strings.HasPrefix(name, "ガ") || !strings.HasSuffix(name, " + 1 more") {
		t.Fatalf("a long Japanese name is cut to fit, decomposed: %d bytes, %q", NameBytes(name), name)
	}
	if !utf8.ValidString(name) {
		t.Fatalf("never inside a character: %q", name)
	}
}
