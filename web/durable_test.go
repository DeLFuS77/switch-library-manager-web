package web

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

func noSteps(int64) func(int64, int64) {
	return func(int64, int64) {}
}

func TestCommitFileKeepsTheBytes(t *testing.T) {
	folder := t.TempDir()
	temporary := filepath.Join(folder, ".game.nsz.tmp")
	target := filepath.Join(folder, "game.nsz")
	data := bytes.Repeat([]byte("switch"), 1000)
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := commitFile(temporary, target); err != nil {
		t.Fatal(err)
	}
	if read, err := os.ReadFile(target); err != nil || !bytes.Equal(read, data) {
		t.Fatalf("the file must keep its bytes: %v", err)
	}
	if _, err := os.Stat(temporary); !os.IsNotExist(err) {
		t.Fatal("the temporary name must be gone")
	}
}

func TestCommitFileOfAMissingFileFails(t *testing.T) {
	folder := t.TempDir()
	target := filepath.Join(folder, "game.nsz")
	if err := commitFile(filepath.Join(folder, ".gone.tmp"), target); err == nil {
		t.Fatal("a temporary file that is not there must fail")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("nothing must take the name")
	}
}

// with no room for the compressed file, nothing is written and the original stays
func TestCompressionWithoutSpaceKeepsTheOriginal(t *testing.T) {
	web, path := compressWeb(t)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	web.compressor.mutex.Lock()
	web.compressor.reserved = math.MaxInt64 / 2
	web.compressor.mutex.Unlock()
	if _, err := web.compressFile(context.Background(), path, switchfs.LevelFast, true, noSteps); !errors.Is(err, errCompressSpace) {
		t.Fatalf("the compression must stop for the space: %v", err)
	}
	if read, err := os.ReadFile(path); err != nil || !bytes.Equal(read, original) {
		t.Fatal("the original must be kept whole")
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*"))
	if len(left) != 1 {
		t.Fatalf("only the original must be in its folder: %v", left)
	}
}

// the power goes after the compressed file took its name and before the original was
// deleted: the task goes on after the restart, finds the compressed file and keeps both
func TestCompressionCutBeforeTheDeleteKeepsTheOriginal(t *testing.T) {
	web, path := compressWeb(t)
	if _, err := web.compressFile(context.Background(), path, switchfs.LevelFast, false, noSteps); err != nil {
		t.Fatal(err)
	}
	if _, err := web.compressFile(context.Background(), path, switchfs.LevelFast, true, noSteps); !errors.Is(err, errCompressExists) {
		t.Fatalf("a compressed file already there must stop the task: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("the original must be kept when the task goes on again")
	}
	if _, err := os.Stat(switchfs.CompressedPath(path)); err != nil {
		t.Fatal("the compressed file must be kept")
	}
}

// the power goes while the compressed file is written: what was written is a hidden
// temporary file, never the compressed name, and the original is untouched
func TestCompressionCutWhileWritingLeavesNoCompressedFile(t *testing.T) {
	web, path := compressWeb(t)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := web.compressFile(ctx, path, switchfs.LevelFast, true, noSteps); err == nil {
		t.Fatal("a stopped compression must fail")
	}
	if _, err := os.Stat(switchfs.CompressedPath(path)); !os.IsNotExist(err) {
		t.Fatal("a compression cut short must not leave a compressed file")
	}
	if read, err := os.ReadFile(path); err != nil || !bytes.Equal(read, original) {
		t.Fatal("the original must be kept whole")
	}
}
