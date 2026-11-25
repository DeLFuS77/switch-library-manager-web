package web

import "testing"

// A DLC the page of the game does not count as missing is not "missing" in the inventory either (#200).
func TestExportDlcStatus(t *testing.T) {
	cases := []struct {
		dlc  TitleDlc
		want string
	}{
		{TitleDlc{}, "missing"},
		{TitleDlc{Owned: true}, "owned"},
		{TitleDlc{Owned: true, LanguageHidden: true}, "owned"},
		{TitleDlc{Ignored: true}, "ignored"},
		{TitleDlc{Ignored: true, LanguageHidden: true}, "ignored"},
		{TitleDlc{LanguageHidden: true}, "hidden"},
		{TitleDlc{LanguageHidden: true, ComesOut: 20261203}, "hidden"},
		{TitleDlc{ComesOut: 20261203}, "upcoming"},
	}
	for _, c := range cases {
		if got := exportDlcStatus(c.dlc); got != c.want {
			t.Errorf("exportDlcStatus(%+v) = %q, want %q", c.dlc, got, c.want)
		}
	}
}
