package web

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// TestSagasOfTheRealTitles prints the biggest series found in a real titles database
// (SLM_REAL_TITLES is its path), to look at the groups by eye.
func TestSagasOfTheRealTitles(t *testing.T) {
	path := os.Getenv("SLM_REAL_TITLES")
	if path == "" {
		t.Skip("set SLM_REAL_TITLES")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	switchDB, err := db.CreateSwitchTitleDB(file, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	web := newTestWeb(t)
	web.state.set(switchDB, &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}})
	index := web.sagas()
	families := map[string]bool{}
	for _, family := range index.familyOfId {
		families[family] = true
	}
	type row struct {
		family string
		names  []string
	}
	rows := []row{}
	for family := range families {
		games := web.sagaMembers(family, "en")
		if len(games) < 2 {
			continue
		}
		names := []string{}
		for _, game := range games {
			names = append(names, game.OriginalName)
		}
		rows = append(rows, row{family, names})
	}
	sort.Slice(rows, func(i, j int) bool { return len(rows[i].names) > len(rows[j].names) })
	out := []string{fmt.Sprintf("%d series", len(rows))}
	for _, r := range rows {
		if len(r.names) > 12 {
			r.names = append(r.names[:12], "...")
		}
		key, publisher := splitSagaId(r.family)
		out = append(out, fmt.Sprintf("[%s / %s] %d: %s", key, publisher, len(r.names), strings.Join(r.names, " | ")))
	}
	os.WriteFile(os.Getenv("SLM_REAL_OUT"), []byte(strings.Join(out, "\n")), 0644)
}
