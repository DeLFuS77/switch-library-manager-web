package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func TestIgnoreAFileTypeFromTheIssues(t *testing.T) {
	web := newTestWeb(t)
	web.embedFS = os.DirFS("..")
	// ignoring a type scans the library again
	manager, err := db.NewLocalSwitchDBManager(web.dataFolder)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	web.localDbManager = manager
	web.HandleIssues()
	issues := []Issue{{File: "/roms/a/1.sfx", Reason: "file type is not supported"}, {File: "/roms/a/2.SFX", Reason: "file type is not supported"},
		{File: "/roms/b.cue", Reason: "file type is not supported"}, {File: "/roms/c.nsp", Reason: "base file is missing"}}
	types := unsupportedTypes(issues, 5)
	if len(types) != 2 || types[0].Name != ".sfx" || types[0].Count != 2 {
		t.Fatalf("the types of the files that are not games, most files first: %+v", types)
	}

	original := settings.ReadSettings(web.dataFolder).IgnoreFileTypes
	defer settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.IgnoreFileTypes = original })
	for _, bad := range []string{".nsp", "XCI", "", "a/b", "..", "a b"} {
		if response := postForm(web, "/issues/ignore-type", url.Values{"type": {bad}}); response.Code != http.StatusBadRequest {
			t.Errorf("%q must be refused: %d", bad, response.Code)
		}
	}
	if response := postForm(web, "/issues/ignore-type", url.Values{"type": {"SFX"}}); response.Code != http.StatusAccepted {
		t.Fatalf("ignore: %d", response.Code)
	}
	postForm(web, "/issues/ignore-type", url.Values{"type": {".sfx"}})
	// the scan that follows ends before the test does
	for deadline := 0; web.state.IsSynchronizing() && deadline < 200; deadline++ {
		waitShort()
	}
	count := 0
	for _, known := range settings.ReadSettings(web.dataFolder).IgnoreFileTypes {
		if known == "sfx" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("a type is ignored once: %v", settings.ReadSettings(web.dataFolder).IgnoreFileTypes)
	}
}

// The issues are summed up by kind, the most urgent first, and the list can show one kind.
func TestIssueKinds(t *testing.T) {
	cases := map[string]string{
		"damaged file: NCA does not match":                                    ISSUE_DAMAGED,
		"failed to read NSP: prod.keys has no key_area_key_application_12...": ISSUE_KEYS,
		"base file is missing":                                                ISSUE_ORPHAN,
		"duplicate base file (x.nsp)":                                         ISSUE_DUPLICATE,
		"old update file, newer update exist locally (x.nsp)":                 ISSUE_OLD,
		"identified by file name only":                                        ISSUE_FILENAME,
		"file type is not supported":                                          ISSUE_UNSUPPORTED,
		"failed to read NSP [reason: unexpected EOF]":                         ISSUE_TRUNCATED,
		"unable to determine title-Id / version - Invalid NSP file, reason - [failed to read PFS0 header: read 0 of 12 bytes: EOF]": ISSUE_TRUNCATED,
		"unable to determine title-Id / version - failed to read NCA header EOF":                                                    ISSUE_TRUNCATED,
		"unable to determine title-Id / version - invalid NCA magic":                                                                ISSUE_UNREADABLE,
	}
	for reason, want := range cases {
		if got := issueKind(reason); got != want {
			t.Errorf("%q: %s, want %s", reason, got, want)
		}
	}
	// the files cut short can be deleted, not the others that could not be read; both say why
	if !deletableIssueKinds[ISSUE_TRUNCATED] || deletableIssueKinds[ISSUE_UNREADABLE] {
		t.Fatal("only the files cut short are deleted among the unread ones")
	}
	cause := func(reason string) string { return issueCause(issueKind(reason), reason, "en") }
	if cause("unable to determine title-Id / version - failed to read NCA header EOF") == "" ||
		cause("unable to determine title-Id / version - invalid NCA magic") == "" ||
		cause("unable to determine title-Id / version - something else") != "" {
		t.Fatal("the cause in plain words, only when it is known")
	}
	// a file that cannot be opened is no file cut short, whatever its name: it was offered to be
	// deleted with the files cut short for the "EOF" in GAMEOFLIFE.nsp
	unopened := Issue{Reason: `failed to read NSP [reason: Invalid NSP file, reason - [open C:\roms\GAMEOFLIFE.nsp: Access is denied.]]`}
	if kind := unopened.kind(); kind != ISSUE_UNREADABLE {
		t.Fatalf("a file that cannot be opened: %s", kind)
	}
	// the scan tells a file cut short by the error of its reader, whatever the text says
	if kind := (Issue{Reason: "failed to read NSP [reason: a header ends early]", CutShort: true}).kind(); kind != ISSUE_TRUNCATED {
		t.Fatalf("cut short by the scan: %s", kind)
	}
	// a file still being written is cut short too: it is not deleted until it rests
	dir := t.TempDir()
	for _, name := range []string{"old.nsp", "new.nsp"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "old.nsp"), past, past); err != nil {
		t.Fatal(err)
	}
	files := []db.ExtendedFileInfo{{FileName: "old.nsp", BaseFolder: dir}, {FileName: "new.nsp", BaseFolder: dir}}
	if settled := settledFiles(files, 10*time.Minute); len(settled) != 1 || settled[0].FileName != "old.nsp" {
		t.Fatalf("only the file at rest: %+v", settled)
	}
	web := newTestWeb(t)
	summaries := web.issueSummaries([]Issue{{File: "a.txt", Reason: "file type is not supported"}, {File: "b.nsp", Reason: "damaged file: x"}, {File: "c.nsp", Reason: "damaged file: y"}}, "en")
	if len(summaries) != 2 || summaries[0].Kind != ISSUE_DAMAGED || summaries[0].Count != 2 || summaries[1].Kind != ISSUE_UNSUPPORTED {
		t.Fatalf("the summary: %+v", summaries)
	}
}

// A duplicate in a file that also holds content the library keeps (a file with several
// titles) says so in Issues, as in Space; the other duplicates do not.
func TestIssuesTellAFileHoldsKeptContent(t *testing.T) {
	web := newTestWeb(t)
	dir := t.TempDir()
	pack := db.ExtendedFileInfo{FileName: "Pack [0100000000010800][v65536].xcz", BaseFolder: dir}
	copied := db.ExtendedFileInfo{FileName: "Other [0100000000020000][v0] (copy).nsp", BaseFolder: dir}
	localDB := &db.LocalSwitchFilesDB{
		TitlesMap: map[string]*db.SwitchGameFiles{
			"0100000000010": {BaseExist: true, File: db.SwitchFileInfo{ExtendedInfo: db.ExtendedFileInfo{FileName: "Game.nsz", BaseFolder: dir}},
				LatestUpdate: 65536, Updates: map[int]db.SwitchFileInfo{65536: {ExtendedInfo: pack}}, Dlc: map[string]db.SwitchFileInfo{}},
		},
		Skipped: map[db.ExtendedFileInfo]db.SkippedFile{
			pack:   {ReasonCode: db.REASON_DUPLICATE, ReasonText: "duplicate base file (" + filepath.Join(dir, "Game.nsz") + ")"},
			copied: {ReasonCode: db.REASON_DUPLICATE, ReasonText: "duplicate base file (" + filepath.Join(dir, "Other.nsp") + ")"},
		},
	}
	web.state.set(&db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}, localDB)
	kept := map[string]bool{}
	for _, issue := range web.buildIssues() {
		kept[filepath.Base(issue.File)] = issue.Kept
	}
	if len(kept) != 2 || !kept[pack.FileName] || kept[copied.FileName] {
		t.Fatalf("only the file that holds the latest update is kept: %v", kept)
	}
}
