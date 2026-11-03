package web

import (
	"slices"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// ownedGame is a base game of the library, for the tests.
func ownedGame(id string) *db.SwitchGameFiles {
	return &db.SwitchGameFiles{BaseExist: true, File: db.SwitchFileInfo{Metadata: &switchfs.ContentMetaAttributes{TitleId: id}},
		Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}}
}

func itemNames(items []TitleItem) []string {
	names := []string{}
	for _, item := range items {
		names = append(names, item.Name)
	}
	slices.Sort(names)
	return names
}

// Every list finds the games written as typed, and forgives typing mistakes only when there is
// none: "mario" finds the Mario games, not Marimo nor Bone Marrow.
func TestListsForgiveTyposOnlyWhenNothingIsWritten(t *testing.T) {
	web := newTestWeb(t)
	title := func(id string, name string) *db.SwitchTitle {
		return &db.SwitchTitle{Attributes: db.TitleAttributes{Id: id, Name: name}}
	}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{
		"0100000000010": title("0100000000010000", "Super Mario Odyssey"),
		"0100000000020": title("0100000000020000", "Marimo"),
		"0100000000030": title("0100000000030000", "The Legend of Zelda: Breath of the Wild"),
		"0100000000040": title("0100000000040000", "Mario Kart 8 Deluxe"),
		"0100000000050": title("0100000000050000", "Bone Marrow"),
	}}, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{
		"0100000000010": ownedGame("0100000000010000"),
		"0100000000020": ownedGame("0100000000020000"),
		"0100000000030": ownedGame("0100000000030000"),
	}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}})

	library := func(keyword string, missing string) []string {
		filter := &TitleItemFilter{Keyword: keyword, WithMissing: missing}
		filter.Normalize()
		items, _, _ := web.getLibraryWithFacets(filter, "en")
		return itemNames(items)
	}
	missing := func(keyword string) []string {
		filter := &TitleItemFilter{Keyword: keyword}
		filter.Normalize()
		items, _, _ := web.getMissingGamesWithFacets(filter, "en")
		return itemNames(items)
	}
	for _, check := range []struct {
		list string
		got  []string
		want []string
	}{
		{"library", library("mario", ""), []string{"Mario Kart 8 Deluxe", "Super Mario Odyssey"}},
		{"my games", library("mario", "0"), []string{"Super Mario Odyssey"}},
		{"typing mistake", library("zelda brth", "0"), []string{"The Legend of Zelda: Breath of the Wild"}},
		{"missing", missing("mario"), []string{"Mario Kart 8 Deluxe"}},
		{"missing with a typing mistake", missing("marow"), []string{"Bone Marrow"}},
	} {
		if !slices.Equal(check.got, check.want) {
			t.Errorf("%s: %v, want %v", check.list, check.got, check.want)
		}
	}

	if got := quickLabels(web.quickSearch("mario", "en", true).Games); !slices.Equal(got, []string{"Super Mario Odyssey"}) {
		t.Errorf("quick search: %v", got)
	}

	now := time.Date(2026, 10, 6, 15, 0, 0, 0, time.Local)
	upcoming := []UpcomingGame{
		{Item: TitleItem{Id: "0100000000060000", Name: "Mario Party Jamboree", ReleaseDate: now.AddDate(0, 1, 0)}},
		{Item: TitleItem{Id: "0100000000070000", Name: "Marimo Party", ReleaseDate: now.AddDate(0, 1, 0)}},
	}
	if page := upcomingPage(upcoming, &TitleItemFilter{Keyword: "mario"}, now); page.Shown != 1 || page.Months[0].Games[0].Item.Name != "Mario Party Jamboree" {
		t.Errorf("upcoming: %d shown", page.Shown)
	}
}
