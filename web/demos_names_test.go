package web

import (
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// The demos of the Chinese and Korean stores are demos too, also when only the game names
// itself so.
func TestDemosOfEveryStore(t *testing.T) {
	for _, name := range []string{"疯狂兔子：奇遇派对 试玩版", "舞力全開 試玩版", "某游戏 体验版", "某遊戲 體驗版", "게임 체험판", "ゲーム 体験版", "Game (Demo)"} {
		if !isDemo(nil, name) {
			t.Errorf("%q is a demo", name)
		}
	}
	if isDemo(nil, "Trial of Mana") || isDemo(nil, "疯狂兔子：奇遇派对") {
		t.Error("a game whose name only looks like a demo")
	}
	meta := &switchfs.ContentMetaAttributes{Ncap: &switchfs.Nacp{TitleName: map[string]switchfs.NacpTitle{
		"AmericanEnglish": {Title: "Rabbids Party"}, "SimplifiedChinese": {Title: "疯狂兔子 试玩版"}}}}
	if !localDemo(meta) || localDemo(nil) || localDemo(&switchfs.ContentMetaAttributes{}) {
		t.Fatal("the names the game gives itself mark the demo")
	}
}

// A placeholder name of the titles database gives way to the name of the game itself.
func TestPlaceholderNamesAreNotShown(t *testing.T) {
	for name, usable := range map[string]bool{"-": false, " - ": false, "?": false, "": false, "Game": true, "舞力全开": true} {
		if usableName(name) != usable {
			t.Errorf("%q usable: %v", name, !usable)
		}
	}
	title := &db.SwitchTitle{Attributes: db.TitleAttributes{Name: "-"}}
	files := &db.SwitchGameFiles{File: db.SwitchFileInfo{Metadata: &switchfs.ContentMetaAttributes{Ncap: &switchfs.Nacp{TitleName: map[string]switchfs.NacpTitle{
		"SimplifiedChinese": {Title: "疯狂兔子：奇遇派对"}}}}}}
	if name := getLocalTitleName(title, files); name != "疯狂兔子：奇遇派对" {
		t.Fatalf("the name of the game itself: %q", name)
	}
}
