package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The files that are not games are deleted from Issues only when nothing else may need them
// (#197): not a piece of a game or a mod, not keys, not a file still being written, not a link.
func TestNonGameRefusal(t *testing.T) {
	web := newTestWeb(t)
	dir := t.TempDir()
	now := time.Now()
	write := func(path string, age time.Duration) string {
		t.Helper()
		full := filepath.Join(dir, path)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(full, now.Add(-age), now.Add(-age))
		return full
	}
	old := time.Hour
	cases := map[string]string{
		write("Game/cover.jpg", old):                      "",
		write("notes/readme.txt", old):                    "",
		write("archives/pack.zip", old):                   "",
		write("Game/prod.keys", old):                      NON_GAME_PART_OF_GAME,
		write("Game/ticket.tik", old):                     NON_GAME_PART_OF_GAME,
		write("Game/BOOT0.bin", old):                      NON_GAME_PART_OF_GAME,
		write("hb/tool.nro", old):                         NON_GAME_PART_OF_GAME,
		write("Game/abc.cnmt.xml", old):                   NON_GAME_PART_OF_GAME,
		write("mods/x/romfs/data.arc", old):               NON_GAME_PART_OF_GAME,
		write("sd/atmosphere/contents/0100/x.txt", old):   NON_GAME_PART_OF_GAME,
		write("downloads/Game.nsp.part", time.Minute):     NON_GAME_RECENT,
		write("Extracted/control.xml", old):               NON_GAME_PART_OF_GAME,
		write(db.QUARANTINE_FOLDER+"/old/cover.png", old): NON_GAME_LINK,
	}
	// an extracted game: the other files beside its NCA belong to it
	write("Extracted/Main.nca", old)
	for path, want := range cases {
		if got := web.nonGameRefusal(path, now); got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
	if got := web.nonGameRefusal(filepath.Join(dir, "gone.txt"), now); got == "" {
		t.Error("a file that is gone is not offered")
	}
}

func TestDeleteNonGameFiles(t *testing.T) {
	web, card, _ := keepWeb(t)
	t.Cleanup(func() {
		for deadline := 0; web.state.IsSynchronizing() && deadline < 200; deadline++ {
			waitShort()
		}
	})
	dir := filepath.Dir(card)
	old := time.Now().Add(-time.Hour)
	_, library := web.state.get()
	add := func(name string, mtime time.Time) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("some data"), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(path, mtime, mtime)
		library.Skipped[db.ExtendedFileInfo{FileName: name, BaseFolder: dir, Size: 9}] = db.SkippedFile{ReasonCode: db.REASON_UNSUPPORTED_TYPE, ReasonText: "file type is not supported"}
		return path
	}
	cover, notes, keys, copying := add("cover.jpg", old), add("notes.txt", old), add("prod.keys", old), add("Game.nsp.part", time.Now())
	web.invalidateDerived()

	// the page: a Delete on the files that can go, the reason on the others, and the window
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest("GET", "/issues.html", nil))
	body := page.Body.String()
	if !strings.Contains(body, `data-issue-delete-file="`+cover+`"`) || strings.Contains(body, `data-issue-delete-file="`+keys+`"`) || strings.Contains(body, `data-issue-delete-file="`+copying+`"`) {
		t.Fatalf("Delete only on the files that can go: %d", page.Code)
	}
	if !strings.Contains(body, NON_GAME_PART_OF_GAME) || !strings.Contains(body, NON_GAME_RECENT) || !strings.Contains(body, "data-non-game-choose") || !strings.Contains(body, `href="/settings.html#ignored"`) {
		t.Fatal("the reasons, the window to choose and the link to the ignored types")
	}

	// the list of the window: nothing protected
	list := httptest.NewRecorder()
	web.router.ServeHTTP(list, httptest.NewRequest("GET", "/issues/non-game-files", nil))
	var listed struct {
		Types   []NonGameType
		LeftOut int
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	paths := []string{}
	for _, group := range listed.Types {
		for _, file := range group.Files {
			paths = append(paths, file.Path)
		}
	}
	if len(paths) != 2 || listed.LeftOut != 2 {
		t.Fatalf("the files offered: %v, %d left out", paths, listed.LeftOut)
	}

	// only the files offered go, whatever is asked
	if response := postForm(web, "/issues/delete-non-game", url.Values{"path": {keys, copying, card}}); response.Code != http.StatusConflict {
		t.Fatalf("nothing protected is deleted: %d", response.Code)
	}
	if response := postForm(web, "/issues/delete-file", url.Values{"path": {keys}}); response.Code != http.StatusBadRequest {
		t.Fatalf("not one by one either: %d", response.Code)
	}
	if response := postForm(web, "/issues/delete-non-game", url.Values{"path": {cover, keys}}); response.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", response.Code, response.Body.String())
	}
	for path, gone := range map[string]bool{cover: true, notes: false, keys: false, copying: false, card: false} {
		if _, err := os.Stat(path); os.IsNotExist(err) != gone {
			t.Errorf("%s gone: %v, want %v", path, !gone, gone)
		}
	}
}

func TestDeleteNonGameNeedsPaths(t *testing.T) {
	web, _, _ := keepWeb(t)
	if response := postForm(web, "/issues/delete-non-game", url.Values{}); response.Code != http.StatusBadRequest {
		t.Fatalf("%d", response.Code)
	}
	recorder := httptest.NewRecorder()
	web.router.ServeHTTP(recorder, httptest.NewRequest("GET", "/issues/delete-non-game", nil))
	if recorder.Code == http.StatusOK {
		t.Fatal("deleting is a POST")
	}
}

func TestWishlistAndNonGameTextsAreTranslated(t *testing.T) {
	texts := []string{NON_GAME_PART_OF_GAME, NON_GAME_RECENT, NON_GAME_LINK, "Games you want and do not have yet.", "The wishlist as JSON, for other tools",
		"Choose files to delete…", "Choose the files to delete", "What is never offered", "Nothing chosen", "%v files, %v", "Delete %v files",
		"No file can be deleted from here now.", "%v other files are not offered.", "None of these files can be deleted now: they changed, are in use or are gone.",
		"Move “%v” (%v) to the quarantine? You can put it back from Settings, Quarantine.", "They go to the quarantine, where they can be put back.", "They cannot be recovered.",
		"Nothing is ticked: tick the files you do not want.", "1 file", "1 file, %v", "Delete 1 file"}
	for _, text := range texts {
		for _, lang := range i18n.SupportedLanguages {
			if _, ok := i18n.Translations[lang][text]; !ok && lang != i18n.DEFAULT_LANGUAGE {
				t.Errorf("no %s translation for %q", lang, text)
			}
		}
	}
}

// A library kept in a folder named like one of a mod or an SD card (D:\Switch\Games) still has
// its files that are not games offered: only the folders inside the library count (#197).
func TestNonGameRefusalLibraryFolderName(t *testing.T) {
	web := newTestWeb(t)
	library := filepath.Join(t.TempDir(), "Switch", "Contents", "Games")
	old := time.Now().Add(-time.Hour)
	write := func(path string) string {
		t.Helper()
		full := filepath.Join(library, path)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(full, old, old)
		return full
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.Folder = library })
	cases := map[string]string{
		write("cover.jpg"):             "",
		write("Game/notes.txt"):        "",
		write("sd/atmosphere/x/a.txt"): NON_GAME_PART_OF_GAME,
		write("mods/x/romfs/data.arc"): NON_GAME_PART_OF_GAME,
	}
	for path, want := range cases {
		if got := web.nonGameRefusal(path, time.Now()); got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
}
