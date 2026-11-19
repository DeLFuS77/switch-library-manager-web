package switchfs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDescribeFile(t *testing.T) {
	initNszKeys(t, "")
	folder := t.TempDir()
	source, original := writeTestNsp(t, folder)
	facts, err := DescribeFile(source)
	if err != nil || facts.Kind != "NSP" || facts.Size != int64(len(original)) || facts.DecompressedSize != facts.Size || facts.Solid+facts.Blocks != 0 {
		t.Fatalf("NSP: %+v %v", facts, err)
	}
	solid := filepath.Join(folder, "solid.nsz")
	CompressGame(context.Background(), source, solid, CompressOptions{})
	if facts, err := DescribeFile(solid); err != nil || facts.Kind != "NSZ" || facts.Solid != 2 || facts.Blocks != 0 || facts.DecompressedSize != int64(len(original)) {
		t.Fatalf("solid NSZ: %+v %v", facts, err)
	}
	blocks := filepath.Join(folder, "blocks.nsz")
	CompressGame(context.Background(), source, blocks, CompressOptions{Blocks: true})
	if facts, err := DescribeFile(blocks); err != nil || facts.Kind != "NSZ" || facts.Blocks != 2 || facts.Solid != 0 {
		t.Fatalf("NSZ in blocks: %+v %v", facts, err)
	}

	xci := writeTestXci(t, folder)
	data, _ := os.ReadFile(xci)
	input, _ := os.Open(xci)
	end, _ := XciDataEnd(input)
	input.Close()
	padded := append(append(data, make([]byte, int(end)-len(data))...), bytes.Repeat([]byte{0xFF}, 0x1000)...)
	os.WriteFile(xci, padded, 0o644)
	if facts, err := DescribeFile(xci); err != nil || facts.Kind != "XCI" || facts.Padding != 0x1000 {
		t.Fatalf("XCI: %+v %v", facts, err)
	}
	xcz := filepath.Join(folder, "game.xcz")
	CompressGame(context.Background(), xci, xcz, CompressOptions{})
	if facts, err := DescribeFile(xcz); err != nil || facts.Kind != "XCZ" || facts.Blocks != 1 {
		t.Fatalf("XCZ: %+v %v", facts, err)
	}
}

// How a file keeps its NCA files is read from the headers of its NCZ alone: an NSP or an XCI is
// plain, an NSZ solid or in blocks, an XCZ in blocks, and the extra compression of NXPress
// (NCZSECT2 and NCZSECT3) goes before the rest, as it limits the file most.
func TestReadStorage(t *testing.T) {
	initNszKeys(t, "")
	folder := t.TempDir()
	source, _ := writeTestNsp(t, folder)
	solid := filepath.Join(folder, "solid.nsz")
	blocks := filepath.Join(folder, "blocks.nsz")
	CompressGame(context.Background(), source, solid, CompressOptions{})
	CompressGame(context.Background(), source, blocks, CompressOptions{Blocks: true})
	xci := writeTestXci(t, folder)
	xcz := filepath.Join(folder, "game.xcz")
	CompressGame(context.Background(), xci, xcz, CompressOptions{})
	for path, want := range map[string]string{source: StoragePlain, solid: StorageSolid, blocks: StorageBlocks, xci: StoragePlain, xcz: StorageBlocks} {
		if storage, err := ReadStorage(path); err != nil || storage != want {
			t.Errorf("%s: %q %v, want %q", filepath.Base(path), storage, err, want)
		}
	}

	// the table of sections of the first NCZ in the experimental formats
	for magic, want := range map[string]string{nczSectionMagicV2: StorageSparse, nczSectionMagicV3: StorageArchive} {
		data, _ := os.ReadFile(solid)
		pkg, err := openGamePackage(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range pkg.files {
			if filepath.Ext(file.name) == ".ncz" {
				copy(data[file.offset+nczHeaderSize:], magic)
				break
			}
		}
		path := filepath.Join(folder, magic+".nsz")
		os.WriteFile(path, data, 0o644)
		if storage, err := ReadStorage(path); err != nil || storage != want {
			t.Errorf("%s: %q %v, want %q", magic, storage, err, want)
		}
	}

	// a file that is no package is not guessed
	junk := filepath.Join(folder, "junk.nsp")
	os.WriteFile(junk, []byte("data"), 0o644)
	if storage, err := ReadStorage(junk); err == nil || storage != "" {
		t.Fatalf("not a package: %q %v", storage, err)
	}
	if storage, err := ReadStorage(filepath.Join(folder, "missing.nsz")); err == nil || storage != "" {
		t.Fatalf("no file: %q %v", storage, err)
	}
}
