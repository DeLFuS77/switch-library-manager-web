package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestSdPlanner(t *testing.T) {
	web := newTestWeb(t)
	switchDB, localDB := testDatabases(t)
	switchDB.TitlesMap["0100000000010"].Attributes.Genres = []string{"RPG"}
	web.state.set(switchDB, localDB)

	// both games fit: the RPG first when RPG is liked
	plan := web.sdPlan(readSdOptions(url.Values{"capacity": {"32"}, "genre": {"RPG"}}), "en")
	if len(plan.Selected) != 2 || plan.Selected[0].Id != "0100000000010000" || plan.Selected[0].Reasons[0].Key != "Genre" {
		t.Fatalf("the liked genre first: %+v", plan.Selected)
	}
	if plan.Usable <= 0 || plan.Total != plan.Selected[0].Size+plan.Selected[1].Size {
		t.Fatalf("totals: %+v", plan)
	}
	// the favorites give their genres when none is chosen
	web.favorites().set("0100000000010000", true)
	plan = web.sdPlan(readSdOptions(url.Values{}), "en")
	if !plan.Options.AutoGenres || plan.Options.Genres[0] != "RPG" || plan.Selected[0].Reasons[0].Key != "Favorite" {
		t.Fatalf("genres of the favorites: %+v", plan.Options)
	}
	// nothing fits on a card that is all reserve
	plan = web.sdPlan(readSdOptions(url.Values{"capacity": {"32"}, "reserve": {"200"}}), "en")
	if len(plan.Selected) != 0 || len(plan.Others) != 2 {
		t.Fatalf("no space left: %+v", plan)
	}
	if options := readSdOptions(url.Values{"capacity": {"3"}, "reserve": {"-5"}, "genre": {"Nope"}}); options.Capacity != 256 || options.ReserveGB != 10 || len(options.Genres) != 0 {
		t.Fatalf("invalid values are dropped: %+v", options)
	}
}

func TestSdCopy(t *testing.T) {
	web := newTestWeb(t)
	web.state.set(testDatabases(t))
	web.HandleSdCard()
	target := t.TempDir()

	if response := postForm(web, "/sd/copy", url.Values{"id": {"0100000000010000"}, "target": {"relative/path"}}); response.Code != http.StatusBadRequest {
		t.Fatalf("relative folders are refused: %d", response.Code)
	}
	if response := postForm(web, "/sd/copy", url.Values{"id": {"0100000000010000"}, "target": {target}, "dlc": {"1"}}); response.Code != http.StatusAccepted {
		t.Fatalf("copy: %d %s", response.Code, response.Body.String())
	}
	task := waitForTask(t, web, TASK_COPY)
	if task.Status != TASK_SUCCESS || task.Files != 3 {
		t.Fatalf("the game, its update and its DLC are copied: %+v", task)
	}
	if _, err := os.Stat(filepath.Join(target, "Known Game", "Known [0100000000010000][v0].nsp")); err != nil {
		t.Fatal(err)
	}
	// again: the files there are kept
	postForm(web, "/sd/copy", url.Values{"id": {"0100000000010000"}, "target": {target}})
	if task := waitForTask(t, web, TASK_COPY); task.Status != TASK_SUCCESS || task.Files != 2 {
		t.Fatalf("a second copy without DLC: %+v", task)
	}
	if folder := sdFolderName(`Game: "The <Best>" / 2`); folder != "Game The Best 2" {
		t.Fatalf("names safe for a card: %q", folder)
	}
}
