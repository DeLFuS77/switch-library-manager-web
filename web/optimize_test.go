package web

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/internal/testnsp"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
	"github.com/dtrunk90/switch-library-manager-web/switchfs/optimize"
)

func optimizeGame(baseVersion int, base string, update string) *db.SwitchGameFiles {
	g := &db.SwitchGameFiles{
		BaseExist: true,
		File: db.SwitchFileInfo{
			ExtendedInfo: db.ExtendedFileInfo{FileName: base, BaseFolder: "/games", Size: 100},
			Metadata:     &switchfs.ContentMetaAttributes{TitleId: "0100000000010000", Version: baseVersion},
		},
		Updates: map[int]db.SwitchFileInfo{},
	}
	if update != "" {
		g.Updates[65536] = db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: update, BaseFolder: "/games/updates", Size: 50}}
		g.LatestUpdate = 65536
	}
	return g
}

// A game is optimized from its base file and the file of its newest update; a game without an
// update, a game in a folder and a SuperNSP with both are not.
func TestOptimizeFiles(t *testing.T) {
	from := func(g *db.SwitchGameFiles) []db.ExtendedFileInfo {
		if g == nil || g.File.Metadata == nil || g.File.Metadata.Version != 0 {
			return nil
		}
		return optimizeFilesFrom(g, g.File.ExtendedInfo)
	}
	if files := from(optimizeGame(0, "g.nsz", "u.nsp")); len(files) != 2 || files[0].FileName != "g.nsz" || files[1].FileName != "u.nsp" {
		t.Fatalf("game and update: %v", files)
	}
	for name, game := range map[string]*db.SwitchGameFiles{
		"no update":       optimizeGame(0, "g.nsp", ""),
		"not a game file": optimizeGame(0, "g.zip", "u.nsp"),
		"nil":             nil,
	} {
		if files := from(game); files != nil {
			t.Errorf("%s: %v", name, files)
		}
	}
	super := optimizeGame(0, "pack.nsp", "pack.nsp")
	super.Updates[65536] = db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "pack.nsp", BaseFolder: "/games"}}
	if files := from(super); files != nil {
		t.Errorf("a SuperNSP: %v", files)
	}
	if got := optimizeName("Game: One", "0100000000010000", optimizeGame(0, "g.nsp", "u.nsp")); got != safeFileName("Game: One")+" [0100000000010000][v65536] (optimized).nsp" {
		t.Errorf("named %q", got)
	}
}

// Each failure is told with its own note, and every text of the optimization is in every
// language.
func TestOptimizeNotesAndTexts(t *testing.T) {
	for err, want := range map[error]string{
		errCompressSpace:                           NOTE_COMPRESS_SPACE,
		errOptimizeExists:                          NOTE_OPTIMIZE_EXISTS,
		optimize.ErrNotBuilt:                       NOTE_OPTIMIZE_NOT_BUILT,
		fmt.Errorf("x: %w", optimize.ErrNotPair{}): NOTE_OPTIMIZE_NO_PAIR,
		errors.New("anything else"):                NOTE_OPTIMIZE_FAILED,
	} {
		if got := optimizeNote(err); got != want {
			t.Errorf("%v: %q, not %q", err, got, want)
		}
	}
	texts := []string{OPTIMIZE_ACTION, tasklog.PHASE_OPTIMIZING, NOTE_OPTIMIZE_FAILED, NOTE_OPTIMIZE_EXISTS, NOTE_OPTIMIZE_NO_PAIR, NOTE_OPTIMIZE_NOT_BUILT, "Select the games to optimize.", "Optimize the games", "update v%v, merged into the game", "Install it with NXDock or DBI, with sigpatches and firmware 12.0.0 or newer."}
	for lang, table := range i18n.Translations {
		for _, text := range texts {
			if table[text] == "" {
				t.Errorf("%s: no text for %q", lang, text)
			}
		}
	}
	if taskKindNames[tasklog.TASK_OPTIMIZE] != OPTIMIZE_ACTION || !pausableKinds[tasklog.TASK_OPTIMIZE] || !queueKinds[tasklog.TASK_OPTIMIZE] {
		t.Error("the optimization is not a task of files like the others")
	}
}

// A game optimized before, kept in the library beside its original: a newer update is optimized
// again from the original (version 0), found among the bases of another version the library keeps,
// and the game page offers it.
func TestOptimizeAgainFromTheOriginal(t *testing.T) {
	web, path, _ := compressWebWithLibrary(t)
	web.HandleTitle()
	folder := filepath.Dir(path)
	_, library := web.state.get()
	local := library.TitlesMap["0100000000010"]
	update := db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "Game [0100000000010800][v131072].nsp", BaseFolder: folder}, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000010800", Version: 131072}}
	local.Updates[131072] = update
	local.LatestUpdate = 131072
	// the original as a dump holds it, with its metadata: version 0 of the game
	original := db.ExtendedFileInfo{FileName: "Game [0100000000010000][v0].nsp", BaseFolder: folder}
	if _, err := testnsp.WriteMultiTitleNsp(filepath.Join(folder, original.FileName), []testnsp.Title{{Id: 0x0100000000010000, Type: switchfs.ContentMetaType_Application, Content: "the original game "}}); err != nil {
		t.Fatal(err)
	}
	local.File = db.SwitchFileInfo{ExtendedInfo: original, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000010000"}}
	if files := web.optimizeFiles(local); len(files) != 2 || files[0] != original {
		t.Fatalf("the game itself: %v", files)
	}
	// optimized with the update before: the library holds the optimized game, the original beside it
	optimized := db.ExtendedFileInfo{FileName: "Game [0100000000010000][v65536] (optimized).nsp", BaseFolder: folder}
	local.File = db.SwitchFileInfo{ExtendedInfo: optimized, Metadata: &switchfs.ContentMetaAttributes{TitleId: "0100000000010000", Version: 65536}}
	library.Skipped[original] = db.SkippedFile{ReasonCode: db.REASON_DUPLICATE, ReasonText: "base file of another version (" + filepath.Join(folder, optimized.FileName) + ")", OtherVersion: true}
	web.invalidateDerived()
	files := web.optimizeFiles(local)
	if len(files) != 2 || files[0] != original || files[1] != update.ExtendedInfo {
		t.Fatalf("again from the original: %v", files)
	}
	if game, _ := web.optimizeGame(filepath.Join(original.BaseFolder, original.FileName)); game != local {
		t.Fatal("the original does not lead to its game")
	}
	if target, sources, _, err := web.optimizeTarget(filepath.Join(original.BaseFolder, original.FileName), "Game"); err != nil || len(sources) != 2 || !strings.HasSuffix(target, "Game [0100000000010000][v131072] (optimized).nsp") {
		t.Fatalf("target %s, %v, %v", target, sources, err)
	}
	page := httptest.NewRecorder()
	web.router.ServeHTTP(page, httptest.NewRequest("GET", "/title/0100000000010000.html", nil))
	if optimize.Built && !strings.Contains(page.Body.String(), `data-optimize="0100000000010000"`) {
		t.Fatalf("the game page offers to optimize it again: %d", page.Code)
	}
	// with no original left, nothing is offered
	delete(library.Skipped, original)
	web.invalidateDerived()
	if files := web.optimizeFiles(local); files != nil {
		t.Fatalf("no original: %v", files)
	}
}
