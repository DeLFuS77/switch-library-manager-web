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
