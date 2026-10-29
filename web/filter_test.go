package web

import (
	"os"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/pagination"
)

// The cards per page are rounded up to whole rows of the columns shown; the choice of the
// user is kept for the links and the menu.
func TestFitColumns(t *testing.T) {
	for _, c := range []struct{ per, columns, want int }{{24, 5, 25}, {24, 6, 24}, {24, 7, 28}, {12, 5, 15}, {24, 1, 24}, {24, 40, 24}} {
		f := &TitleItemFilter{PerPage: c.per}
		f.FitColumns(c.columns)
		if f.PerPage != c.want || f.SelectedPerPage() != c.per {
			t.Errorf("%d per page on %d columns: %d, chosen %d", c.per, c.columns, f.PerPage, f.SelectedPerPage())
		}
	}
}

// The search, the order and the per page keep every other filter of the list (My games, a
// collection, a genre, a kind of issue...), and start again on the first page.
func TestFilterFormsKeepEveryFilter(t *testing.T) {
	set, err := parseTemplates(os.DirFS(".."), "resources/partials/filter.html", "resources/partials/pagination.html")
	if err != nil {
		t.Fatal(err)
	}
	f := &TitleItemFilter{Keyword: `zelda "&"`, PerPage: 48, SortBy: "added", SortOrder: "desc", Page: 3, Status: STATUS_UPDATE, Format: "nsz",
		Kind: KIND_GAME, Region: "eur", Extra: EXTRA_FAVORITES, Collection: "Couch", IssueKind: "damaged", WithMissing: "0",
		Genre: db.Genres[0], Players: "2", GameLanguage: "ja", Play: PLAY_PLAYING}
	f.Normalize()
	f.FitColumns(5)
	hidden := func(name, value string) string {
		return `<input type="hidden" name="` + name + `" value="` + value + `">`
	}
	kept := []string{hidden("missing", "0"), hidden("play", PLAY_PLAYING), hidden("collection", "Couch"), hidden("kind_of_issue", "damaged"),
		hidden("genre", db.Genres[0]), hidden("players", "2"), hidden("languages", "ja"), hidden("kind", KIND_GAME), hidden("region", "eur"),
		hidden("extra", EXTRA_FAVORITES), hidden("status", STATUS_UPDATE), hidden("format", "nsz")}
	render := func(name string) string {
		var out strings.Builder
		data := map[string]any{"Filter": f, "Page": "index", "Pagination": pagination.Calculate(f.Page, f.PerPage, 500)}
		if err := set[DEFAULT_LANGUAGE].ExecuteTemplate(&out, name, data); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	search := render("filter")
	for _, want := range append(kept, hidden("per_page", "48")) {
		if !strings.Contains(search, want) {
			t.Errorf("the search and the order lose %s", want)
		}
	}
	for _, own := range []string{"q", "sort_by", "sort_order", "page"} {
		if strings.Contains(search, `type="hidden" name="`+own+`"`) {
			t.Errorf("the search and the order repeat %s", own)
		}
	}

	perPage := render("pagination")
	for _, want := range append(kept, hidden("q", "zelda &#34;&amp;&#34;"), hidden("sort_by", "added"), hidden("sort_order", "desc")) {
		if !strings.Contains(perPage, want) {
			t.Errorf("the per page loses %s", want)
		}
	}
	for _, own := range []string{"per_page", "page"} {
		if strings.Contains(perPage, `type="hidden" name="`+own+`"`) {
			t.Errorf("the per page repeats %s", own)
		}
	}
}
