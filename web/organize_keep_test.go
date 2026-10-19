package web

import (
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// Only the known choices of the copy of a duplicate to keep are saved.
func TestOrganizeFormKeepDuplicate(t *testing.T) {
	for value, want := range map[string]string{"nsp": settings.KEEP_NSP, "xci": settings.KEEP_XCI, "auto": settings.KEEP_AUTO, "": settings.KEEP_FIRST_FOUND, "zip": settings.KEEP_FIRST_FOUND} {
		form := OrganizeForm{KeepDuplicate: value}
		if got := form.toOptions(settings.OrganizeOptions{KeepDuplicate: settings.KEEP_AUTO}).KeepDuplicate; got != want {
			t.Errorf("%q: %q, want %q", value, got, want)
		}
	}
}
