package web

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Pages are also opened from /title/...: a relative link there would point inside that folder
// (issue #3), so every link of the templates starts at the root of the site.
func TestLinksWorkFromEveryFolder(t *testing.T) {
	relative := regexp.MustCompile(`\s(href|src|action)="([a-zA-Z][^":]*)"|"Href" "([a-zA-Z][^":]*)"`)
	files, _ := filepath.Glob("../resources/*.html")
	more, _ := filepath.Glob("../resources/*/*.html")
	for _, file := range append(files, more...) {
		// the exported site is opened as files, its links are relative on purpose
		if filepath.Base(file) == "site.html" {
			continue
		}
		data, _ := os.ReadFile(file)
		for _, match := range relative.FindAllStringSubmatch(string(data), -1) {
			t.Errorf("%s: relative link %q", filepath.Base(file), match[0])
		}
	}
}
