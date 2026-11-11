package web

import (
	"net/http"
	"testing"
)

// The bytes of a base game for NXDock: only to a console with its key, only places that make
// sense, only base games of the library. (The reading itself, switchfs.OpenBaseRomfs, was checked
// against the reader of NXDock on real games: the same bytes, a sparse base in an NSZ among them.)
func TestNetBaseAsked(t *testing.T) {
	_, admin, key := netWeb(t)
	console := &client{t: t, handler: admin.handler}
	auth := basic("switch", key)
	stranger := &client{t: t, handler: admin.handler}
	if code := stranger.do("GET", "/install/base/0100000000010000/0/16", nil).Code; code != http.StatusUnauthorized && code != http.StatusForbidden {
		t.Fatalf("without a key: %d", code)
	}
	for path, want := range map[string]int{
		"/install/base/0100000000010000/0":           http.StatusNotFound,
		"/install/base/01000000000100/0/16":          http.StatusNotFound,
		"/install/base/0100000000010000/-1/16":       http.StatusBadRequest,
		"/install/base/0100000000010000/0/0":         http.StatusBadRequest,
		"/install/base/0100000000010000/0/999999999": http.StatusBadRequest,
		"/install/base/zz00000000010000/0/16":        http.StatusBadRequest,
		"/install/base/0100000000020000/0/16":        http.StatusNotFound,            // not in the library
		"/install/base/0100000000010800/0/16":        http.StatusNotFound,            // an update is not a base
		"/install/base/0100000000010000/0/16":        http.StatusInternalServerError, // the file of the test is no game
	} {
		if code := console.do("GET", path, nil, "Authorization", auth).Code; code != want {
			t.Errorf("%s: %d, want %d", path, code, want)
		}
	}
}
