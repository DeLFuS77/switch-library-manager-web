package web

import (
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// mergedGame is a game rebuilt with its update in it, as hacBrewPackLZ writes it: the base file
// takes the version and the control data of the update, and no update file is beside it.
func mergedGame(id string, version int, display string) *db.SwitchGameFiles {
	game := ownedGame(id)
	game.File.ExtendedInfo = db.ExtendedFileInfo{FileName: "Moon Runner [" + id + "].nsp", BaseFolder: "/games"}
	game.File.Metadata.Version = version
	game.File.Metadata.Ncap = &switchfs.Nacp{DisplayVersion: display}
	return game
}

// The Library counts the update a game rebuilt with it holds: it is not among the games with an
// update while no newer one is out, it shows the version of the update, and it is again once a
// newer update is out.
func TestLibraryCountsTheUpdateInAMergedGame(t *testing.T) {
	web := newTestWeb(t)
	title := &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000010000", Name: "Moon Runner", Region: "US"},
		Dlc: map[string]db.TitleAttributes{}, Updates: map[int]string{65536: "2024-01-10", 131072: "2024-03-02"}}
	local := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{"0100000000010": mergedGame("0100000000010000", 131072, "1.0.2")},
		Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{"0100000000010": title}}, local)

	library, _, facets := web.getLibraryWithFacets(defaultFilter(), "en")
	if len(library) != 1 {
		t.Fatalf("library: %+v", library)
	}
	if item := library[0]; item.UpdateAvailable || item.LocalUpdate != 131072 || item.Version != "1.0.2" {
		t.Errorf("the update in the game: %+v", item)
	}
	if facets.Update != 0 || facets.Complete != 1 {
		t.Errorf("with an update: %+v", facets)
	}

	title.Updates[196608] = "2024-06-20"
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{"0100000000010": title}}, local)
	if library, _, facets := web.getLibraryWithFacets(defaultFilter(), "en"); !library[0].UpdateAvailable || facets.Update != 1 {
		t.Errorf("a newer update out: %+v %+v", library[0], facets)
	}
}

// The history of versions marks the version a game rebuilt with its update holds as in the
// library and installed, with its display version.
func TestTitleVersionsOfAMergedGame(t *testing.T) {
	released := map[int]string{65536: "2024-01-10", 131072: "2024-03-02", 196608: "2024-06-20"}
	versions := titleVersions("Moon Runner", released, mergedGame("0100000000010000", 131072, "1.0.2"))
	if versions[1].Version != 131072 || !versions[1].Owned || !versions[1].Installed || versions[1].DisplayVersion != "1.0.2" {
		t.Errorf("the version in the game: %+v", versions[1])
	}
	if versions[0].Owned || versions[0].Installed || versions[2].Owned || versions[2].Installed {
		t.Errorf("the other versions: %+v", versions)
	}

	// an older update file beside it: both are in the library, the game is what is installed
	local := mergedGame("0100000000010000", 131072, "1.0.2")
	local.Updates[65536] = db.SwitchFileInfo{Metadata: &switchfs.ContentMetaAttributes{Version: 65536}}
	local.LatestUpdate = 65536
	versions = titleVersions("Moon Runner", released, local)
	if !versions[2].Owned || versions[2].Installed || !versions[1].Owned || !versions[1].Installed {
		t.Errorf("with an older update file: %+v", versions)
	}
}

// A pack of a game rebuilt with its update holds no older update file, its DLC asks for no
// update it already holds, and its name tells the version.
func TestPackOfAMergedGame(t *testing.T) {
	local := mergedGame("0100000000010000", 131072, "1.0.2")
	old := db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "Moon Runner [0100000000010800][v65536].nsp", BaseFolder: "/games"},
		Metadata: &switchfs.ContentMetaAttributes{Version: 65536}}
	local.Updates[65536] = old
	local.LatestUpdate = 65536
	local.Dlc["0100000000011001"] = db.SwitchFileInfo{Metadata: &switchfs.ContentMetaAttributes{Type: "DLC", RequiredTitleVersion: 131072}}

	for _, file := range packFiles(local) {
		if file == old.ExtendedInfo {
			t.Error("the older update file is not packed")
		}
	}
	if contents := packContents(local); contents != "1G+1D" {
		t.Errorf("contents: %s", contents)
	}
	if need := dlcNeedsNewer(local); need != 0 {
		t.Errorf("the DLC has its update: %d", need)
	}
	if name := packName("Moon Runner", "0100000000010000", local, false); name != "Moon Runner [0100000000010000][v131072][+1 DLC] (pack).nsp" {
		t.Errorf("name: %s", name)
	}
}
