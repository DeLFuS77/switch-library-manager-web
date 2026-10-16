package web

import (
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// A game the titles database knows from the Korean store only shows the name it gives
// itself in English, except in Japanese, Korean and Chinese.
func TestNameInTheScriptOfTheInterface(t *testing.T) {
	nacp := &switchfs.Nacp{TitleName: map[string]switchfs.NacpTitle{
		"AmericanEnglish": {Title: "Shin chan: Shiro and the Coal Town"},
		"Spanish":         {Title: ""},
		"Korean":          {Title: "짱구는 못말려"},
	}}
	lookup := func(id string) *switchfs.Nacp {
		if id == "010049901D704000" {
			return nacp
		}
		return nil
	}
	previous := fileNacp.Load()
	fileNacp.Store(&lookup)
	t.Cleanup(func() { fileNacp.Store(previous) })

	switchDB := &db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}}
	korean := "짱구는 못 말려『탄광마을의 흰둥이』"
	if got := titleName(switchDB, "es", "010049901D704000", korean); got != "Shin chan: Shiro and the Coal Town" {
		t.Errorf("es: %q", got)
	}
	if got := titleName(switchDB, "ko", "010049901D704000", korean); got != korean {
		t.Errorf("ko keeps the name of the database: %q", got)
	}
	nacp.TitleName["Spanish"] = switchfs.NacpTitle{Title: "Shin chan: Shiro y el pueblo del carbón"}
	if got := titleName(switchDB, "es", "010049901D704000", korean); got != "Shin chan: Shiro y el pueblo del carbón" {
		t.Errorf("es prefers the Spanish name: %q", got)
	}
	// a name in the script of the interface stays, and a game without other names keeps its own
	if got := titleName(switchDB, "es", "010049901D704000", "Crayon Shin-chan"); got != "Crayon Shin-chan" {
		t.Errorf("latin name: %q", got)
	}
	if got := titleName(switchDB, "es", "010049901D704000", "-"); got != "Shin chan: Shiro y el pueblo del carbón" {
		t.Errorf("a placeholder gives way: %q", got)
	}
	if got := titleName(switchDB, "es", "0100000000010000", korean); got != korean {
		t.Errorf("no other name: %q", got)
	}
}
