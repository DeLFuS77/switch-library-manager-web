package web

import (
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// Every page, the diagnostics too, is told whether the program is an official build: built with
// the tag "official", which only the workflows of this repository use (TRADEMARKS.md).
func TestPagesAreToldTheOfficialBuild(t *testing.T) {
	web := newTestWeb(t)
	for _, page := range []string{"index", "diagnostics"} {
		if got := web.globalPageData(page).OfficialBuild; got != settings.OFFICIAL_BUILD {
			t.Errorf("%s: OfficialBuild %v, the program %v", page, got, settings.OFFICIAL_BUILD)
		}
	}
}
