package web

import (
	"errors"
	"fmt"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
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
// update, one that holds an update already (merged or optimized before), a game in a folder and a
// SuperNSP with both are not.
func TestOptimizeFiles(t *testing.T) {
	if files := optimizeFiles(optimizeGame(0, "g.nsz", "u.nsp")); len(files) != 2 || files[0].FileName != "g.nsz" || files[1].FileName != "u.nsp" {
		t.Fatalf("game and update: %v", files)
	}
	for name, game := range map[string]*db.SwitchGameFiles{
		"no update":       optimizeGame(0, "g.nsp", ""),
		"merged before":   optimizeGame(65536, "g.nsp", "u.nsp"),
		"not a game file": optimizeGame(0, "g.zip", "u.nsp"),
		"nil":             nil,
	} {
		if files := optimizeFiles(game); files != nil {
			t.Errorf("%s: %v", name, files)
		}
	}
	super := optimizeGame(0, "pack.nsp", "pack.nsp")
	super.Updates[65536] = db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "pack.nsp", BaseFolder: "/games"}}
	if files := optimizeFiles(super); files != nil {
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
	texts := []string{OPTIMIZE_ACTION, tasklog.PHASE_OPTIMIZING, NOTE_OPTIMIZE_FAILED, NOTE_OPTIMIZE_EXISTS, NOTE_OPTIMIZE_NO_PAIR, NOTE_OPTIMIZE_NOT_BUILT, "Select the games to optimize."}
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
