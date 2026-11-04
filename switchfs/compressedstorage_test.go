package switchfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// compressedNca builds a program NCA whose one section is a RomFS in compressed storage, as games
// since firmware 12 have: the hash levels, then the data level with its blocks (four LZ4 ones,
// made at the level made and told at told, and one stored as it is) and the table of the
// compressed storage after them: a bucket tree, its first node then its entry set, with padding
// bytes after it within the size the FS header tells. change changes the FS header last.
func compressedNca(t *testing.T, told, made int8, padding int, change ...func(fs []byte)) []byte {
	t.Helper()
	const hashLevels = 0x4000
	type block struct {
		kind        byte
		raw, stored []byte
	}
	blocks := []block{}
	for i := 0; i < 4; i++ {
		raw := studyData(int64(20 + i))
		stored, _ := nintendoLz4(raw, made)
		blocks = append(blocks, block{3, raw, stored})
	}
	stored := gameData(64 << 10)
	blocks = append(blocks[:2], append([]block{{0, stored, stored}}, blocks[2:]...)...)

	data := []byte{}
	table := make([]byte, 2*bucketNodeSize+padding)
	set := table[bucketNodeSize:]
	virtual := uint64(0)
	for i, b := range blocks {
		e := set[0x10+i*0x18:]
		binary.LittleEndian.PutUint64(e, virtual)
		binary.LittleEndian.PutUint64(e[8:], uint64(len(data)))
		e[16], e[17] = b.kind, byte(told)
		binary.LittleEndian.PutUint32(e[20:], uint32(len(b.stored)))
		data = append(data, b.stored...)
		virtual += uint64(len(b.raw))
	}
	// the first node: one entry set, and the end of the decompressed data
	binary.LittleEndian.PutUint32(table[4:], 1)
	binary.LittleEndian.PutUint64(table[8:], virtual)
	binary.LittleEndian.PutUint32(set[4:], uint32(len(blocks)))
	binary.LittleEndian.PutUint64(set[8:], virtual)
	for len(data)%0x10 != 0 {
		data = append(data, 0)
	}
	tableOffset := len(data)
	data = append(data, table...)

	plain := append(gameData(hashLevels), data...)
	for len(plain)%0x200 != 0 {
		plain = append(plain, 0)
	}
	return makeProgramNca(t, plain, 3, "", func(header []byte) {
		fs := header[0x400:]
		fs[0x2], fs[0x3] = 0, 3
		copy(fs[0x8:], "IVFC")
		binary.LittleEndian.PutUint32(fs[0x14:], 7)
		binary.LittleEndian.PutUint64(fs[0x18+5*0x18:], hashLevels)
		binary.LittleEndian.PutUint64(fs[0x18+5*0x18+8:], uint64(len(data)))
		binary.LittleEndian.PutUint64(fs[0x178:], uint64(tableOffset))
		binary.LittleEndian.PutUint64(fs[0x180:], uint64(len(table)))
		copy(fs[0x188:], "BKTR")
		binary.LittleEndian.PutUint32(fs[0x18C:], 1)
		binary.LittleEndian.PutUint32(fs[0x190:], uint32(len(blocks)))
		for _, c := range change {
			c(fs)
		}
	})
}

// compressBack compresses an NSP with Rebuild, checks the copy, decompresses it and compares it
// with the original; it gives the result and the NSZ.
func compressBack(t *testing.T, source string) (CompressResult, []byte) {
	t.Helper()
	target := NszPath(source)
	result, err := CompressGame(context.Background(), source, target, CompressOptions{Rebuild: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyCompressed(context.Background(), target, result.Hashes, nil); err != nil {
		t.Fatal(err)
	}
	back := filepath.Join(t.TempDir(), "back.nsp")
	if _, err := DecompressAndCheck(context.Background(), target, back, nil); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(source)
	rebuilt, _ := os.ReadFile(back)
	if !bytes.Equal(original, rebuilt) {
		t.Fatal("the NSP back differs")
	}
	packed, _ := os.ReadFile(target)
	return result, packed
}

// The table of a compressed storage is read as Atmosphère reads it, its entry set right after
// its first node, also when the table is bigger than its tree (#63 of NXPress: "invalid bucket
// tree"); the LZ4 blocks go into an NCZSECT3 and come back byte for byte.
func TestCompressedStorageRebuilt(t *testing.T) {
	initNszKeys(t, "")
	for _, padding := range []int{0, 0x8000} {
		for _, level := range []int8{0, 5} {
			result, packed := compressBack(t, writeOneNcaNsp(t, compressedNca(t, level, level, padding)))
			if result.Rebuilt != 4 || result.Lz4Blocks != 4 || len(result.Lz4Unread) != 0 {
				t.Fatalf("padding %#x level %d: %+v", padding, level, result)
			}
			if !bytes.Contains(packed, []byte(nczSectionMagicV3)) {
				t.Fatalf("padding %#x level %d: not NCZSECT3", padding, level)
			}
		}
	}
}

// An NCA whose LZ4 blocks are found but none is made again byte for byte gives the NCZ every tool
// reads, not an NCZSECT3 without its table (#63 of NXPress: "invalid NCZSECT3 table").
func TestNoBlockMadeAgainGivesPlainNcz(t *testing.T) {
	initNszKeys(t, "")
	// told level 0, made at level 5
	source := writeOneNcaNsp(t, compressedNca(t, 0, 5, 0))
	result, packed := compressBack(t, source)
	if result.Rebuilt != 0 || result.Lz4Blocks != 4 || result.Compressed != 1 {
		t.Fatalf("%+v", result)
	}
	if bytes.Contains(packed, []byte(nczSectionMagicV3)) || !bytes.Contains(packed, []byte(nczSectionMagic)) {
		t.Fatal("not NCZSECTN")
	}
	// a plain NSZ, which is recompressed as any other
	again := filepath.Join(t.TempDir(), "again.nsz")
	if _, err := RecompressGame(context.Background(), NszPath(source), again, CompressOptions{Blocks: true}); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCompressed(context.Background(), again, result.Hashes, nil); err != nil {
		t.Fatal(err)
	}
}

// A compressed storage that cannot be read leaves the LZ4 blocks of its NCA as they are, and is
// told: the game is still compressed, as a plain NSZ.
func TestUnreadableCompressedStorageStillCompresses(t *testing.T) {
	initNszKeys(t, "")
	perSet := (bucketNodeSize - 0x10) / 0x18
	for name, change := range map[string]func(fs []byte){
		// more entries than the table holds
		"count": func(fs []byte) { binary.LittleEndian.PutUint32(fs[0x190:], uint32(perSet+1)) },
		// a table past the data level
		"offset": func(fs []byte) { binary.LittleEndian.PutUint64(fs[0x178:], 1<<40) },
	} {
		result, packed := compressBack(t, writeOneNcaNsp(t, compressedNca(t, 0, 0, 0, change)))
		if result.Rebuilt != 0 || result.Lz4Blocks != 0 || len(result.Lz4Unread) != 1 || !strings.Contains(result.Lz4Unread[0], ".nca: ") {
			t.Fatalf("%s: %+v", name, result)
		}
		if bytes.Contains(packed, []byte(nczSectionMagicV3)) {
			t.Fatalf("%s: NCZSECT3", name)
		}
	}
}

// bucketEntries reads the entry sets right after the first node, whatever follows them, and
// refuses a count the table cannot hold before taking memory for it.
func TestBucketEntries(t *testing.T) {
	perSet := (bucketNodeSize - 0x10) / 0x18
	count := perSet + 3
	// the first node, two entry sets and padding
	table := make([]byte, 4*bucketNodeSize)
	binary.LittleEndian.PutUint32(table[4:], 2)
	binary.LittleEndian.PutUint64(table[8:], uint64(count)<<16)
	index := 0
	for s, n := range []int{perSet, 3} {
		node := table[(1+s)*bucketNodeSize:]
		binary.LittleEndian.PutUint32(node[4:], uint32(n))
		for k := 0; k < n; k++ {
			binary.LittleEndian.PutUint64(node[0x10+k*0x18:], uint64(index)<<16)
			index++
		}
	}
	entries, end, err := bucketEntries(table, 0x18, count)
	if err != nil || len(entries) != count || end != uint64(count)<<16 {
		t.Fatalf("%d entries, end %#x: %v", len(entries), end, err)
	}
	for i, e := range entries {
		if binary.LittleEndian.Uint64(e) != uint64(i)<<16 {
			t.Fatalf("entry %d is %#x", i, binary.LittleEndian.Uint64(e))
		}
	}
	// a count the table cannot hold, a second level of nodes, or none
	for _, bad := range []int{0, -1, 3*perSet + 1, 2047 * perSet, 1 << 30} {
		if _, _, err := bucketEntries(table, 0x18, bad); err == nil {
			t.Errorf("count %d taken", bad)
		}
	}
	// a set that tells more entries than a node holds
	binary.LittleEndian.PutUint32(table[2*bucketNodeSize+4:], uint32(perSet+1))
	if _, _, err := bucketEntries(table, 0x18, count); err == nil {
		t.Error("a set of too many entries taken")
	}
}
