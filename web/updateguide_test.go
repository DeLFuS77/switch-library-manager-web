package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestHostFoldersFromTheMountTable(t *testing.T) {
	mountinfo := `1 0 0:30 / / rw,relatime - overlay overlay rw
2 1 0:52 /appdata/switch-library-manager-web /usr/local/share/switch-library-manager-web rw - fuse.shfs shfs rw
3 1 0:52 /Juegos\040Switch /mnt/roms rw - fuse.shfs shfs rw
4 1 259:1 /appdata/other /mnt/other rw - btrfs /dev/nvme0n1p1 rw
`
	entries := readMountInfo(strings.NewReader(mountinfo))
	if host, sure := hostFolder(entries, "/usr/local/share/switch-library-manager-web"); host != "/mnt/user/appdata/switch-library-manager-web" || !sure {
		t.Fatalf("data folder: %q %v", host, sure)
	}
	if host, sure := hostFolder(entries, "/mnt/roms/sub"); host != "/mnt/user/Juegos Switch/sub" || !sure {
		t.Fatalf("library: %q %v", host, sure)
	}
	if host, sure := hostFolder(entries, "/mnt/other"); host != "/appdata/other" || sure {
		t.Fatalf("a folder of a disk must be checked: %q %v", host, sure)
	}
	if _, sure := hostFolder(entries, "/not/mounted"); sure {
		t.Fatal("a folder of the container itself has no host folder")
	}
}

func TestUpdatePageShowsTheCommands(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleUpdateGuide()
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/update.html", nil))
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, "docker pull delfus77/switch-library-manager-web:latest") || !strings.Contains(body, "Check for Updates") {
		t.Fatalf("got %d", recorder.Code)
	}
}
