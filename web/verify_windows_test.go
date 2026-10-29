package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// openWithoutSharing opens a file the way a program that keeps others out does: no one else
// can open it until it is closed.
func openWithoutSharing(t *testing.T, path string) {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.CloseHandle(handle) })
	if file, err := os.Open(path); err == nil {
		file.Close()
		t.Fatal("the file should be in use")
	}
}

// A file another program holds open without sharing is left unchecked: it is not damaged.
func TestAFileInUseIsNotDamaged(t *testing.T) {
	web, path := compressWeb(t)
	openWithoutSharing(t, path)
	web.startVerificationOf([]string{path}, verifyRecheckAll, TRIGGER_MANUAL)
	task := waitForTask(t, web, TASK_VERIFY)
	if task.Damaged != 0 || len(task.Warnings) != 1 || task.Warnings[0].Text != NOTE_VERIFY_UNREADABLE {
		t.Fatalf("a file in use is no damage: %+v", task)
	}
	if len(web.verifications().damaged()) != 0 {
		t.Fatal("nothing is kept of it")
	}
	// nor by a compression, which reads it whole too
	web.startCompression([]string{path}, "fast", false)
	if task := waitForCompression(t, web); task.Status != TASK_FAILED || len(task.Warnings) != 1 || task.Warnings[0].Text != NOTE_COMPRESS_FAILED {
		t.Fatalf("a file in use is not compressed, and not damaged: %+v", task)
	}
	if len(web.verifications().damaged()) != 0 {
		t.Fatal("a compression keeps no damage of it")
	}
}

// The bulk deletion of the damaged files leaves a file in use: it cannot be read now.
func TestIssuesDeleteLeavesAFileInUse(t *testing.T) {
	web, _, separate := keepWeb(t)
	t.Cleanup(func() {
		for deadline := 0; web.state.IsSynchronizing() && deadline < 200; deadline++ {
			waitShort()
		}
	})
	info, _ := os.Stat(separate)
	web.verifications().set(separate, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: false, Reason: "damaged file: x", Checker: verifyChecker})
	web.invalidateDerived()
	file := []db.ExtendedFileInfo{{FileName: filepath.Base(separate), BaseFolder: filepath.Dir(separate), Size: info.Size()}}
	if files := web.stillOfNoUse(file, ISSUE_DAMAGED); len(files) != 1 {
		t.Fatalf("a damaged file that reads goes: %+v", files)
	}
	openWithoutSharing(t, separate)
	if response := postForm(web, "/issues/delete", url.Values{"kind": {ISSUE_DAMAGED}}); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"deleted":0`) {
		t.Fatalf("a file in use stays: %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(separate); err != nil {
		t.Fatal("the file stays")
	}
}
