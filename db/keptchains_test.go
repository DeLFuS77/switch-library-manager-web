package db

import (
	"path/filepath"
	"testing"
)

// Every duplicate and older version names the file the library keeps, also when the one it
// was compared with turned out to be a duplicate or an older version too.
func TestKeptChainsNameTheFileThatStays(t *testing.T) {
	folder := filepath.Join(string(filepath.Separator), "games")
	file := func(name string) ExtendedFileInfo { return ExtendedFileInfo{FileName: name, BaseFolder: folder} }
	path := func(name string) string { return filepath.Join(folder, name) }
	skipped := map[ExtendedFileInfo]SkippedFile{
		// three copies of a DLC: the first two were each kept for a while
		file("DLC (1).nsp"): {ReasonCode: REASON_DUPLICATE, ReasonText: "duplicate DLC file (" + path("DLC (2).nsp") + ")"},
		file("DLC (2).nsp"): {ReasonCode: REASON_DUPLICATE, ReasonText: "duplicate DLC file (" + path("DLC.nsp") + ")"},
		// three updates, and a copy of the first one
		file("v1.nsp"):      {ReasonCode: REASON_OLD_UPDATE, ReasonText: "old update file, newer update exist locally (" + path("v2.nsp") + ")"},
		file("v2.nsp"):      {ReasonCode: REASON_OLD_UPDATE, ReasonText: "old update file, newer update exist locally (" + path("v3.nsp") + ")"},
		file("v1 copy.nsp"): {ReasonCode: REASON_DUPLICATE, ReasonText: "duplicate update file (" + path("v1.nsp") + ")"},
		// other reasons are left as they are
		file("broken.nsp"): {ReasonCode: REASON_MALFORMED_FILE, ReasonText: "failed to read (" + path("v1.nsp") + ")"},
	}
	resolveKeptChains(skipped)

	want := map[string]SkippedFile{
		"DLC (1).nsp": {ReasonCode: REASON_DUPLICATE, ReasonText: "duplicate DLC file (" + path("DLC.nsp") + ")"},
		"DLC (2).nsp": {ReasonCode: REASON_DUPLICATE, ReasonText: "duplicate DLC file (" + path("DLC.nsp") + ")"},
		"v1.nsp":      {ReasonCode: REASON_OLD_UPDATE, ReasonText: "old update file, newer update exist locally (" + path("v3.nsp") + ")"},
		"v2.nsp":      {ReasonCode: REASON_OLD_UPDATE, ReasonText: "old update file, newer update exist locally (" + path("v3.nsp") + ")"},
		// a copy of an older version is an older version too
		"v1 copy.nsp": {ReasonCode: REASON_OLD_UPDATE, ReasonText: "old update file, newer update exist locally (" + path("v3.nsp") + ")"},
		"broken.nsp":  {ReasonCode: REASON_MALFORMED_FILE, ReasonText: "failed to read (" + path("v1.nsp") + ")"},
	}
	for name, entry := range want {
		if got := skipped[file(name)]; got != entry {
			t.Errorf("%s: %+v, want %+v", name, got, entry)
		}
	}
}
