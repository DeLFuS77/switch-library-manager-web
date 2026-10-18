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
	if issueCause("unable to determine title-Id / version - failed to read NCA header EOF", "en") == "" ||
		issueCause("unable to determine title-Id / version - invalid NCA magic", "en") == "" ||
		issueCause("unable to determine title-Id / version - something else", "en") != "" {
		t.Fatal("the cause in plain words, only when it is known")
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
