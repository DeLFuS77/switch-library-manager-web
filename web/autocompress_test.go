package web

import (
	"os"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

func setAutoCompress(t *testing.T, web *Web, mode string, keep bool) {
	t.Helper()
	original := *settings.ReadSettings(web.dataFolder)
	t.Cleanup(func() {
		settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
			s.AutoCompress, s.AutoCompressKeep = original.AutoCompress, original.AutoCompressKeep
		})
	})
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.AutoCompress, s.AutoCompressKeep = mode, keep })
}

func TestAutoCompressNewFiles(t *testing.T) {
	web, path, restore := compressWebWithLibrary(t)

	if web.autoCompress(TRIGGER_WATCHER) {
		t.Fatal("automatic compression is off by default")
	}
	setAutoCompress(t, web, AUTO_COMPRESS_NEW, false)
	if !web.autoCompress(TRIGGER_WATCHER) {
		t.Fatal("the new file must be compressed")
	}
	task := waitForTask(t, web, TASK_COMPRESS)
	if task.Status != TASK_SUCCESS || task.Trigger != TRIGGER_WATCHER || task.Files != 1 {
		t.Fatalf("task: %+v", task)
	}
	if _, err := os.Stat(switchfs.CompressedPath(path)); err != nil {
		t.Fatal("the NSZ must exist")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the original must be deleted")
	}
	for deadline := time.Now().Add(10 * time.Second); web.state.IsSynchronizing() && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	restore()
	if web.autoCompress(TRIGGER_WATCHER) {
		t.Fatal("nothing is left to compress")
	}
}

func TestAutoCompressDoesNotRetryFailures(t *testing.T) {
	web, path := compressWeb(t)
	setAutoCompress(t, web, AUTO_COMPRESS_NEW, false)
	data, _ := os.ReadFile(path)
	data[len(data)-50] ^= 0xFF
	os.WriteFile(path, data, 0o644)

	if !web.autoCompress(TRIGGER_WATCHER) {
		t.Fatal("the first attempt runs")
	}
	if task := waitForTask(t, web, TASK_COMPRESS); task.Status != TASK_FAILED {
		t.Fatalf("a damaged file fails: %+v", task)
	}
	if web.autoCompress(TRIGGER_WATCHER) {
		t.Fatal("a failed file is not tried again until it changes")
	}
}

func TestNightlyCompressRunsOncePerNight(t *testing.T) {
	web := newTestWeb(t)
	setAutoCompress(t, web, AUTO_COMPRESS_NIGHT, false)
	night := time.Date(2026, 10, 6, autoCompressHour, 5, 0, 0, time.Local)
	if !web.nightlyCompressDue(night) || web.nightlyCompressDue(night.Add(10*time.Minute)) {
		t.Fatal("once per night")
	}
	if web.nightlyCompressDue(night.Add(3 * time.Hour)) {
		t.Fatal("only at night")
	}
	if !web.nightlyCompressDue(night.Add(24 * time.Hour)) {
		t.Fatal("again the next night")
	}
}
