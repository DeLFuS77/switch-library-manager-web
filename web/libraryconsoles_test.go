package web

import (
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// The filter On the console takes the games installed on a console of the user, the ones not
// installed on it and the ones out of date, and counts them before it is chosen; it is in the
// address, and a console of another user takes none.
func TestLibraryFilterOnTheConsole(t *testing.T) {
	web, _, _, _, _, keyId := consolesWeb(t)
	switchDB, localDB := web.state.get()
	switchDB.TitlesMap["0100000000020"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000020000", Name: "Other Game"}}
	switchDB.TitlesMap["0100000000030"] = &db.SwitchTitle{Attributes: db.TitleAttributes{Id: "0100000000030000", Name: "Third Game"}}
	web.state.set(switchDB, localDB)

	library := func(user string, onConsole string) ([]string, LibraryFacets) {
		t.Helper()
		filter := &TitleItemFilter{PerPage: 96, SortBy: "name", SortOrder: "asc", Page: 1, OnConsole: onConsole, player: user}
		filter.Normalize()
		items, _, facets := web.getLibraryWithFacets(filter, "en")
		ids := []string{}
		for _, item := range items {
			ids = append(ids, item.Id)
		}
		return ids, facets
	}
	all, facets := library("bob", "")
	if len(all) != 3 || len(facets.Consoles) != 1 {
		t.Fatalf("the library: %v %+v", all, facets.Consoles)
	}
	if choice := facets.Consoles[0]; choice.Id != keyId || choice.Name != "Switch" || choice.Installed != 2 || choice.NotInstalled != 1 || choice.Outdated != 1 || !choice.Counted {
		t.Fatalf("the choices: %+v", choice)
	}
	for choice, want := range map[string]string{
		"installed." + keyId:         "0100000000010000,0100000000020000",
		"not." + keyId:               "0100000000030000",
		"outdated." + keyId:          "0100000000010000",
		"installed.0000000000000000": "",
	} {
		if got, _ := library("bob", choice); strings.Join(got, ",") != want {
			t.Errorf("%s: %v, want %s", choice, got, want)
		}
	}
	if _, facets := library("bob", "outdated."+keyId); facets.OnConsoleLabel != "Out of date on Switch" || facets.Consoles[0].Installed != 2 {
		t.Errorf("the chip and the numbers before the filter: %+v", facets)
	}
	// Alice has no console: no choice, and bob's takes nothing
	if got, facets := library("alice", "installed."+keyId); len(got) != 0 || len(facets.Consoles) != 0 {
		t.Errorf("another user: %v %+v", got, facets.Consoles)
	}
	if got, _ := library("alice", ""); len(got) != 3 {
		t.Errorf("without the filter: %v", got)
	}

	// in the address, cleaned
	filter := &TitleItemFilter{OnConsole: "installed." + keyId}
	if !strings.Contains(filter.query().Encode(), "on_console=installed."+keyId) || filter.SecondaryCount() != 1 {
		t.Errorf("in the address: %s", filter.query().Encode())
	}
	for _, wrong := range []string{"installed", "installed.XYZ", "gone." + keyId, "installed." + keyId + "0"} {
		filter := &TitleItemFilter{OnConsole: wrong}
		if filter.Normalize(); filter.OnConsole != "" {
			t.Errorf("%q kept", wrong)
		}
	}
}
