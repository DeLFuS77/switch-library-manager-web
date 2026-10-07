package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAutomaticBackupsKeepTheLastOnes(t *testing.T) {
	web := newTestWeb(t)
	os.WriteFile(filepath.Join(web.dataFolder, "settings.json"), []byte(`{}`), 0600)
	os.WriteFile(filepath.Join(web.dataFolder, FAVORITES_FILENAME), []byte(`{"0100000000010000":"2026-01-01T00:00:00Z"}`), 0600)
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if !web.autoBackupDue(start) {
		t.Fatal("without copies a copy is due")
	}
	for week := 0; week < 7; week++ {
		now := start.AddDate(0, 0, 7*week)
		if err := web.makeAutoBackup(now); err != nil {
			t.Fatal(err)
		}
		// the files are dated by when they were written
		name := filepath.Join(web.autoBackupFolder(), "slm-backup-"+now.Format("2006-01-02")+".zip")
		os.Chtimes(name, now, now)
	}
	backups := web.autoBackups()
	if len(backups) != autoBackupsKept || backups[0].Name != "slm-backup-2026-02-12.zip" {
		t.Fatalf("the last %d copies, newest first: %+v", autoBackupsKept, backups)
	}
	if web.autoBackupDue(start.AddDate(0, 0, 45)) || !web.autoBackupDue(start.AddDate(0, 0, 49)) {
		t.Fatal("a copy every week")
	}
	data, _ := os.ReadFile(filepath.Join(web.autoBackupFolder(), backups[0].Name))
	files, err := readBackup(data)
	if err != nil || len(files[FAVORITES_FILENAME]) == 0 {
		t.Fatalf("the copy holds the configuration: %v %v", err, files)
	}
	for _, name := range []string{"../settings.json", "slm-backup-2026-02-12.zip/../../x", "settings.json", "slm-backup-1999-01-01.zip"} {
		if web.autoBackupPath(name) != "" {
			t.Errorf("%q must not be served", name)
		}
	}
}

func TestReadOnlyUsersCannotDownloadBackups(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/backup/auto/slm-backup-2026-01-01.zip", nil)
	if viewerAllowed(request) {
		t.Fatal("a read-only user downloads the users and settings")
	}
}
