package web

import (
	"fmt"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// A file nothing could shrink is remembered by its size; replaced by another one, it is
// offered again.
func TestUncompressibleFilesAreRemembered(t *testing.T) {
	web := newTestWeb(t)
	store := web.uncompressible()
	store.add("/games/a.nsp", 100, true)
	if file, ok := store.get("/games/a.nsp", 100); !ok || !file.Sparse {
		t.Fatalf("not remembered: %+v %v", file, ok)
	}
	if _, ok := store.get("/games/a.nsp", 200); ok {
		t.Fatal("a file of another size is tried again")
	}
	// saved: read again by a new start of the app
	other := &Web{dataFolder: web.dataFolder}
	if _, ok := other.uncompressible().get("/games/a.nsp", 100); !ok {
		t.Fatal("not kept after a restart")
	}
}

func TestSparseStorageNote(t *testing.T) {
	if note := compressNote(TASK_COMPRESS, fmt.Errorf("x: %w", switchfs.ErrSparseStorage)); note != NOTE_COMPRESS_SPARSE {
		t.Fatalf("note: %q", note)
	}
	if note := compressNote(TASK_COMPRESS, switchfs.ErrNotCompressible); note != NOTE_COMPRESS_NOTHING {
		t.Fatalf("note: %q", note)
	}
}
