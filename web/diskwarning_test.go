package web

import (
	"os"
	"strings"
	"testing"
)

func TestMeasureLowDisks(t *testing.T) {
	folder := t.TempDir()
	free, total, err := diskSpace(folder)
	if err != nil || total == 0 || free > total {
		t.Fatalf("diskSpace = %v, %v, %v", free, total, err)
	}
	// every volume is short of space below 101%, and none below 0%
	if low := measureLowDisks([]string{folder, folder}, 101); len(low) != 1 || low[0].Total != int64(total) {
		t.Errorf("a volume below 101%% free: %+v", low)
	}
	if low := measureLowDisks([]string{folder}, 0); len(low) != 0 {
		t.Errorf("a volume below 0%% free: %+v", low)
	}
	if low := measureLowDisks([]string{folder + string(os.PathSeparator) + "missing"}, 101); len(low) != 0 {
		t.Errorf("a missing folder: %+v", low)
	}
}

func TestRoomToCompress(t *testing.T) {
	folder := t.TempDir()
	if !roomToCompress(folder+string(os.PathSeparator)+"game.nsp", 1<<20) {
		t.Error("no room for 1 MiB")
	}
	if roomToCompress(folder+string(os.PathSeparator)+"game.nsp", 1<<62) {
		t.Error("room for 4 EiB")
	}
}

// The header warns of a volume short of space, with its free space.
func TestLowDiskBanner(t *testing.T) {
	set, err := parseTemplates(os.DirFS(".."), "resources/layout.html", "resources/partials/*.html", "resources/pages/maintenance.html")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	data := MaintenancePageData{GlobalPageData: GlobalPageData{IsKeysFileAvailable: true, LowDisks: []LowDisk{{Folder: "/mnt/games", Free: 3 << 30, Total: 100 << 30, Percent: 3}}}}
	if err := set["es"].ExecuteTemplate(&out, "layout", data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Queda poco espacio en /mnt/games: 3") || !strings.Contains(out.String(), "(3%)") {
		t.Errorf("no warning in the header")
	}
}
