package web

import (
	"path/filepath"
	"strings"
	"testing"
)

// Settings refuse a library folder listed twice or inside another one: its files would be read
// twice and count as duplicates of themselves.
func TestRepeatedFolders(t *testing.T) {
	roms := filepath.Join("lib", "roms")
	inside := filepath.Join(roms, "updates")
	other := filepath.Join("lib", "romsx")
	if message := repeatedFolders([]string{roms, other}, "en"); message != "" {
		t.Fatalf("two different folders: %q", message)
	}
	if message := repeatedFolders([]string{roms, other, roms + string(filepath.Separator)}, "en"); !strings.Contains(message, "listed twice") {
		t.Fatalf("a folder twice: %q", message)
	}
	if message := repeatedFolders([]string{inside, roms}, "en"); !strings.Contains(message, "is inside") || !strings.HasPrefix(message, inside) {
		t.Fatalf("a folder inside another: %q", message)
	}
	if message := repeatedFolders([]string{roms, roms}, "es"); !strings.Contains(message, "dos veces") {
		t.Fatalf("in the language of the page: %q", message)
	}
}
