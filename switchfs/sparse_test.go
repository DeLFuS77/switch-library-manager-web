package switchfs

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// sparseNca builds a program NCA in sparse storage, as a base game released with its update:
// its one section covers 0x40000 bytes from 0x4000, of which it stores two pieces only, away
// from their place, each still encrypted at its place in the whole section, then its table,
// encrypted with the sparse generation.
func sparseNca(t *testing.T) []byte {
	t.Helper()
	const virtualSize, tableOffset, tableSize, physical = 0x40000, 0x20000, 0x8000, 0x4000
	// the pieces: [0x10000, 0x20000) and [0x30000, 0x40000) stored one after the other; zeros
	// between them
	entries := []sparseEntry{{0, 0, 1}, {0x10000, 0, 0}, {0x20000, 0, 1}, {0x30000, 0x10000, 0}}
	whole := makeProgramNca(t, gameData(virtualSize), 3, "", func(header []byte) {
		fs := header[0x400:]
		binary.LittleEndian.PutUint64(fs[0x148:], tableOffset)
		binary.LittleEndian.PutUint64(fs[0x150:], tableSize)
		copy(fs[0x158:], "BKTR")
		binary.LittleEndian.PutUint32(fs[0x15C:], 1)
		binary.LittleEndian.PutUint32(fs[0x160:], uint32(len(entries)))
		binary.LittleEndian.PutUint64(fs[0x168:], physical)
		binary.LittleEndian.PutUint16(fs[0x170:], 1)
	})
	// the encrypted section, as the whole NCA has it at 0x4000
	section := whole[0x4000:]
	nca := append([]byte(nil), whole[:0x4000]...)
	nca = append(nca, section[0x10000:0x20000]...)
	nca = append(nca, section[0x30000:0x40000]...)

	table := make([]byte, tableSize)
	binary.LittleEndian.PutUint32(table[4:], 1)
	binary.LittleEndian.PutUint64(table[8:], virtualSize)
	set := table[sparseNodeSize:]
	binary.LittleEndian.PutUint32(set[4:], uint32(len(entries)))
	binary.LittleEndian.PutUint64(set[8:], virtualSize)
	for i, entry := range entries {
		e := set[0x10+i*sparseEntrySize:]
		binary.LittleEndian.PutUint64(e, entry.virtual)
		binary.LittleEndian.PutUint64(e[8:], entry.physical)
		binary.LittleEndian.PutUint32(e[16:], entry.storage)
	}
	// the counter of the section (secure value 0x55667788, generation 1) with the generation
	// replaced by 1 << 16, at the place of the table in the file
	key, _ := hex.DecodeString(testNcaKey)
	block, _ := aes.NewCipher(key)
	counter := []byte{0x55, 0x66, 0x77, 0x88, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint64(counter[8:], (physical+tableOffset)>>4)
	cipher.NewCTR(block, counter).XORKeyStream(table, table)
	return append(nca, table...)
}

// An NCA in sparse storage is stored as it is unless asked; asked, it is compressed in
// NCZSECT2 and rebuilt byte for byte, solid and in blocks, also through a recompression.
func TestSparseNcaRoundTrip(t *testing.T) {
	initNszKeys(t, "")
	nca := sparseNca(t)
	source := writeOneNcaNsp(t, nca)
	if _, err := CompressGame(context.Background(), source, NszPath(source), CompressOptions{}); !errors.Is(err, ErrSparseStorage) {
		t.Fatalf("not asked: %v", err)
	}
	for _, blocks := range []bool{false, true} {
		target := NszPath(source)
		os.Remove(target)
		result, err := CompressGame(context.Background(), source, target, CompressOptions{Blocks: blocks, Sparse: true})
		if err != nil || result.Sparse != 1 || result.Compressed != 1 || result.OutputSize >= result.InputSize*3/4 {
			t.Fatalf("blocks %v: %+v %v", blocks, result, err)
		}
		if err := VerifyCompressed(context.Background(), target, result.Hashes, nil); err != nil {
			t.Fatalf("blocks %v: %v", blocks, err)
		}
		// the table of the NCZ says NCZSECT2, as the pieces move their counters
		packed, _ := os.ReadFile(target)
		if !bytes.Contains(packed, []byte(nczSectionMagicV2)) || bytes.Contains(packed, []byte(nczSectionMagic)) {
			t.Fatalf("blocks %v: not NCZSECT2", blocks)
		}
		if facts, err := DescribeFile(target); err != nil || facts.Sparse != 1 {
			t.Fatalf("blocks %v: facts %+v %v", blocks, facts, err)
		}
		back := filepath.Join(t.TempDir(), "back.nsp")
		if _, err := DecompressAndCheck(context.Background(), target, back, nil); err != nil {
			t.Fatal(err)
		}
		original, _ := os.ReadFile(source)
		rebuilt, _ := os.ReadFile(back)
		if !bytes.Equal(original, rebuilt) {
			t.Fatalf("blocks %v: the NSP back differs", blocks)
		}
		// recompressed the other way, it stays NCZSECT2 and still rebuilds
		again := filepath.Join(t.TempDir(), "again.nsz")
		if _, err := RecompressGame(context.Background(), target, again, CompressOptions{Blocks: !blocks}); err != nil {
			t.Fatalf("blocks %v: recompress: %v", blocks, err)
		}
		if err := VerifyCompressed(context.Background(), again, result.Hashes, nil); err != nil {
			t.Fatalf("blocks %v: recompressed: %v", blocks, err)
		}
	}
}

// A NCZSECT2 table whose shift is not on 16 bytes, or on a section not encrypted, is refused.
func TestNczSect2Checked(t *testing.T) {
	initNszKeys(t, "")
	source := writeOneNcaNsp(t, sparseNca(t))
	target := NszPath(source)
	result, err := CompressGame(context.Background(), source, target, CompressOptions{Sparse: true})
	if err != nil {
		t.Fatal(err)
	}
	packed, _ := os.ReadFile(target)
	at := bytes.Index(packed, []byte(nczSectionMagicV2))
	count := int(binary.LittleEndian.Uint64(packed[at+8:]))
	for i := 0; i < count; i++ {
		entry := packed[at+0x10+i*0x40:]
		if binary.LittleEndian.Uint64(entry[0x18:]) == 0 {
			continue
		}
		broken := append([]byte(nil), packed...)
		binary.LittleEndian.PutUint64(broken[at+0x10+i*0x40+0x18:], binary.LittleEndian.Uint64(entry[0x18:])+8)
		path := filepath.Join(t.TempDir(), "broken.nsz")
		os.WriteFile(path, broken, 0o644)
		if err := VerifyCompressed(context.Background(), path, result.Hashes, nil); err == nil {
			t.Fatal("a shift not on 16 bytes was taken")
		}
		return
	}
	t.Fatal("no shifted section")
}
