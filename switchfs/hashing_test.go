package switchfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The hashes a compression takes of the original while reading it are the ones a pass of
// their own gave: NSP, XCI, blocks, and an NCA rewritten by a tool.
func TestCompressionHashesTheOriginalWhileReadingIt(t *testing.T) {
	initNszKeys(t, "")
	section, tree := pfs0TreeSection()
	sources := map[string]string{
		"nsp":       func() string { path, _ := writeTestNsp(t, t.TempDir()); return path }(),
		"xci":       writeTestXci(t, t.TempDir()),
		"rewritten": writeRewrittenNsp(t, section, tree, false),
	}
	for name, source := range sources {
		for _, blocks := range []bool{false, true} {
			expected, check, err := HashAndCheck(context.Background(), source, nil)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "copy"+filepath.Ext(CompressedPath(source)))
			result, err := CompressGame(context.Background(), source, target, CompressOptions{Blocks: blocks})
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if !reflect.DeepEqual(result.Hashes, expected) {
				t.Fatalf("%s, blocks %v: the hashes differ from a pass of their own", name, blocks)
			}
			// the check from those hashes tells what the check of the file told
			fromHashes, err := CheckHashes(context.Background(), source, result.Hashes)
			if err != nil || !reflect.DeepEqual(fromHashes, check) {
				t.Fatalf("%s: %+v instead of %+v (%v)", name, fromHashes, check, err)
			}
			if err := VerifyCompressed(context.Background(), target, result.Hashes, nil); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
	}

	// a damaged NCA is still told apart from a rewritten one
	damaged := writeRewrittenNsp(t, section, tree, true)
	result, err := CompressGame(context.Background(), damaged, filepath.Join(t.TempDir(), "damaged.nsz"), CompressOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CheckHashes(context.Background(), damaged, result.Hashes); err == nil || !strings.Contains(err.Error(), "damaged or was modified") {
		t.Fatalf("a damaged NCA: %v", err)
	}
	// every file of the original must have been hashed
	delete(result.Hashes, func() string {
		for name := range result.Hashes {
			return name
		}
		return ""
	}())
	if _, err := CheckHashes(context.Background(), damaged, result.Hashes); err == nil {
		t.Fatal("a file without its hash")
	}
}

// The reader hashes a file read once in order; a read past what was hashed leaves it
// incomplete, a read of bytes already hashed does not.
func TestHashingReaderNeedsTheFileInOrder(t *testing.T) {
	data := bytes.Repeat([]byte("switch library "), 4096)
	file := bytes.NewReader(append(append([]byte("before"), data...), []byte("after")...))
	in := func(reads [][2]int64) *hashingReaderAt {
		reader := newHashingReaderAt(file, 6, int64(len(data)), sha256.New())
		for _, read := range reads {
			buffer := make([]byte, read[1])
			reader.ReadAt(buffer, read[0])
		}
		return reader
	}
	whole := in([][2]int64{{6, 1000}, {6, 500}, {1006, int64(len(data)) - 1000}})
	want := sha256.Sum256(data)
	if !whole.complete() || !bytes.Equal(whole.hash.Sum(nil), want[:]) {
		t.Fatal("read in order, with a part read again")
	}
	if in([][2]int64{{6, 1000}, {2000, 1000}}).complete() {
		t.Fatal("a gap")
	}
	if in([][2]int64{{6, int64(len(data)) - 10}}).complete() {
		t.Fatal("read short of its end")
	}
}
