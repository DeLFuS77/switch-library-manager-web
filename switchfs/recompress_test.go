package switchfs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nczModes tells, by name, whether each NCZ of a package is in blocks.
func nczModes(t *testing.T, path string) map[string]bool {
	t.Helper()
	input, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	pkg, err := openGamePackage(input)
	if err != nil {
		t.Fatal(err)
	}
	modes := map[string]bool{}
	for _, file := range pkg.files {
		if strings.HasSuffix(file.name, ".ncz") {
			data := make([]byte, min(file.size, nczHeaderSize+0x2000))
			input.ReadAt(data, file.offset)
			modes[file.name] = bytes.Contains(data[nczHeaderSize:], []byte(nczBlockMagic))
		}
	}
	return modes
}

func TestRecompressSolidAndBlocks(t *testing.T) {
	initNszKeys(t, "")
	folder := t.TempDir()
	source, original := writeTestNsp(t, folder)
	expected, _ := HashNcas(context.Background(), source, nil)
	solid := filepath.Join(folder, "solid.nsz")
	if _, err := CompressGame(context.Background(), source, solid, CompressOptions{}); err != nil {
		t.Fatal(err)
	}

	// solid to blocks, and back, without the NSP: each gives the original back
	blocks := filepath.Join(folder, "blocks.nsz")
	again := filepath.Join(folder, "again.nsz")
	var last, total int64
	result, err := RecompressGame(context.Background(), solid, blocks, CompressOptions{Blocks: true, Progress: func(done, all int64) { last, total = done, all }})
	if err != nil || result.Compressed != 2 || last != total || total == 0 {
		t.Fatalf("solid to blocks: %+v %v (%d of %d)", result, err, last, total)
	}
	if _, err := RecompressGame(context.Background(), blocks, again, CompressOptions{Level: "3"}); err != nil {
		t.Fatal(err)
	}
	for path, inBlocks := range map[string]bool{blocks: true, again: false} {
		for name, mode := range nczModes(t, path) {
			if mode != inBlocks {
				t.Fatalf("%s: %s in blocks %v, want %v", filepath.Base(path), name, mode, inBlocks)
			}
		}
		if err := VerifyCompressed(context.Background(), path, expected, nil); err != nil {
			t.Fatalf("%s: %v", filepath.Base(path), err)
		}
		restored := filepath.Join(folder, "back-"+filepath.Base(path)+".nsp")
		if err := DecompressNsz(context.Background(), path, restored, nil); err != nil {
			t.Fatal(err)
		}
		if data, _ := os.ReadFile(restored); !bytes.Equal(data, original) {
			t.Fatalf("%s does not decompress to the original", filepath.Base(path))
		}
	}

	// an NSP holds nothing to recompress
	if _, err := RecompressGame(context.Background(), source, filepath.Join(folder, "x.nsz"), CompressOptions{}); err == nil {
		t.Fatal("an NSP is compressed, not recompressed")
	}
}

func TestRecompressXczStaysInBlocks(t *testing.T) {
	initNszKeys(t, "")
	folder := t.TempDir()
	source := writeTestXci(t, folder)
	expected, _ := HashNcas(context.Background(), source, nil)
	xcz := filepath.Join(folder, "game.xcz")
	if _, err := CompressGame(context.Background(), source, xcz, CompressOptions{}); err != nil {
		t.Fatal(err)
	}
	again := filepath.Join(folder, "again.xcz")
	if _, err := RecompressGame(context.Background(), xcz, again, CompressOptions{Level: "19"}); err != nil {
		t.Fatal(err)
	}
	for name, inBlocks := range nczModes(t, again) {
		if !inBlocks {
			t.Fatalf("%s: an XCZ is always in blocks", name)
		}
	}
	if err := VerifyCompressed(context.Background(), again, expected, nil); err != nil {
		t.Fatal(err)
	}
}
