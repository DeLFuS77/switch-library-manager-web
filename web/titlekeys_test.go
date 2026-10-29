package web

import (
	"os"
	"path/filepath"
	"testing"
)

// A game without its title key shows in Issues, and Compress leaves it out; once the file
// changes it is looked at again.
func TestGameWithoutItsTitleKey(t *testing.T) {
	web, dir := spaceWeb(t)
	game := filepath.Join(dir, "Game [0100000000010000][v0].nsp")
	offered := func() bool {
		for _, candidate := range web.compressCandidates() {
			if candidate.Path == game {
				return true
			}
		}
		return false
	}
	if !offered() {
		t.Fatal("the game is offered to compress")
	}
	info, _ := os.Stat(game)
	web.titleKeys().set(game, titleKeyCheck{Size: info.Size(), ModTime: info.ModTime().UnixNano(), Missing: "0100000000010000000000000000000a"})
	web.invalidateDerived()
	if offered() {
		t.Fatal("without its title key it is not offered")
	}
	found := false
	for _, issue := range web.getIssues() {
		if issue.File == game && issueKind(issue.Reason) == ISSUE_NO_KEY {
			found = true
		}
	}
	if !found {
		t.Fatal("Issues tells it")
	}
	if got := translateIssue("es", "no title key (rights ID 0100000000010000000000000000000a): the file has no ticket and title.keys does not list it"); got == "" || got[:3] != "sin" {
		t.Fatalf("translated: %q", got)
	}

	// the file replaced by another copy: looked at again, not taken as without its key
	os.WriteFile(game, make([]byte, 301), 0o644)
	web.invalidateDerived()
	if len(web.missingTitleKeys()) != 0 || !offered() {
		t.Fatal("a file that changed is looked at again")
	}
}

// The files of the library are looked at only once the scan running ends: the library it shows
// meanwhile is the part found so far, and the files not found yet would be forgotten.
func TestTitleKeysWaitForTheScan(t *testing.T) {
	web, _ := compressWeb(t)
	gone := filepath.Join(t.TempDir(), "Not scanned yet [0100000000020000][v0].nsp")
	web.titleKeys().set(gone, titleKeyCheck{Size: 1, Missing: "0100000000020000000000000000000a"})
	web.state.startSync()
	web.checkTitleKeys()
	web.state.endSync()
	if _, ok := web.titleKeys().files[gone]; !ok {
		t.Fatal("kept while a scan runs")
	}
	web.checkTitleKeys()
	if _, ok := web.titleKeys().files[gone]; ok {
		t.Fatal("a file gone is forgotten once the library is whole")
	}
}
