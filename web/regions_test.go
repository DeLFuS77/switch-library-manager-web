package web

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

func TestRegionalDuplicates(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	switchDB.TitlesMap["0100000000010"].Attributes.Languages = []string{"en", "ja"}
	// the Japanese copy of the same game, with Spanish
	switchDB.TitlesMap["0100000000050"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000050000", Name: "Known Game（ノウン）", Region: "JP", Languages: []string{"es", "ja"}}}
	localDB.TitlesMap["0100000000050"] = &db.SwitchGameFiles{
		BaseExist: true,
		File:      db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "Known JP.nsp", Size: 100}, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000050000"}},
		Updates:   map[int]db.SwitchFileInfo{},
		Dlc:       map[string]db.SwitchFileInfo{},
	}
	web.state.set(switchDB, localDB)

	groups := web.regionalDuplicates("es")
	if len(groups) != 1 || len(groups[0].Copies) != 2 {
		t.Fatalf("one game with two copies: %+v", groups)
	}
	keep := groups[0].Copies[0]
	if !keep.Keep || keep.Id != "0100000000050000" || keep.Reasons[0] != REGION_REASON_LANGUAGE || groups[0].Savable == 0 {
		t.Fatalf("the copy in Spanish is suggested for a Spanish interface: %+v", groups[0])
	}
	if groups := web.regionalDuplicates("en"); groups[0].Copies[0].Id != "0100000000010000" {
		t.Fatalf("the copy with the update and the DLC for an English interface: %+v", groups[0])
	}
}

// regionTestWeb has the Known Game twice: the American copy of the test library, and a
// Japanese copy with its files on disk.
func regionTestWeb(t *testing.T) (*Web, string) {
	t.Helper()
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	switchDB.TitlesMap["0100000000010"].Attributes.Languages = []string{"en", "ja"}
	switchDB.TitlesMap["0100000000050"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000050000", Name: "Known Game（ノウン）", Region: "JP", Languages: []string{"es", "ja"}}}
	folder := t.TempDir()
	for _, name := range []string{"Known JP.nsp", "Known JP DLC.nsp"} {
		if err := os.WriteFile(filepath.Join(folder, name), []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	localDB.TitlesMap["0100000000050"] = &db.SwitchGameFiles{
		BaseExist: true,
		File:      db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "Known JP.nsp", BaseFolder: folder, Size: 4}, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000050000"}},
		Updates:   map[int]db.SwitchFileInfo{},
		Dlc:       map[string]db.SwitchFileInfo{"0100000000051001": {ExtendedInfo: db.ExtendedFileInfo{FileName: "Known JP DLC.nsp", BaseFolder: folder, Size: 4}}},
	}
	web.state.set(switchDB, localDB)
	return web, folder
}

func TestRegionalDuplicateDamagedIsNotKept(t *testing.T) {
	web, folder := regionTestWeb(t)
	// the Spanish copy would be kept for a Spanish interface, but it is damaged
	path := filepath.Join(folder, "Known JP.nsp")
	info, _ := os.Stat(path)
	web.verifications().set(path, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: false, Reason: "bad", Checker: verifyChecker})
	group := web.buildRegionalDuplicates("es")[0]
	if group.Copies[0].Id != "0100000000010000" || group.Copies[0].Reasons[0] != REGION_REASON_SOUND || !group.Copies[1].Damaged {
		t.Fatalf("the sound copy is kept: %+v", group.Copies)
	}
}

func TestRemoveRegionCopy(t *testing.T) {
	web, folder := regionTestWeb(t)
	// the copy that stays was never checked: nothing is deleted
	if _, _, err := web.removeRegionCopy("0100000000050000", "en"); err != errRegionVerify {
		t.Fatalf("the copy that stays must be checked first: %v", err)
	}
	if _, err := os.Stat(filepath.Join(folder, "Known JP.nsp")); err != nil {
		t.Fatal("nothing is deleted before")
	}
	_, localDB := web.state.get()
	base := localDB.TitlesMap["0100000000010"].File.ExtendedInfo
	kept := filepath.Join(base.BaseFolder, base.FileName)
	info, err := os.Stat(kept)
	if err != nil {
		t.Fatal(err)
	}
	web.verifications().set(kept, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: true, Checker: verifyChecker})
	deleted, freed, err := web.removeRegionCopy("0100000000050000", "en")
	if err != nil || deleted != 2 || freed != 8 {
		t.Fatalf("the copy and its DLC: %d %d %v", deleted, freed, err)
	}
	for _, name := range []string{"Known JP.nsp", "Known JP DLC.nsp"} {
		if _, err := os.Stat(filepath.Join(folder, name)); !os.IsNotExist(err) {
			t.Fatalf("%s is deleted", name)
		}
	}
	// an unknown game, or a game with a single copy, is never deleted
	if _, _, err := web.removeRegionCopy("0100000000099000", "en"); err != errRegionCopy {
		t.Fatalf("not a copy of another region: %v", err)
	}
	web.state.set(testDatabases(t))
	if _, _, err := web.removeRegionCopy("0100000000010000", "en"); err != errRegionCopy {
		t.Fatalf("the only copy: %v", err)
	}
}

func TestRegionCopyNeedsAnotherRegionAndKeepsSharedFiles(t *testing.T) {
	web, folder := regionTestWeb(t)
	switchDB, localDB := web.state.get()
	base := localDB.TitlesMap["0100000000010"].File.ExtendedInfo
	kept := filepath.Join(base.BaseFolder, base.FileName)
	info, _ := os.Stat(kept)
	web.verifications().set(kept, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: true, Checker: verifyChecker})

	// two games of the same name and region are different games, not copies
	region := switchDB.TitlesMap["0100000000050"].Attributes.Region
	switchDB.TitlesMap["0100000000050"].Attributes.Region = switchDB.TitlesMap["0100000000010"].Attributes.Region
	web.invalidateDerived()
	if _, _, err := web.removeRegionCopy("0100000000050000", "en"); err != errRegionCopy {
		t.Fatalf("same region: %v", err)
	}
	if leftOut, _ := web.regionCopies("en"); len(leftOut) != 0 {
		t.Fatalf("neither is left out of the library: %v", leftOut)
	}
	switchDB.TitlesMap["0100000000050"].Attributes.Region = region
	web.invalidateDerived()

	// the DLC file also holds a DLC of another game: it stays
	shared := localDB.TitlesMap["0100000000050"].Dlc["0100000000051001"]
	localDB.TitlesMap["0100000000099"] = &db.SwitchGameFiles{Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{"0100000000099001": shared}}
	web.invalidateDerived()
	deleted, _, err := web.removeRegionCopy("0100000000050000", "en")
	if err != nil || deleted != 1 {
		t.Fatalf("only the game file: %d %v", deleted, err)
	}
	if _, err := os.Stat(filepath.Join(folder, "Known JP DLC.nsp")); err != nil {
		t.Fatal("the shared file stays")
	}
}

// With the setting on, a game the library has from two regions shows and counts once, by the
// copy suggested to keep, with the other region told on its card; off, both count.
func TestCountRegionsOnce(t *testing.T) {
	web, _ := regionTestWeb(t)
	filter := &TitleItemFilter{WithMissing: "0"}
	filter.Normalize()
	_, _, before := web.getLibraryWithFacets(filter, "en")

	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.CountRegionsOnce = true })
	items, _, after := web.getLibraryWithFacets(filter, "en")
	if after.All != before.All-1 {
		t.Fatalf("the two regions count once: %d before, %d after", before.All, after.All)
	}
	others := 0
	for _, item := range items {
		others += item.OtherRegions
	}
	if others != 1 {
		t.Fatalf("the copy kept tells the other region: %+v", items)
	}
	if stats := web.getStatistics("en"); stats.Games != after.All {
		t.Fatalf("the statistics count as the library: %d and %d", stats.Games, after.All)
	}
	if views := web.libraryViews("mine", "en"); views.Mine != after.All {
		t.Fatalf("My games counts as the library: %d and %d", views.Mine, after.All)
	}
}

// The copy chosen of a game of several regions stands for it instead of the suggested one: the
// library shows it with the flags of the others, Space keeps it, and the page of the game lists
// the copies; the suggested one comes back when asked.
func TestRegionMainCopy(t *testing.T) {
	web, _ := regionTestWeb(t)
	web.handleRegionMain()
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.CountRegionsOnce = true })
	if group := web.buildRegionalDuplicates("es")[0]; group.Copies[0].Id != "0100000000050000" || !group.Copies[0].Suggested || group.Chosen {
		t.Fatalf("the Spanish copy is suggested: %+v", group)
	}

	if response := postForm(web, "/regions/main", url.Values{"id": {"0100000000010000"}}); response.Code != http.StatusOK {
		t.Fatalf("choose: %d %s", response.Code, response.Body.String())
	}
	group := web.regionalDuplicates("es")[0]
	main, other := group.Copies[0], group.Copies[1]
	if !group.Chosen || main.Id != "0100000000010000" || !main.Keep || !main.Chosen || other.Keep || !other.Suggested || group.Savable != other.Size {
		t.Fatalf("the American copy is the main one: %+v", group)
	}
	leftOut, others := web.regionCopies("es")
	if !leftOut["0100000000050000"] || leftOut["0100000000010000"] || len(others["0100000000010000"]) != 1 || others["0100000000010000"][0] != "JP" {
		t.Fatalf("the library shows the chosen copy with the other flag: %v %v", leftOut, others)
	}
	filter := &TitleItemFilter{WithMissing: "0"}
	filter.Normalize()
	items, _, _ := web.getLibraryWithFacets(filter, "es")
	found := false
	for _, item := range items {
		if item.Id == "0100000000050000" {
			t.Fatal("the other copy is left out of the library")
		}
		if item.Id == "0100000000010000" {
			found = item.OtherRegions == 1 && len(item.OtherFlags) == 1 && item.OtherFlags[0] == "JP"
		}
	}
	if !found {
		t.Fatalf("the card of the chosen copy has both flags: %+v", items)
	}
	detail, ok := web.getTitleDetail("0100000000050000", "es")
	if !ok || detail.Regions == nil || len(detail.Regions.Copies) != 2 || !detail.Regions.Chosen {
		t.Fatalf("the page of a copy lists the copies: %+v", detail.Regions)
	}
	if other := (&Web{dataFolder: web.dataFolder}).regionChoices().snapshot(); !other["0100000000010000"] {
		t.Fatal("the choice is kept after a restart")
	}

	// back to the suggested copy
	postForm(web, "/regions/main", url.Values{"id": {"0100000000010000"}, "suggested": {"true"}})
	if group := web.regionalDuplicates("es")[0]; group.Chosen || group.Copies[0].Id != "0100000000050000" || !group.Copies[0].Keep {
		t.Fatalf("the suggested copy again: %+v", group)
	}
	// a game of one region, or no title ID
	for _, id := range []string{"0100000000020000", "nope"} {
		if response := postForm(web, "/regions/main", url.Values{"id": {id}}); response.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", id, response.Code)
		}
	}
}

// With each game counting once, the card has the flags of the regions the game was released in,
// the ones of the library lit and the others dimmed; a name of two games of one region has none.
func TestMissingRegionFlags(t *testing.T) {
	web, _ := regionTestWeb(t)
	switchDB, localDB := web.state.get()
	switchDB.TitlesMap["0100000000060"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000060000", Name: "Known Game", Region: "KR"}}
	web.state.set(switchDB, localDB)
	filter := &TitleItemFilter{WithMissing: "0"}
	filter.Normalize()
	cardOf := func() TitleItem {
		items, _, _ := web.getLibraryWithFacets(filter, "en")
		for _, item := range items {
			if item.Id == "0100000000010000" || item.Id == "0100000000050000" {
				return item
			}
		}
		t.Fatal("no card of the game")
		return TitleItem{}
	}
	if card := cardOf(); card.MissingFlags != nil {
		t.Fatalf("each region apart: no flags of other regions: %+v", card)
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.CountRegionsOnce = true })
	web.invalidateDerived()
	if card := cardOf(); len(card.OtherFlags) != 1 || len(card.MissingFlags) != 1 || card.MissingFlags[0] != "KR" {
		t.Fatalf("one copy lit, Korea dimmed: %+v %+v", card.OtherFlags, card.MissingFlags)
	}
	// with the games you do not have: no card of Korea, which the card of the game shows dimmed
	everything := &TitleItemFilter{}
	everything.Normalize()
	items, _, _ := web.getLibraryWithFacets(everything, "en")
	for _, item := range items {
		if item.Id == "0100000000060000" {
			t.Fatal("no card of a region of a game you have")
		}
	}
	// two American games of that name: they are not copies of one game
	switchDB.TitlesMap["0100000000070"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000070000", Name: "Known Game", Region: "US"}}
	web.invalidateDerived()
	if card := cardOf(); len(card.MissingFlags) != 0 {
		t.Fatalf("a name of two games: %+v", card.MissingFlags)
	}
}

// A flag is a country: titles of one country in other languages ("US (Spanish)") add none, and
// a name of several titles of one country adds no dimmed flags; a cover shows four at most.
func TestRegionFlagsByCountry(t *testing.T) {
	if regionCode("US (Spanish)") != "US" || regionCode(" jp ") != "JP" || regionCode("") != "" {
		t.Fatal("regionCode")
	}
	web, _ := regionTestWeb(t)
	switchDB, localDB := web.state.get()
	for i, region := range []string{"KR", "GB", "HK", "AU"} {
		prefix := fmt.Sprintf("01000000000%d0", 6+i)
		switchDB.TitlesMap[prefix] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: prefix + "000", Name: "Known Game", Region: region}}
	}
	// the same game in English and in Spanish for America: no flags of other regions
	switchDB.TitlesMap["0100000000110"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000110000", Name: "Pokémon Game", Region: "US (English)"}}
	switchDB.TitlesMap["0100000000120"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000120000", Name: "Pokémon Game", Region: "US (Spanish)"}}
	web.state.set(switchDB, localDB)
	if regions, ok := web.buildRegionsByName()[regionalName("Pokémon Game")]; ok {
		t.Fatalf("one country twice: %v", regions)
	}
	if regions := web.buildRegionsByName()[regionalName("Known Game")]; strings.Join(regions, ",") != "AU,GB,HK,JP,KR,US" {
		t.Fatalf("by country: %v", regions)
	}

	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.CountRegionsOnce = true })
	filter := &TitleItemFilter{WithMissing: "0"}
	filter.Normalize()
	items, _, _ := web.getLibraryWithFacets(filter, "en")
	for _, item := range items {
		if item.Id != "0100000000010000" {
			continue
		}
		if strings.Join(item.OtherFlags, ",") != "JP" || strings.Join(item.MissingFlags, ",") != "AU,GB" || strings.Join(item.HiddenRegions, ",") != "HK,KR" {
			t.Fatalf("four flags at most, the others on hover: %v %v %v", item.OtherFlags, item.MissingFlags, item.HiddenRegions)
		}
		return
	}
	t.Fatalf("no card of the game: %+v", items)
}
