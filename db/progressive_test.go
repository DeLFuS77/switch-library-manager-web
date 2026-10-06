package db

import (
	"sync"
	"testing"
)

type partialRecorder struct {
	mutex sync.Mutex
	games []int
}

func (r *partialRecorder) UpdateProgress(int, int, string) {}

func (r *partialRecorder) PartialLibrary(titles map[string]*SwitchGameFiles, skipped map[ExtendedFileInfo]SkippedFile, files int) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.games = append(r.games, len(titles))
	// the copy must not change while the scan goes on
	for _, title := range titles {
		title.Updates[-1] = SwitchFileInfo{}
	}
}

func TestSlowScansShowTheGamesFoundSoFar(t *testing.T) {
	saved := partialLibraryInterval
	partialLibraryInterval = 0
	defer func() { partialLibraryInterval = saved }()

	folder := fakeLibrary(t, 1000)
	manager, err := NewLocalSwitchDBManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	recorder := &partialRecorder{}
	localDB, err := manager.CreateLocalSwitchFilesDB(nil, t.TempDir(), []string{folder}, recorder, true, true)
	if err != nil || len(localDB.TitlesMap) != 1000 {
		t.Fatalf("scan: %v %v", err, len(localDB.TitlesMap))
	}
	// 3000 files in chunks of 1000: two partial libraries before the whole one
	if len(recorder.games) != 2 || recorder.games[0] == 0 || recorder.games[0] >= recorder.games[1] || recorder.games[1] >= 1000 {
		t.Fatalf("partial libraries: %v", recorder.games)
	}
	for _, title := range localDB.TitlesMap {
		if _, ok := title.Updates[-1]; ok {
			t.Fatal("changing a partial library must not change the scan")
		}
	}
}
