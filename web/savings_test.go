package web

import (
	"path/filepath"
	"testing"
)

func TestContentKind(t *testing.T) {
	cases := map[string]string{
		"Game [0100D00000150000][v0].nsp":     "Base",
		"Game [0100D00000150800][v65536].nsp": "Update",
		"Game [0100D00000151001][v0].nsp":     "DLC",
		"Game without an id.nsp":              "",
	}
	for name, want := range cases {
		if got := contentKind(name); got != want {
			t.Errorf("contentKind(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestRatioEstimate(t *testing.T) {
	store := &ratioStore{path: filepath.Join(t.TempDir(), COMPRESSION_RATIOS_FILENAME), kinds: map[string]ratioTotal{}}
	if _, ok := store.estimate("Base", "a.xci"); ok {
		t.Fatal("an estimate with no file compressed")
	}
	// fewer files of the format than needed: the kind alone gives the estimate
	store.add("Base", "a.nsp", 100, 80)
	store.add("Base", "b.nsp", 100, 80)
	store.add("Base", "c.xci", 100, 50)
	if percent, ok := store.estimate("Base", "d.xci"); !ok || percent != 30 {
		t.Fatalf("estimate = %d %v, want 30 from the kind", percent, ok)
	}
	store.add("Base", "e.nsp", 100, 80)
	if percent, ok := store.estimate("Base", "f.nsp"); !ok || percent != 20 {
		t.Fatalf("estimate = %d %v, want 20 from the format", percent, ok)
	}
	// a file that grew or has no size says nothing
	store.add("Base", "g.nsp", 100, 120)
	store.add("", "h.nsp", 100, 10)
	if total := store.kinds["Base"]; total.Files != 4 {
		t.Fatalf("files = %d, want 4", total.Files)
	}
	if summary := store.summary(); len(summary) != 1 || summary[0].Percent != 27 {
		t.Fatalf("summary = %+v", summary)
	}
}
