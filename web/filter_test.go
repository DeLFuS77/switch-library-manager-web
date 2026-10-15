package web

import "testing"

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
