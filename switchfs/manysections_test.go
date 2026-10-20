package switchfs

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// makeBktrNcaN builds an update patch NCA whose AesCtrEx section has n subsections, each
// with its own counter, followed by the subsection table: real updates have hundreds.
func makeBktrNcaN(t *testing.T, n int, subSize int) []byte {
	t.Helper()
	dataSize := n * subSize
	table := make([]byte, 0x8000)
	binary.LittleEndian.PutUint32(table[4:], 1)
	binary.LittleEndian.PutUint64(table[8:], uint64(dataSize))
	bucket := table[0x4000:]
	binary.LittleEndian.PutUint32(bucket[4:], uint32(n))
	binary.LittleEndian.PutUint64(bucket[8:], uint64(dataSize))
	for i := 0; i < n; i++ {
		entry := bucket[0x10+0x10*i:]
		binary.LittleEndian.PutUint64(entry[0:], uint64(i*subSize))
		binary.LittleEndian.PutUint32(entry[12:], uint32(10+i))
	}
	section := append(gameData(dataSize), table...)
	nca := makeProgramNca(t, section, 4, "", func(header []byte) {
		binary.LittleEndian.PutUint64(header[0x400+0x120:], uint64(dataSize))
		binary.LittleEndian.PutUint64(header[0x400+0x128:], 0x8000)
		copy(header[0x400+0x130:], "BKTR")
	})
	contentKey, _ := hex.DecodeString(testNcaKey)
	block, _ := aes.NewCipher(contentKey)
	crypt := func(start int, size int, generation uint32) {
		counter := []byte{0x55, 0x66, 0x77, 0x88, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
		binary.BigEndian.PutUint32(counter[4:], generation)
		binary.BigEndian.PutUint64(counter[8:], uint64(0x4000+start)>>4)
		part := nca[0x4000+start : 0x4000+start+size]
		cipher.NewCTR(block, counter).XORKeyStream(part, part)
	}
	for i := 0; i < n; i++ {
		crypt(i*subSize, subSize, uint32(10+i))
	}
	crypt(dataSize, 0x8000, 1)
	return nca
}

// An update with many subsections makes an NCZ with as many sections. Its size must be known
// before decompressing it (the free space is reserved first), and it must come back exact.
func TestNczWithManySectionsDecompresses(t *testing.T) {
	initNszKeys(t, "")
	folder := t.TempDir()
	patch := makeBktrNcaN(t, 100, 0x1000)
	source := filepath.Join(folder, "update.nsp")
	original := makePFS0(pfs0Magic, []string{contentName(patch, ".nca")}, [][]byte{patch})
	os.WriteFile(source, original, 0o644)

	target := NszPath(source)
	if _, err := CompressNsp(context.Background(), source, target, CompressOptions{}); err != nil {
		t.Fatal(err)
	}
	nsz, _ := os.ReadFile(target)
	pfs0, _ := ReadPfs0File(target)
	ncz := nsz[pfs0.Files[0].StartOffset:]
	if count := binary.LittleEndian.Uint64(ncz[0x4008:]); count <= 64 {
		t.Fatalf("the NCZ needs more than 64 sections for this check: %v", count)
	}

	size, err := DecompressedSize(target)
	if err != nil {
		t.Fatalf("the size of an NCZ with many sections must be known: %v", err)
	}
	if size != int64(len(original)) {
		t.Fatalf("decompressed size %v, want %v", size, len(original))
	}
	restored := filepath.Join(folder, "restored.nsp")
	if err := DecompressNsz(context.Background(), target, restored, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(restored)
	if !bytes.Equal(data, original) {
		t.Fatal("an update with many subsections must come back byte for byte")
	}
}

// blockRoundTrip compresses an NSP with one program NCA in blocks, checks it decompresses back
// byte for byte, and gives the sizes of its blocks.
func blockRoundTrip(t *testing.T, plain []byte) []uint32 {
	t.Helper()
	initNszKeys(t, "")
	folder := t.TempDir()
	game := makeProgramNca(t, plain, 3, "")
	source := filepath.Join(folder, "game.nsp")
	original := makePFS0(pfs0Magic, []string{contentName(game, ".nca")}, [][]byte{game})
	os.WriteFile(source, original, 0o644)
	target := NszPath(source)
	if _, err := CompressNsp(context.Background(), source, target, CompressOptions{Blocks: true, Workers: 3}); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(folder, "restored.nsp")
	if err := DecompressNsz(context.Background(), target, restored, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(restored)
	if !bytes.Equal(data, original) {
		t.Fatal("a block compressed NCZ must come back byte for byte")
	}
	nsz, _ := os.ReadFile(target)
	pfs0, _ := ReadPfs0File(target)
	ncz := nsz[pfs0.Files[0].StartOffset:]
	count := binary.LittleEndian.Uint64(ncz[0x4008:])
	at := 0x4010 + int(count)*0x40
	if string(ncz[at:at+8]) != nczBlockMagic {
		t.Fatal("the NCZ must be written in blocks")
	}
	blocks := binary.LittleEndian.Uint32(ncz[at+12:])
	sizes := make([]uint32, blocks)
	for i := range sizes {
		sizes[i] = binary.LittleEndian.Uint32(ncz[at+24+4*i:])
	}
	return sizes
}

// nsz issue 210: when the data is an exact multiple of the block size, the last block is a
// whole block, not one of size zero.
func TestBlocksOfAnExactMultiple(t *testing.T) {
	sizes := blockRoundTrip(t, gameData(2<<20))
	if len(sizes) != 2 {
		t.Fatalf("two whole blocks: %v", sizes)
	}
}

// nsz issues 79 and 190: a short last block that does not shrink is stored as it is, and is
// read back with its own size, not the size of a whole block.
func TestShortLastBlockStoredAsItIs(t *testing.T) {
	random := make([]byte, 0x1000)
	rand.New(rand.NewSource(7)).Read(random)
	sizes := blockRoundTrip(t, append(gameData(1<<20), random...))
	if len(sizes) != 2 || sizes[1] != 0x1000 {
		t.Fatalf("the last block is stored as it is, 0x1000 bytes: %v", sizes)
	}
	if sizes[0] >= 1<<20 {
		t.Fatalf("the first block compresses: %v", sizes)
	}
}
