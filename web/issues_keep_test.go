package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// keepWeb is a library with a game in a cartridge with two games and a separate copy of it.
func keepWeb(t *testing.T) (*Web, string, string) {
	t.Helper()
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	manager, err := db.NewLocalSwitchDBManager(web.dataFolder)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	web.localDbManager = manager
	dir := t.TempDir()
	cardInfo := db.ExtendedFileInfo{FileName: "Collection.xcz", BaseFolder: dir, Size: 900}
	copyInfo := db.ExtendedFileInfo{FileName: "First.nsz", BaseFolder: dir, Size: 100}
	for _, file := range []db.ExtendedFileInfo{cardInfo, copyInfo} {
		os.WriteFile(filepath.Join(dir, file.FileName), []byte("x"), 0o644)
	}
	base := func(id string, file db.ExtendedFileInfo) *db.SwitchGameFiles {
		return &db.SwitchGameFiles{BaseExist: true, File: db.SwitchFileInfo{ExtendedInfo: file, Metadata: &switchfs.ContentMetaAttributes{TitleId: id}},
			Updates: map[int]db.SwitchFileInfo{}, Dlc: map[string]db.SwitchFileInfo{}}
	}
	card, separate := filepath.Join(dir, cardInfo.FileName), filepath.Join(dir, copyInfo.FileName)
	library := &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{"0100000000030": base("0100000000030000", copyInfo), "0100000000040": base("0100000000040000", cardInfo)},
		Skipped:   map[db.ExtendedFileInfo]db.SkippedFile{cardInfo: {ReasonCode: db.REASON_DUPLICATE, ReasonText: "duplicate base file (" + separate + ")"}},
	}
	known := map[string]*db.SwitchTitle{
		"0100000000030": {Attributes: db.TitleAttributes{Name: "First"}},
		"0100000000040": {Attributes: db.TitleAttributes{Name: "Second"}},
	}
	// the card also holds the first game: the scan made it the duplicate of the separate copy
	library.TitlesMap["0100000000030"].Updates[65536] = db.SwitchFileInfo{ExtendedInfo: cardInfo, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000030800", Version: 65536}}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: known}, library)
	web.HandleIssues()
	return web, card, separate
}

func TestIssuesKeepThisCopy(t *testing.T) {
	web, card, separate := keepWeb(t)
	t.Cleanup(func() {
		for deadline := 0; web.state.IsSynchronizing() && deadline < 200; deadline++ {
			waitShort()
		}
	})
	games := web.gamesInFiles()
	if len(games[card]) != 2 || games[card][0] != "First" || games[card][1] != "Second" || games[separate] != nil {
		t.Fatalf("the games of each file: %v", games)
	}
	// with the smaller file, the separate copy stays; keeping the card changes one duplicate
	if got := web.duplicatesChanging(settings.KEEP_AUTO); got != 1 {
		t.Fatalf("the card holds several titles, it is kept instead: %d", got)
	}
	if got := web.duplicatesChanging(settings.KEEP_NSP); got != 0 {
		t.Fatalf("the NSZ is kept already: %d", got)
	}

	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest("GET", "/issues.html", nil))
	if body := page.Body.String(); !strings.Contains(body, "data-issue-keep") || !strings.Contains(body, "First, Second") {
		t.Fatalf("the row shows the button and the games of the card: %d", page.Code)
	}

	if response := postForm(web, "/issues/keep", url.Values{"path": {separate}}); response.Code != http.StatusBadRequest {
		t.Fatalf("only a duplicate can be kept: %d", response.Code)
	}
	if response := postForm(web, "/issues/keep", url.Values{"path": {card}}); response.Code != http.StatusAccepted {
		t.Fatalf("keep: %d %s", response.Code, response.Body.String())
	}
	if kept := settings.ReadSettings(web.dataFolder).OrganizeOptions.KeepFiles; len(kept) != 1 || kept[0] != card {
		t.Fatalf("the card is kept: %v", kept)
	}
	// the other copy chosen before is no longer chosen, and files gone are left out
	if got := keptChoices([]string{separate, filepath.Join(filepath.Dir(card), "gone.nsp"), card}, card, separate); len(got) != 1 || got[0] != card {
		t.Fatalf("choices: %v", got)
	}
}

func TestIssuesDeleteOneUnreadableFile(t *testing.T) {
	web, card, _ := keepWeb(t)
	t.Cleanup(func() {
		for deadline := 0; web.state.IsSynchronizing() && deadline < 200; deadline++ {
			waitShort()
		}
	})
	dir := filepath.Dir(card)
	unreadable := db.ExtendedFileInfo{FileName: "Odd.nsp", BaseFolder: dir, Size: 1}
	os.WriteFile(filepath.Join(dir, unreadable.FileName), []byte("x"), 0o644)
	_, library := web.state.get()
	library.Skipped[unreadable] = db.SkippedFile{ReasonCode: db.REASON_MALFORMED_FILE, ReasonText: "failed to read NSP [reason: invalid NCA magic]"}
	web.invalidateDerived()
	path := filepath.Join(dir, unreadable.FileName)

	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest("GET", "/issues.html", nil))
	body := page.Body.String()
	if !strings.Contains(body, `data-issue-delete-file="`+path+`"`) || strings.Contains(body, `data-issue-delete-file="`+card+`"`) {
		t.Fatalf("only the unreadable file can be deleted on its own: %d", page.Code)
	}

	// a duplicate is not deleted this way, nor a file that is not an issue
	for _, other := range []string{card, filepath.Join(dir, "First.nsz")} {
		if response := postForm(web, "/issues/delete-file", url.Values{"path": {other}}); response.Code != http.StatusBadRequest {
			t.Fatalf("%s is not deleted: %d", other, response.Code)
		}
	}
	if response := postForm(web, "/issues/delete-file", url.Values{"path": {path}}); response.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the file is deleted")
	}
	if _, err := os.Stat(card); err != nil {
		t.Fatal("the other files stay")
	}
}

// The copy chosen in Issues stays chosen once Organize moved it, and once the app compressed
// or converted it: by its old path the next scan let the settings choose again.
func TestKeptChoiceFollowsTheFile(t *testing.T) {
	web := newTestWeb(t)
	dir := t.TempDir()
	chosen, moved := filepath.Join(dir, "Game.nsp"), filepath.Join(dir, "Game", "Game [0100000000010000][v0].nsp")
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.OrganizeOptions.KeepFiles = []string{chosen, "other.nsp"} })
	web.followKeptChoices([]process.Operation{
		{Kind: process.OP_MOVE, From: chosen, To: moved},
		{Kind: process.OP_MOVE, From: "other.nsp", To: "failed.nsp", Error: "another file is moved to the same destination"},
	})
	if kept := settings.ReadSettings(web.dataFolder).OrganizeOptions.KeepFiles; len(kept) != 2 || kept[0] != moved || kept[1] != "other.nsp" {
		t.Fatalf("the copy chosen where it was moved: %v", kept)
	}

	compressed := filepath.Join(dir, "Card.xcz")
	os.WriteFile(compressed, []byte("x"), 0o644)
	newer := filepath.Join(dir, "New.nsp")
	os.WriteFile(newer, []byte("x"), 0o644)
	if got := keptChoices([]string{filepath.Join(dir, "Card.xci")}, newer, filepath.Join(dir, "Other.nsp")); len(got) != 2 || got[0] != compressed {
		t.Fatalf("a copy chosen and compressed since is kept by its new name: %v", got)
	}
}
