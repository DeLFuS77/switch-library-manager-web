package process

import (
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The fields of a file: the titles of each kind it holds and its size, also in a template.
func TestFileTemplateData(t *testing.T) {
	pack := db.ExtendedFileInfo{FileName: "Game.nsp", BaseFolder: "C:/games", Size: 2_650_000_000}
	update := db.ExtendedFileInfo{FileName: "Update.nsp", BaseFolder: "C:/games", Size: 600_000_000}
	game := &db.SwitchGameFiles{
		File:      db.SwitchFileInfo{ExtendedInfo: pack},
		BaseExist: true,
		Updates:   map[int]db.SwitchFileInfo{65536: {ExtendedInfo: pack}, 131072: {ExtendedInfo: update}},
		Dlc:       map[string]db.SwitchFileInfo{"0100000000011001": {ExtendedInfo: pack}, "0100000000011002": {ExtendedInfo: pack}},
	}
	data := map[string]string{settings.TEMPLATE_TITLE_NAME: "Game", settings.TEMPLATE_TITLE_ID: "0100000000010000", settings.TEMPLATE_VERSION: "65536"}
	fileTemplateData(data, game, pack)
	if got := applyTemplate(data, false, "{TITLE_NAME} [{TITLE_ID}][v{VERSION}] ({PACKAGE_CONTENTS}) {SIZE_GB} {SIZE_MB}", 0); got != "Game [0100000000010000][v65536] (1G+1U+2D) 2.7GB 2650MB" {
		t.Fatal(got)
	}
	fileTemplateData(data, game, update)
	if data[settings.TEMPLATE_PACKAGE_CONTENTS] != "1U" || data[settings.TEMPLATE_SIZE_GB] != "0.6GB" || data[settings.TEMPLATE_SIZE_MB] != "600MB" {
		t.Fatalf("an update of its own: %v", data)
	}
}

// {DLC_NUMBER} is the number of a DLC from its title ID, and a name with its title ID stays
// when the user asks.
func TestDlcNumberAndKeptNames(t *testing.T) {
	for id, want := range map[string]string{"0100000000011001": "1", "010022900D3ED01C": "28", "0100000000010000": "", "0100000000010800": "", "nothing": ""} {
		if got := dlcNumber(id); got != want {
			t.Errorf("dlcNumber(%s) = %q, want %q", id, got, want)
		}
	}
	data := map[string]string{settings.TEMPLATE_TITLE_NAME: "Game", settings.TEMPLATE_TITLE_ID: "0100000000011002", settings.TEMPLATE_VERSION: "0", settings.TEMPLATE_DLC_NUMBER: "2"}
	options := settings.OrganizeOptions{RenameFiles: true, FileNameTemplate: "{TITLE_NAME} [DLC {DLC_NUMBER}] [{TITLE_ID}][v{VERSION}]"}
	if got := getFileName(options, "anything.nsp", data, 0); got != "Game [DLC 2] [0100000000011002][v0].nsp" {
		t.Errorf("template: %q", got)
	}
	options.KeepNamesWithId = true
	if got := getFileName(options, "My name [0100000000011002].nsp", data, 0); got != "My name [0100000000011002].nsp" {
		t.Errorf("a name with its title ID: %q", got)
	}
	if got := getFileName(options, "My name [0100000000011002].nsp", data, 1); got == "My name [0100000000011002].nsp" {
		t.Error("a second try keeps the name it collided with")
	}
	if got := getFileName(options, "My name [0100000000011003].nsp", data, 0); got != "Game [DLC 2] [0100000000011002][v0].nsp" {
		t.Errorf("a name with another title ID: %q", got)
	}
}
