package process

import (
	"path/filepath"
	"strings"
	"testing"

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
