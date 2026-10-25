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
	if note := compressNote(TASK_COMPRESS, fmt.Errorf("game.nsp: x.nca: rights ID 01: %w", switchfs.ErrNoTitleKey)); note != NOTE_COMPRESS_NO_KEY {
		t.Fatalf("a game without its title key is told so: %q", note)
	}
	if note := compressNote(TASK_COMPRESS, fmt.Errorf("x: %w", switchfs.ErrSparseStorage)); note != NOTE_COMPRESS_SPARSE {
		t.Fatalf("note: %q", note)
	}
	if note := compressNote(TASK_COMPRESS, switchfs.ErrNotCompressible); note != NOTE_COMPRESS_NOTHING {
		t.Fatalf("note: %q", note)
	}
	// the reasons the library tells, each with its own note
	for err, want := range map[error]string{
		fmt.Errorf("x: %w", switchfs.ErrAlreadyCompressed):                                                         NOTE_COMPRESS_ALREADY,
		fmt.Errorf("%w: a.nca: it holds no game data (control)", switchfs.ErrNotCompressible):                      NOTE_COMPRESS_NO_DATA,
		fmt.Errorf("%w: it holds no NCA file but metadata", switchfs.ErrNotCompressible):                           NOTE_COMPRESS_NO_DATA,
		fmt.Errorf("%w: a.nca: its sections use an encryption the app does not read", switchfs.ErrNotCompressible): NOTE_COMPRESS_ENCRYPTION,
	} {
		if note := compressNote(TASK_COMPRESS, err); note != want {
			t.Errorf("%v: %q", err, note)
		}
	}
}

// A file noted by an older version, with its size times the passes of the task, is still
// known; a file noted now needs its own size.
func TestUncompressibleFromAnOlderVersion(t *testing.T) {
	web := newTestWeb(t)
	store := web.uncompressible()
	store.files["/games/old.nsp"] = uncompressibleFile{Size: 300, Sparse: true}
	if _, ok := store.get("/games/old.nsp", 100); !ok {
		t.Fatal("a file noted with the size of the progress is offered again")
	}
	if _, ok := store.get("/games/old.nsp", 70); ok {
		t.Fatal("another file of that name is taken for it")
	}
	store.add("/games/new.nsp", 100, false)
	if _, ok := store.get("/games/new.nsp", 50); ok {
		t.Fatal("a file noted with its own size matches another size")
	}
}
