package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

func TestCleanupFollowsTheRulesOfTheSpacePage(t *testing.T) {
	web := newTestWeb(t)
	folder := t.TempDir()
	kept := filepath.Join(folder, "Game.nsp")
	os.WriteFile(kept, []byte("game"), 0644)
	info, _ := os.Stat(kept)
	duplicate := db.SkippedFile{ReasonCode: db.REASON_DUPLICATE, ReasonText: "duplicate base file (" + kept + ")"}

	if reason := web.cleanupRefusal(db.ExtendedFileInfo{}, duplicate); reason != NOTE_SPACE_VERIFY_KEPT {
		t.Fatalf("the copy that stays was never checked: %q", reason)
	}
	web.verifications().set(kept, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: true, Checker: verifyChecker})
	if reason := web.cleanupRefusal(db.ExtendedFileInfo{}, duplicate); reason != "" {
		t.Fatalf("the copy that stays is sound: %q", reason)
	}

	old := db.SkippedFile{ReasonCode: db.REASON_OLD_UPDATE, ReasonText: "old update file, newer update exist locally (" + kept + ")"}
	if reason := web.cleanupRefusal(db.ExtendedFileInfo{}, old); reason != "" {
		t.Fatalf("the newer update is sound: %q", reason)
	}
	web.verifications().set(kept, verifyRecord{Size: info.Size(), ModTime: info.ModTime().UnixNano(), OK: false, Checker: verifyChecker})
	if reason := web.cleanupRefusal(db.ExtendedFileInfo{}, old); reason != NOTE_SPACE_NEWER_DAMAGED {
		t.Fatalf("the newer update is damaged: %q", reason)
	}
}
