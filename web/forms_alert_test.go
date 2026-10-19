package web

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// A form of the preferences keeps no element with the class alert: saving removes the
// message of the last save by that class, and a note of a field with it once stopped every
// save of Settings and Organize.
func TestFormsHaveNoAlertInside(t *testing.T) {
	alertClass := regexp.MustCompile(`class="[^"]*\balert\b`)
	for _, page := range []string{"settings.html", "organize.html", "account.html"} {
		data, err := os.ReadFile("../resources/pages/" + page)
		if err != nil {
			t.Fatal(err)
		}
		html := string(data)
		for start := strings.Index(html, "<form"); start >= 0; {
			end := strings.Index(html[start:], "</form>")
			if end < 0 {
				break
			}
			if match := alertClass.FindString(html[start : start+end]); match != "" {
				t.Errorf("%s: a form holds %s", page, match)
			}
			next := strings.Index(html[start+end:], "<form")
			if next < 0 {
				break
			}
			start += end + next
		}
	}
}
