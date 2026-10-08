package web

import (
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

func TestTitleVersions(t *testing.T) {
	released := map[int]string{65536: "2024-01-10", 131072: "2024-03-02", 196608: "2024-06-20"}
	local := &db.SwitchGameFiles{BaseExist: true, LatestUpdate: 131072, Updates: map[int]db.SwitchFileInfo{131072: {}}}
	versions := titleVersions("Star Game", released, local)
	if len(versions) != 3 || versions[0].Version != 196608 || versions[2].Number != 1 {
		t.Fatalf("the newest first: %+v", versions)
	}
	if versions[0].Owned || !versions[1].Owned || !versions[1].Installed || versions[2].Owned {
		t.Fatalf("owned and installed: %+v", versions)
	}
	if versions[0].Date.Year() != 2024 || !strings.HasPrefix(versions[0].NotesUrl, "https://duckduckgo.com/?q=Star+Game") || !strings.Contains(versions[0].NotesUrl, "June+2024") {
		t.Fatalf("date and notes: %+v", versions[0])
	}
	// a game that is not in the library
	if versions := titleVersions("Star Game", released, nil); versions[0].Owned || versions[0].Installed {
		t.Fatal("nothing is owned")
	}
}
