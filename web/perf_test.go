package web

import (
	"fmt"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// largeDatabases builds a catalog and a library of the given sizes, like a big collection.
func largeDatabases(games int, catalog int) (*db.SwitchTitlesDB, *db.LocalSwitchFilesDB) {
	switchDB := &db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}
	localDB := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}}

	for i := 0; i < catalog; i++ {
		prefix := fmt.Sprintf("0100%09X", i)
		id := prefix + "000"
		title := &db.SwitchTitle{
			Attributes: db.TitleAttributes{Id: id, Name: fmt.Sprintf("Game %d", i), Region: "US", ReleaseDate: 20200101},
			Updates:    map[int]string{65536: "2021-01-01", 131072: "2022-01-01"},
			Dlc:        map[string]db.TitleAttributes{},
		}
		for d := 1; d <= 3; d++ {
			dlcId := fmt.Sprintf("%s%03X", prefix, 0x1000+d)
			title.Dlc[dlcId] = db.TitleAttributes{Id: dlcId, Name: fmt.Sprintf("Game %d DLC %d", i, d)}
		}
		switchDB.TitlesMap[prefix] = title

		if i >= games {
			continue
		}
		file := func(name string) db.ExtendedFileInfo {
			return db.ExtendedFileInfo{FileName: name, BaseFolder: "/roms", Size: 1 << 30}
		}
		local := &db.SwitchGameFiles{
			BaseExist: true,
			File:      db.SwitchFileInfo{ExtendedInfo: file(fmt.Sprintf("Game %d [%s][v0].nsp", i, id)), Metadata: &switchfs.ContentMetaAttributes{TitleId: id, Type: "Base"}},
			Updates:   map[int]db.SwitchFileInfo{},
			Dlc:       map[string]db.SwitchFileInfo{},
		}
		if i%2 == 0 {
			updateId := prefix + "800"
			local.Updates[65536] = db.SwitchFileInfo{ExtendedInfo: file(fmt.Sprintf("Game %d [%s][v65536].nsp", i, updateId)), Metadata: &switchfs.ContentMetaAttributes{TitleId: updateId, Version: 65536, Type: "Update"}}
			local.LatestUpdate = 65536
		}
		dlcId := fmt.Sprintf("%s%03X", prefix, 0x1001)
		local.Dlc[dlcId] = db.SwitchFileInfo{ExtendedInfo: file(fmt.Sprintf("Game %d DLC [%s][v0].nsp", i, dlcId)), Metadata: &switchfs.ContentMetaAttributes{TitleId: dlcId, Type: "DLC"}}
		localDB.TitlesMap[prefix] = local
		if i%10 == 0 {
			localDB.Skipped[file(fmt.Sprintf("junk %d.txt", i))] = db.SkippedFile{ReasonText: "file type is not supported"}
		}
	}
	localDB.NumFiles = games * 3
	return switchDB, localDB
}

func benchWeb(b *testing.B) *Web {
	b.Helper()
	web := &Web{dataFolder: b.TempDir()}
	web.sugarLogger = newTestWeb(&testing.T{}).sugarLogger
	web.state.set(largeDatabases(5000, 20000))
	return web
}

// every page renders the navigation counts
func BenchmarkNavCounts(b *testing.B) {
	web := benchWeb(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		web.globalPageData("index")
	}
}

func BenchmarkLibraryPage(b *testing.B) {
	web := benchWeb(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		web.getLibraryWithFacets(defaultFilter(), "en")
		web.globalPageData("index")
	}
}

func BenchmarkUpdatesPage(b *testing.B) {
	web := benchWeb(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		web.getMissingUpdates(defaultFilter(), "en")
		web.globalPageData("updates")
	}
}

func BenchmarkMissingGamesPage(b *testing.B) {
	web := benchWeb(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		web.getMissingGames(defaultFilter(), "en")
		web.globalPageData("missing")
	}
}

func BenchmarkStatisticsPage(b *testing.B) {
	web := benchWeb(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		web.getStatistics("en")
		web.globalPageData("statistics")
	}
}

// a search walks the whole list
func BenchmarkMissingGamesSearch(b *testing.B) {
	web := benchWeb(b)
	filter := defaultFilter()
	filter.Keyword = "game 1"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		web.getMissingGames(filter, "en")
	}
}

// the lists are built and sorted again after every scan
func BenchmarkMissingGamesRebuild(b *testing.B) {
	web := benchWeb(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		web.invalidateDerived()
		web.getMissingGames(defaultFilter(), "en")
	}
}
