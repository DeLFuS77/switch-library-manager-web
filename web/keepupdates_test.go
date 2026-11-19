package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// keptUpdateWeb has a game with two updates in the library: the newest, v131072, and v65536, an old
// update the cleanup deletes.
func keptUpdateWeb(t *testing.T) (web *Web, old db.ExtendedFileInfo, newest string) {
	t.Helper()
	web = newTestWeb(t)
	web.embedFS = os.DirFS("..")
	folder := t.TempDir()
	write := func(name string) db.ExtendedFileInfo {
		os.WriteFile(filepath.Join(folder, name), []byte("nsp"), 0o644)
		return db.ExtendedFileInfo{FileName: name, BaseFolder: folder, Size: 3}
	}
	base := write("Game [0100000000010000][v0].nsp")
	old = write("Game [0100000000010800][v65536].nsp")
	latest := write("Game [0100000000010800][v131072].nsp")
	newest = filepath.Join(folder, latest.FileName)
	web.state.set(nil, &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{
			"0100000000010": {
				BaseExist: true,
				File:      db.SwitchFileInfo{ExtendedInfo: base, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000010000"}},
				Updates: map[int]db.SwitchFileInfo{
					65536:  {ExtendedInfo: old, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000010800", Version: 65536}},
					131072: {ExtendedInfo: latest, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000010800", Version: 131072}},
				},
				LatestUpdate: 131072,
				Dlc:          map[string]db.SwitchFileInfo{},
			},
		},
		Skipped: map[db.ExtendedFileInfo]db.SkippedFile{
			old: {ReasonCode: db.REASON_OLD_UPDATE, ReasonText: "old update file, newer update exist locally (" + newest + ")"},
		},
	})
	web.HandleIssues()
	return web, old, newest
}

func pathOf(file db.ExtendedFileInfo) string {
	return filepath.Join(file.BaseFolder, file.FileName)
}

func offeredToCompress(web *Web, path string) (bool, bool) {
	for _, candidate := range web.compressCandidates() {
		if candidate.Path == path {
			return true, candidate.KeptUpdate
		}
	}
	return false, false
}

// Keep this update keeps the version: no cleanup deletes it (Delete old updates and the
// automations, and Space), Issues no longer lists it, and Compress offers it; Stop keeping lets it
// go again (#193).
func TestKeepThisUpdate(t *testing.T) {
	web, old, _ := keptUpdateWeb(t)
	_, localDB := web.state.get()
	skipped := localDB.Skipped[old]
	if note := web.cleanupRefusal(old, skipped); note != "" {
		t.Fatalf("an old update not kept goes: %q", note)
	}
	if offered, _ := offeredToCompress(web, pathOf(old)); offered {
		t.Fatal("an old update the cleanup deletes is not offered to compress")
	}

	if code := postForm(web, "/updates/keep", url.Values{"path": {pathOf(old)}}).Code; code != http.StatusOK {
		t.Fatalf("keep: %d", code)
	}
	if kept := settings.ReadSettings(web.dataFolder).OrganizeOptions.KeepUpdates; !slices.Equal(kept, []string{"0100000000010800:65536"}) {
		t.Fatalf("kept by the ID of the update and its version: %v", kept)
	}
	if note := web.cleanupRefusal(old, skipped); note != NOTE_KEPT_BY_CHOICE {
		t.Fatalf("Delete old updates and the automations keep it: %q", note)
	}
	operations := process.CleanupLibrary(nil, old.BaseFolder, []string{old.BaseFolder}, localDB, settings.OrganizeOptions{}, true, nil, web.cleanupRefusal)
	for _, op := range operations {
		if op.Kind == process.OP_DELETE && op.From == pathOf(old) {
			t.Fatal("the cleanup must not delete a version kept")
		}
	}
	found := false
	for _, group := range web.spaceGroups() {
		for _, file := range group.Files {
			if file.Path == pathOf(old) {
				found = true
				if file.Deletable || !file.KeptUpdate || file.Note != NOTE_KEPT_BY_CHOICE || !file.Keepable {
					t.Fatalf("Space keeps it and says so: %+v", file)
				}
			}
		}
	}
	if !found {
		t.Fatal("Space lists the old update kept, to let it go")
	}
	for _, issue := range web.getIssues() {
		if issue.File == pathOf(old) {
			t.Fatal("a version kept is no issue")
		}
	}
	if offered, kept := offeredToCompress(web, pathOf(old)); !offered || !kept {
		t.Fatalf("Compress offers it, kept: %v %v", offered, kept)
	}
	// kept by its version: the same version moved, renamed or compressed is the one kept
	if !web.keptByChoice(db.ExtendedFileInfo{FileName: "Game [0100000000010800][v65536].nsz", BaseFolder: t.TempDir()}) {
		t.Fatal("the version stays kept wherever its file goes")
	}

	if code := postForm(web, "/updates/keep", url.Values{"path": {pathOf(old)}, "keep": {"false"}}).Code; code != http.StatusOK {
		t.Fatalf("stop keeping: %d", code)
	}
	if kept := settings.ReadSettings(web.dataFolder).OrganizeOptions.KeepUpdates; len(kept) != 0 {
		t.Fatalf("let go: %v", kept)
	}
	if note := web.cleanupRefusal(old, skipped); note != "" {
		t.Fatalf("let go, it goes again: %q", note)
	}
}

// Only an old update of the library is kept: not the newest update, a game or a path of the
// request that is no file of the library.
func TestKeepOnlyOldUpdates(t *testing.T) {
	web, old, newest := keptUpdateWeb(t)
	for _, path := range []string{newest, filepath.Join(old.BaseFolder, "Game [0100000000010000][v0].nsp"), "/etc/passwd", ""} {
		if code := postForm(web, "/updates/keep", url.Values{"path": {path}}).Code; code != http.StatusBadRequest {
			t.Errorf("%q: %d", path, code)
		}
	}
	if kept := settings.ReadSettings(web.dataFolder).OrganizeOptions.KeepUpdates; len(kept) != 0 {
		t.Fatalf("nothing kept: %v", kept)
	}
}

// The page of a game tells of an old update that the cleanup deletes it, and offers to keep it;
// kept, it says so and offers to let it go. Issues offers to keep it from its row.
func TestOldUpdateRowOffersToKeep(t *testing.T) {
	web, old, _ := keptUpdateWeb(t)
	web.HandleTitle()
	page := func(url string) string {
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, url, nil))
		if recorder.Code != 200 {
			t.Fatalf("%s: %d", url, recorder.Code)
		}
		return recorder.Body.String()
	}
	keep := `data-update-keep="` + pathOf(old) + `" data-keep="true"`
	body := page("/title/0100000000010000.html")
	if !strings.Contains(body, keep) || !strings.Contains(body, "Older than v131072") || strings.Count(body, "data-update-keep=") != 1 {
		t.Fatal("the old update, and only it, can be kept from the page of its game")
	}
	if !strings.Contains(page("/issues.html"), keep) {
		t.Fatal("Issues offers to keep the old update")
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.OrganizeOptions.KeepUpdates = []string{"0100000000010800:65536"}
	})
	body = page("/title/0100000000010000.html")
	if !strings.Contains(body, `data-update-keep="`+pathOf(old)+`" data-keep="false"`) || strings.Contains(body, "Older than v131072") {
		t.Fatal("kept, the page of the game offers to let it go")
	}
}

// Compress offers the old updates the cleanup keeps by itself too: here while the newer copy is
// damaged (or for their cheats); only the ones it deletes are left out.
func TestCompressOffersOldUpdatesTheCleanupKeeps(t *testing.T) {
	web, old, newest := keptUpdateWeb(t)
	info, _ := os.Stat(newest)
	web.verifications().set(newest, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: false, Checker: verifyChecker})
	web.invalidateDerived()
	if offered, kept := offeredToCompress(web, pathOf(old)); !offered || kept {
		t.Fatalf("offered while the newer copy is damaged, not as kept on purpose: %v %v", offered, kept)
	}
}

// The list of Recompress is for the ones who use Compress, as its other lists.
func TestRecompressListAccess(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/compress/recompress-list?to=blocks", nil)
	if roleAllows(ROLE_VIEWER, request) || !roleAllows(ROLE_EDITOR, request) || !roleAllows(ROLE_ADMIN, request) {
		t.Fatal("the list of Recompress: administrators and editors only")
	}
	keep := httptest.NewRequest(http.MethodPost, "/updates/keep", nil)
	if roleAllows(ROLE_VIEWER, keep) || roleAllows(ROLE_EDITOR, keep) {
		t.Fatal("only an administrator keeps an update")
	}
}

func TestKeepUpdateTextsAreTranslated(t *testing.T) {
	for _, text := range []string{NOTE_KEPT_BY_CHOICE, "Keep this update", "Stop keeping", "Kept: no cleanup deletes this version", "Older than v%v: Delete old updates and Space delete it."} {
		for _, lang := range i18n.SupportedLanguages {
			if _, ok := i18n.Translations[lang][text]; !ok && lang != i18n.DEFAULT_LANGUAGE {
				t.Errorf("no %s translation for %q", lang, text)
			}
		}
	}
}
