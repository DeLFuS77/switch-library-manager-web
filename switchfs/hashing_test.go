package switchfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
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

// A decompression checks the NSP while writing it: the same result as reading it again with
// VerifyFile, the same NSP as the original, a damaged NCA refused and the NSP removed.
func TestDecompressionChecksWhileWriting(t *testing.T) {
	initNszKeys(t, "")
	section, tree := pfs0TreeSection()
	plain, _ := writeTestNsp(t, t.TempDir())
	for name, source := range map[string]string{"nsp": plain, "rewritten": writeRewrittenNsp(t, section, tree, false)} {
		nsz := filepath.Join(t.TempDir(), "game.nsz")
		if _, err := CompressGame(context.Background(), source, nsz, CompressOptions{}); err != nil {
			t.Fatal(err)
		}
		back := filepath.Join(t.TempDir(), "back.nsp")
		check, err := DecompressAndCheck(context.Background(), nsz, back, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		again, err := VerifyFile(context.Background(), back, nil)
		if err != nil || !reflect.DeepEqual(check, again) {
			t.Fatalf("%s: %+v, reading it again %+v (%v)", name, check, again, err)
		}
		original, _ := HashNcas(context.Background(), source, nil)
		hashes, _ := HashNcas(context.Background(), back, nil)
		if !reflect.DeepEqual(original, hashes) {
			t.Fatalf("%s: the NSP differs from the original", name)
		}
	}

	damaged := writeRewrittenNsp(t, section, tree, true)
	nsz := filepath.Join(t.TempDir(), "damaged.nsz")
	if _, err := CompressGame(context.Background(), damaged, nsz, CompressOptions{}); err != nil {
		t.Fatal(err)
	}
	back := filepath.Join(t.TempDir(), "back.nsp")
	if _, err := DecompressAndCheck(context.Background(), nsz, back, nil); err == nil || !strings.Contains(err.Error(), "damaged or was modified") {
		t.Fatalf("a damaged NCA: %v", err)
	}
	if _, err := os.Stat(back); !os.IsNotExist(err) {
		t.Fatal("the NSP of a damaged NCA is removed")
	}
}

// What is on the disk must be what was written: a file with another start or end, another
// size or another name is refused.
func TestCheckWrittenComparesTheDisk(t *testing.T) {
	initNszKeys(t, "")
	source, _ := writeTestNsp(t, t.TempDir())
	nsz := filepath.Join(t.TempDir(), "game.nsz")
	if _, err := CompressGame(context.Background(), source, nsz, CompressOptions{}); err != nil {
		t.Fatal(err)
	}
	back := filepath.Join(t.TempDir(), "back.nsp")
	written, err := decompressNsz(context.Background(), nsz, back, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkWritten(context.Background(), back, written); err != nil {
		t.Fatal(err)
	}
	changes := map[string]func(w *writtenFile){
		"start": func(w *writtenFile) { w.head[0] ^= 1 },
		"end":   func(w *writtenFile) { w.tail[len(w.tail)-1] ^= 1 },
		"size":  func(w *writtenFile) { w.size++ },
		"name":  func(w *writtenFile) { w.name = "other.nca" },
	}
	for name, change := range changes {
		copied := *written[0]
		copied.head = append([]byte(nil), written[0].head...)
		copied.tail = append([]byte(nil), written[0].tail...)
		change(&copied)
		altered := append([]*writtenFile{&copied}, written[1:]...)
		if _, err := checkWritten(context.Background(), back, altered); err == nil {
			t.Errorf("another %s", name)
		}
	}
	if _, err := checkWritten(context.Background(), back, written[1:]); err == nil {
		t.Error("a file missing")
	}
}
