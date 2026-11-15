package switchfs

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// An NSP becomes a game card that holds its NCA files in its secure partition, unchanged, with a
// header that tells where everything is; back to an NSP it gives the same files. An NSZ becomes
// an XCZ with the same compressed files.
func TestConvertNspToXci(t *testing.T) {
	initNszKeys(t, "")
	folder := t.TempDir()
	nsp := writeTitleNsp(t, folder, 0x0100000000010000, 0, ContentMetaType_Application, 1)
	xci := CardPath(nsp)
	if filepath.Ext(xci) != ".xci" || CardPath("a.NSZ") != "a.xcz" || ConvertedPath("a.nsp") != "a.nsp" {
		t.Fatalf("names: %s", xci)
	}
	check, err := ConvertAndCheck(context.Background(), nsp, xci, nil)
	if err != nil || len(check.Modified) != 0 {
		t.Fatalf("convert: %+v %v", check, err)
	}
	expected, err := HashNcas(context.Background(), nsp, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := HashNcas(context.Background(), xci, nil)
	if err != nil || !reflect.DeepEqual(got, expected) {
		t.Fatalf("the card must hold the same NCA files: %v %v", got, err)
	}
	if titles, err := ReadXciMetadata(xci); err != nil || titles["0100000000010000"] == nil {
		t.Fatalf("the game in the card: %v %v", titles, err)
	}
	if _, err := VerifyFile(context.Background(), xci, nil); err != nil {
		t.Fatal(err)
	}

	// the header: the root partition, its hash, the secure area and the end of the data
	data, _ := os.ReadFile(xci)
	if string(data[0x100:0x104]) != "HEAD" || binary.LittleEndian.Uint64(data[0x130:]) != cardRootOffset {
		t.Fatal("the header of the card")
	}
	if end := (int64(binary.LittleEndian.Uint32(data[0x118:])) + 1) * cardPage; end != int64(len(data)) {
		t.Fatalf("the data ends at %d of %d", end, len(data))
	}
	if trimmed, err := XciDataEnd(mustOpen(t, xci)); err != nil || trimmed != int64(len(data)) {
		t.Fatalf("nothing to trim: %d %v", trimmed, err)
	}

	// and back: the NSP of the card holds the same files
	back := filepath.Join(folder, "back.nsp")
	if _, err := ConvertAndCheck(context.Background(), xci, back, nil); err != nil {
		t.Fatal(err)
	}
	if again, err := HashNcas(context.Background(), back, nil); err != nil || !reflect.DeepEqual(again, expected) {
		t.Fatalf("back to an NSP: %v %v", again, err)
	}

	// an NSZ becomes an XCZ with the same compressed files, which decompresses to them
	nsz := CompressedPath(nsp)
	if _, err := CompressGame(context.Background(), nsp, nsz, CompressOptions{}); err != nil {
		t.Fatal(err)
	}
	xcz := CardPath(nsz)
	if _, err := ConvertAndCheck(context.Background(), nsz, xcz, nil); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCompressed(context.Background(), xcz, expected, nil); err != nil {
		t.Fatalf("the XCZ must decompress to the same files: %v", err)
	}
}

func mustOpen(t *testing.T, path string) *os.File {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}
