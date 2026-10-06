package db

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// fakeLibrary creates files named like a library, identified by their names.
func fakeLibrary(b testing.TB, games int) string {
	b.Helper()
	folder := b.TempDir()
	for i := 0; i < games; i++ {
		sub := filepath.Join(folder, fmt.Sprintf("folder %d", i%50))
		os.MkdirAll(sub, 0755)
		prefix := fmt.Sprintf("0100%09X", i)
		for _, name := range []string{
			fmt.Sprintf("Game %d [%s000][v0].nsp", i, prefix),
			fmt.Sprintf("Game %d [%s800][v65536].nsp", i, prefix),
			fmt.Sprintf("Game %d DLC [%s001][v0].nsp", i, prefix),
		} {
			os.WriteFile(filepath.Join(sub, name), []byte("x"), 0644)
		}
	}
	return folder
}

// a scan from scratch of 3000 files identified by name
func BenchmarkScanLibrary(b *testing.B) {
	folder := fakeLibrary(b, 1000)
	manager, err := NewLocalSwitchDBManager(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer manager.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		localDB, err := manager.CreateLocalSwitchFilesDB(nil, b.TempDir(), []string{folder}, nil, true, true)
		if err != nil || len(localDB.TitlesMap) != 1000 {
			b.Fatalf("scan: %v %v", err, len(localDB.TitlesMap))
		}
	}
}

// Reading real files needs keys and games, so it only runs with
// SLM_BENCH_DATA (a data folder whose settings point to prod.keys) and SLM_BENCH_ROMS.
func BenchmarkReadRealMetadata(b *testing.B) {
	dataFolder, roms := os.Getenv("SLM_BENCH_DATA"), os.Getenv("SLM_BENCH_ROMS")
	if dataFolder == "" || roms == "" {
		b.Skip("set SLM_BENCH_DATA and SLM_BENCH_ROMS")
	}
	if _, err := settings.InitSwitchKeys(dataFolder); err != nil {
		b.Fatal(err)
	}
	entries, _ := os.ReadDir(roms)
	files := []string{}
	for _, entry := range entries {
		if strings.HasSuffix(strings.ToLower(entry.Name()), ".nsp") {
			files = append(files, filepath.Join(roms, entry.Name()))
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, file := range files {
			if _, err := switchfs.ReadNspMetadata(file); err != nil {
				b.Fatalf("%s: %v", filepath.Base(file), err)
			}
		}
	}
	b.ReportMetric(float64(b.Elapsed().Microseconds())/float64(b.N*len(files))/1000, "ms/file")
}

// Scans copies (hard links) of a real game, with 1 and with 4 workers. Needs the same
// variables as BenchmarkReadRealMetadata; SLM_BENCH_GAME is the game to link.
func BenchmarkScanRealFiles(b *testing.B) {
	dataFolder, game := os.Getenv("SLM_BENCH_DATA"), os.Getenv("SLM_BENCH_GAME")
	if dataFolder == "" || game == "" {
		b.Skip("set SLM_BENCH_DATA and SLM_BENCH_GAME")
	}
	if _, err := settings.InitSwitchKeys(dataFolder); err != nil {
		b.Fatal(err)
	}
	folder := b.TempDir()
	for i := 0; i < 40; i++ {
		if err := os.Link(game, filepath.Join(folder, fmt.Sprintf("copy %d.nsp", i))); err != nil {
			b.Skip("hard links are not supported here: ", err)
		}
	}
	for _, workers := range []int{1, 4} {
		b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
			scanWorkersOverride = workers
			defer func() { scanWorkersOverride = 0 }()
			for i := 0; i < b.N; i++ {
				// a new cache every time, so every file is read
				manager, err := NewLocalSwitchDBManager(b.TempDir())
				if err != nil {
					b.Fatal(err)
				}
				if _, err := manager.CreateLocalSwitchFilesDB(nil, b.TempDir(), []string{folder}, nil, true, true); err != nil {
					b.Fatal(err)
				}
				manager.Close()
			}
		})
	}
}

// caching the metadata of 500 new files: one transaction per file, as before, or one for all
func BenchmarkCacheWrites(b *testing.B) {
	value := map[string]*switchfs.ContentMetaAttributes{"0100000000010000": {TitleId: "0100000000010000", Version: 65536}}
	for _, batch := range []bool{false, true} {
		b.Run(fmt.Sprintf("batch=%v", batch), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				pdb, err := NewPersistentDB(b.TempDir())
				if err != nil {
					b.Fatal(err)
				}
				entries := map[string]interface{}{}
				for f := 0; f < 500; f++ {
					entries[fmt.Sprintf("file %d", f)] = value
				}
				if batch {
					pdb.AddEntries(DB_TABLE_FILE_SCAN_METADATA, entries)
				} else {
					for key, v := range entries {
						pdb.AddEntry(DB_TABLE_FILE_SCAN_METADATA, key, v)
					}
				}
				pdb.Close()
			}
		})
	}
}

// parsing the real titles database, as on every start; SLM_BENCH_DATA holds titles.json
func BenchmarkCreateSwitchTitleDB(b *testing.B) {
	dataFolder := os.Getenv("SLM_BENCH_DATA")
	if dataFolder == "" {
		b.Skip("set SLM_BENCH_DATA")
	}
	for i := 0; i < b.N; i++ {
		titles, err := os.Open(filepath.Join(dataFolder, "titles.json"))
		if err != nil {
			b.Skip(err)
		}
		versions, _ := os.Open(filepath.Join(dataFolder, "versions.json"))
		if _, err := CreateSwitchTitleDB(titles, versions); err != nil {
			b.Fatal(err)
		}
		titles.Close()
		versions.Close()
	}
}
