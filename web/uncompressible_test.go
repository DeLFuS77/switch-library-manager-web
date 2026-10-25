package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// A file nothing could shrink is remembered by its size; replaced by another one, it is
// offered again.
func TestUncompressibleFilesAreRemembered(t *testing.T) {
	web := newTestWeb(t)
	store := web.uncompressible()
	store.add("/games/a.nsp", 100, UNCOMPRESSIBLE_SPARSE)
	if file, ok := store.get("/games/a.nsp", 100); !ok || file.reason() != UNCOMPRESSIBLE_SPARSE {
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

// A file nothing could shrink that Organize moves is remembered under its new path, and one it
// deletes is forgotten: it is not offered again because its folder changed.
func TestUncompressibleFilesFollowOrganize(t *testing.T) {
	web := newTestWeb(t)
	store := web.uncompressible()
	store.add("/games/a.nsp", 100, UNCOMPRESSIBLE_SPARSE)
	store.add("/games/b.nsp", 200, UNCOMPRESSIBLE_NO_DATA)
	store.add("/games/d.nsp", 300, UNCOMPRESSIBLE_NO_DATA)
	moved := "/games/A [0100000000010000]/A [0100000000010000][v0].nsp"
	store.follow([]process.Operation{
		{Kind: process.OP_MOVE, From: "/games/a.nsp", To: moved},
		{Kind: process.OP_DELETE, From: "/games/b.nsp"},
		{Kind: process.OP_MOVE, From: "/games/c.nsp", To: "/games/C/c.nsp"},
		{Kind: process.OP_MOVE, From: "/games/d.nsp", To: "/games/D/d.nsp", Error: "access denied"},
	})
	if file, ok := store.get(moved, 100); !ok || !file.Sparse || file.Reason != UNCOMPRESSIBLE_SPARSE {
		t.Fatalf("moved: %+v %v", file, ok)
	}
	if _, ok := store.get("/games/a.nsp", 100); ok {
		t.Fatal("the old path is forgotten")
	}
	if _, ok := store.get("/games/b.nsp", 200); ok {
		t.Fatal("a deleted file is forgotten")
	}
	if _, ok := store.get("/games/d.nsp", 300); !ok {
		t.Fatal("a move that failed leaves it where it was")
	}
	other := &Web{dataFolder: web.dataFolder}
	if _, ok := other.uncompressible().get(moved, 100); !ok {
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
	store.add("/games/new.nsp", 100, "")
	if _, ok := store.get("/games/new.nsp", 50); ok {
		t.Fatal("a file noted with its own size matches another size")
	}
}

// The Compress page tells why each file cannot be compressed. For a file noted without its
// reason by an older version, the reason is found in the background, without writing anything,
// and kept.
func TestUncompressibleReasonsAreShown(t *testing.T) {
	web, path := compressWeb(t)
	info, _ := os.Stat(path)
	// the game becomes its own NSZ, under the name of the NSP: compressed already
	nsz := filepath.Join(t.TempDir(), "game.nsz")
	if _, err := switchfs.CompressGame(context.Background(), path, nsz, switchfs.CompressOptions{}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(nsz)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	// noted by an older version, with no reason; the library still knows the size it had
	store := web.uncompressible()
	store.files[path] = uncompressibleFile{Size: info.Size(), Exact: true}

	page := func() string {
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/compress.html", nil))
		return recorder.Body.String()
	}
	if strings.Contains(page(), "Compressed already") {
		t.Fatal("the reason is not known yet")
	}
	waitForBackgroundWork(web)
	if file, _ := store.get(path, info.Size()); file.reason() != UNCOMPRESSIBLE_ALREADY {
		t.Fatalf("the reason found: %+v", file)
	}
	if !strings.Contains(page(), `<span class="badge text-bg-secondary">Compressed already</span>`) {
		t.Fatal("the page tells the reason")
	}
	// saved: known after a restart
	if file, _ := (&Web{dataFolder: web.dataFolder}).uncompressible().get(path, info.Size()); file.reason() != UNCOMPRESSIBLE_ALREADY {
		t.Fatalf("not kept: %+v", file)
	}
}

func TestUncompressibleReason(t *testing.T) {
	for err, want := range map[error]string{
		fmt.Errorf("x: %w", switchfs.ErrSparseStorage):                                                             UNCOMPRESSIBLE_SPARSE,
		fmt.Errorf("x: %w", switchfs.ErrAlreadyCompressed):                                                         UNCOMPRESSIBLE_ALREADY,
		fmt.Errorf("%w: a.nca: it holds no game data (control)", switchfs.ErrNotCompressible):                      UNCOMPRESSIBLE_NO_DATA,
		fmt.Errorf("%w: a.nca: its sections use an encryption the app does not read", switchfs.ErrNotCompressible): UNCOMPRESSIBLE_ENCRYPTION,
		switchfs.ErrNotCompressible:                                                                                "",
		errors.New("prod.keys is needed"):                                                                          "",
	} {
		if got := uncompressibleReason(err); got != want {
			t.Errorf("%v: %q, want %q", err, got, want)
		}
	}
}
