package web

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func zipPaths(t *testing.T, data []byte) map[string]string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range reader.File {
		rc, _ := f.Open()
		body, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = string(body)
	}
	return files
}

func TestCheatZipForEachTarget(t *testing.T) {
	web := cheatsWeb(t)
	web.downloadCheats()
	cheats := web.cheats()
	game := ownedCheat{Id: "0100000000010000", Build: "2222222222222222"}
	id, build := game.Id, game.Build

	// Switch: one file under atmosphere/, with every cheat
	var sw bytes.Buffer
	if err := writeCheatsZip(&sw, cheats, []ownedCheat{game}, cheatTargetFor("")); err != nil {
		t.Fatal(err)
	}
	files := zipPaths(t, sw.Bytes())
	path := "atmosphere/contents/" + id + "/cheats/" + build + ".txt"
	if _, ok := files[path]; !ok || len(files) != 1 || !strings.Contains(files[path], "Infinite HP") {
		t.Fatalf("Atmosphère: one file with all cheats: %v", keysOf(files))
	}

	// Ryujinx: the same single file under mods/
	var ry bytes.Buffer
	writeCheatsZip(&ry, cheats, []ownedCheat{game}, cheatTargetFor("ryujinx"))
	files = zipPaths(t, ry.Bytes())
	if _, ok := files["mods/contents/"+id+"/cheats/"+build+".txt"]; !ok || len(files) != 1 {
		t.Fatalf("Ryujinx path: %v", keysOf(files))
	}

	// Yuzu/Eden: a folder per cheat, each with the master code, none for the game header
	var yz bytes.Buffer
	writeCheatsZip(&yz, cheats, []ownedCheat{game}, cheatTargetFor("yuzu"))
	files = zipPaths(t, yz.Bytes())
	hp := "load/" + id + "/Infinite HP/cheats/" + build + ".txt"
	gold := "load/" + id + "/Max Gold/cheats/" + build + ".txt"
	if _, ok := files[hp]; !ok {
		t.Fatalf("Yuzu per-cheat folder: %v", keysOf(files))
	}
	if _, ok := files[gold]; !ok {
		t.Fatalf("Yuzu second cheat: %v", keysOf(files))
	}
	for name, body := range files {
		if strings.Contains(name, "Master Code") {
			t.Fatalf("the master code has no folder of its own: %v", keysOf(files))
		}
		if !strings.Contains(body, "Master Code") {
			t.Fatalf("each cheat carries the master code: %q", body)
		}
	}
	if len(files) != 2 {
		t.Fatalf("one folder per real cheat: %v", keysOf(files))
	}
}

func keysOf(m map[string]string) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestCheatFolderName(t *testing.T) {
	taken := map[string]bool{}
	if got := cheatFolderName(`Fire: all/the <time>`, taken); got != "Fire all the time" {
		t.Fatalf("folder name cleaned: %q", got)
	}
	if got := cheatFolderName("Fire: all/the <time>", taken); got != "Fire all the time 2" {
		t.Fatalf("a repeated name is numbered: %q", got)
	}
	if got := cheatFolderName("...", taken); got != "Cheat" {
		t.Fatalf("an empty name: %q", got)
	}
}
