package web

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Favorites marked at the same time are all on the disk: a save that read the favorites
// before another change never writes after the save that has it.
func TestFavoritesMarkedAtOnceAreAllSaved(t *testing.T) {
	web := newTestWeb(t)
	store := web.favorites()
	var wait sync.WaitGroup
	for i := 0; i < 40; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			store.set(fmt.Sprintf("01000000000%03X000", i), true)
		}(i)
	}
	wait.Wait()
	reloaded := &favoriteStore{path: filepath.Join(web.dataFolder, FAVORITES_FILENAME)}
	reloaded.reload()
	if got := len(reloaded.snapshot()); got != 40 {
		t.Fatalf("%d favorites on the disk, want 40", got)
	}

	// the same for the stores of IGDB
	times := web.timesToBeat()
	for i := 0; i < 40; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			id := fmt.Sprintf("01000000000%03X000", i)
			times.mutex.Lock()
			times.games[id] = TimeToBeat{Found: true, Checked: time.Now()}
			times.mutex.Unlock()
			times.save()
		}(i)
	}
	wait.Wait()
	web.timeToBeatOnce = sync.Once{}
	if got := len(web.timesToBeat().snapshot()); got != 40 {
		t.Fatalf("%d times to beat on the disk, want 40", got)
	}
}
