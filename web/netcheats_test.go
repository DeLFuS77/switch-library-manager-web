package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The console gets the cheats of a version, or of the build it reads from the game, with their
// names and their text as Atmosphère reads it; an update asks under the ID of its game.
func TestNetCheatsForTheConsole(t *testing.T) {
	cheatsServer(t)
	web, _, _, console, auth, _, _ := pushWeb(t)
	web.downloadCheats()
	get := func(path string) (int, netCheats, string) {
		got := console.do("GET", path, nil, "Authorization", auth)
		answer := netCheats{}
		json.Unmarshal(got.Body.Bytes(), &answer)
		return got.Code, answer, got.Body.String()
	}
	for _, path := range []string{"/install/cheats/0100000000010000?version=65536", "/install/cheats/0100000000010800?version=65536",
		"/install/cheats/0100000000010000?build=2222222222222222", "/install/cheats/0100000000010000?build=2222222222222222&version=0"} {
		code, answer, body := get(path)
		if code != http.StatusOK || answer.TitleId != "0100000000010000" || answer.BuildId != "2222222222222222" || answer.Version == nil ||
			*answer.Version != 65536 || answer.Text != testCheats || strings.Join(answer.Names, "|") != "Master Code|Infinite HP|Max Gold" {
			t.Errorf("%s: %d %s", path, code, body)
		}
	}
	// the library has 131072, which has none; nor has the first version, nor a build not known
	for _, path := range []string{"/install/cheats/0100000000010000", "/install/cheats/0100000000010000?version=0",
		"/install/cheats/0100000000010000?build=9999999999999999", "/install/cheats/0100000000090000?version=0"} {
		if code, _, body := get(path); code != http.StatusNotFound || !strings.Contains(body, `"error":"no cheats"`) {
			t.Errorf("%s: %d %s", path, code, body)
		}
	}
	// without cheats, the build ID still comes, for the check before an update
	if code, _, body := get("/install/cheats/0100000000010000?build=9999999999999999"); code != http.StatusNotFound ||
		!strings.Contains(body, `"build_id":"9999999999999999"`) {
		t.Errorf("the build ID without cheats: %d %s", code, body)
	}
	if code, _, body := get("/install/cheats/0100000000010000?version=65536&build="); code != http.StatusOK || !strings.Contains(body, "2222222222222222") {
		t.Errorf("a version with cheats: %d %s", code, body)
	}
	for _, path := range []string{"/install/cheats/0100000000010000?build=22", "/install/cheats/0100000000010000?version=new"} {
		if code, _, _ := get(path); code != http.StatusBadRequest {
			t.Errorf("%s: %d", path, code)
		}
	}
}
