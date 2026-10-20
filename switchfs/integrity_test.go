package switchfs

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pfs0TreeSection is the plain data of a partition section with its hash table, and the
// change of the FS header that describes it (HierarchicalSha256).
func pfs0TreeSection() ([]byte, func([]byte)) {
	const blockSize, dataOffset, dataSize = 0x1000, 0x200, 0x7E00
	section := make([]byte, dataOffset+dataSize)
	copy(section[dataOffset:], gameData(dataSize))
	blocks := (dataSize + blockSize - 1) / blockSize
	for i := 0; i < blocks; i++ {
		end := min(dataOffset+(i+1)*blockSize, len(section))
		sum := sha256.Sum256(section[dataOffset+i*blockSize : end])
		copy(section[i*32:], sum[:])
	}
	master := sha256.Sum256(section[:blocks*32])
	return section, func(header []byte) {
		header[0x403] = 2
		copy(header[0x408:], master[:])
		binary.LittleEndian.PutUint32(header[0x428:], blockSize)
		binary.LittleEndian.PutUint32(header[0x42C:], 2)
		binary.LittleEndian.PutUint64(header[0x430:], 0)
		binary.LittleEndian.PutUint64(header[0x438:], uint64(blocks*32))
		binary.LittleEndian.PutUint64(header[0x440:], dataOffset)
		binary.LittleEndian.PutUint64(header[0x448:], dataSize)
	}
}

// ivfcTreeSection is the plain data of a romfs section with two IVFC levels: the hashes,
// then the data, each block hashed padded to its size.
func ivfcTreeSection() ([]byte, func([]byte)) {
	const blockSize, dataOffset, dataSize = 0x1000, 0x1000, 0x1800
	section := make([]byte, dataOffset+dataSize)
	copy(section[dataOffset:], gameData(dataSize))
	for i := 0; i < 2; i++ {
		block := make([]byte, blockSize)
		copy(block, section[dataOffset+i*blockSize:min(dataOffset+(i+1)*blockSize, len(section))])
		sum := sha256.Sum256(block)
		copy(section[i*32:], sum[:])
	}
	level0 := make([]byte, blockSize)
	copy(level0, section[:0x40])
	master := sha256.Sum256(level0)
	return section, func(header []byte) {
		header[0x402] = 0
		header[0x403] = 3
		copy(header[0x408:], "IVFC")
		binary.LittleEndian.PutUint32(header[0x40C:], 0x20000)
		binary.LittleEndian.PutUint32(header[0x410:], 0x20)
		binary.LittleEndian.PutUint32(header[0x414:], 3)
		levels := [][3]uint64{{0, 0x40, 12}, {dataOffset, dataSize, 12}}
		for i, level := range levels {
			entry := header[0x418+i*0x18:]
			binary.LittleEndian.PutUint64(entry[0:], level[0])
			binary.LittleEndian.PutUint64(entry[8:], level[1])
			binary.LittleEndian.PutUint32(entry[16:], uint32(level[2]))
		}
		copy(header[0x408+0xC0:], master[:])
	}
}

// rewritten is what NSC_Builder does to an NCA: a change in its header only.
func rewritten(header []byte) { header[0x21C] ^= 0x5A }

// writeRewrittenNsp writes an NSP whose NCA keeps the name (content ID) of the original
// while its header was rewritten; damage flips a byte of its data.
func writeRewrittenNsp(t *testing.T, section []byte, tree func([]byte), damage bool) string {
	t.Helper()
	original := makeProgramNca(t, section, 3, "", tree)
	nca := makeProgramNca(t, section, 3, "", tree, rewritten)
	if damage {
		nca[len(nca)-0x100] ^= 0x01
	}
	path := filepath.Join(t.TempDir(), "Game.nsp")
	os.WriteFile(path, makePFS0(pfs0Magic, []string{contentName(original, ".nca")}, [][]byte{nca}), 0o644)
	return path
}

func TestRewrittenNcaWithIntactDataIsNotDamaged(t *testing.T) {
	initNszKeys(t, "")
	for name, build := range map[string]func() ([]byte, func([]byte)){"partition": pfs0TreeSection, "romfs": ivfcTreeSection} {
		section, tree := build()
		check, err := VerifyFile(context.Background(), writeRewrittenNsp(t, section, tree, false), nil)
		if err != nil || len(check.Modified) != 1 || check.Partial {
			t.Fatalf("%s: rewritten but intact: %+v %v", name, check, err)
		}
		if _, err := VerifyFile(context.Background(), writeRewrittenNsp(t, section, tree, true), nil); err == nil || !strings.Contains(err.Error(), "does not match its hashes") {
			t.Fatalf("%s: a damaged byte of the data: %v", name, err)
		}
	}
}

func TestRewrittenNcaIsCompressedAndCheckedInTheNsz(t *testing.T) {
	initNszKeys(t, "")
	section, tree := pfs0TreeSection()
	source := writeRewrittenNsp(t, section, tree, false)
	expected, err := HashNcas(context.Background(), source, nil)
	if err != nil {
		t.Fatal(err)
	}
	target := NszPath(source)
	if _, err := CompressGame(context.Background(), source, target, CompressOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCompressed(context.Background(), target, expected, nil); err != nil {
		t.Fatalf("the copy of a rewritten NCA: %v", err)
	}
	// on its own, the NSZ is checked by the hashes of its data, without any key
	check, err := VerifyFile(context.Background(), target, nil)
	if err != nil || len(check.Modified) != 1 {
		t.Fatalf("the NSZ of a rewritten NCA: %+v %v", check, err)
	}
}

func TestCopyMustHoldEveryFileOfTheOriginal(t *testing.T) {
	initNszKeys(t, "")
	source, _ := writeTestNsp(t, t.TempDir())
	expected, err := HashNcas(context.Background(), source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := expected[testRightsId+".tik"]; !ok {
		t.Fatal("the ticket is hashed too")
	}
	target := NszPath(source)
	if _, err := CompressGame(context.Background(), source, target, CompressOptions{}); err != nil {
		t.Fatal(err)
	}
	expected["missing.nca"] = [32]byte{}
	if err := VerifyCompressed(context.Background(), target, expected, nil); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("a file of the original is missing: %v", err)
	}
	delete(expected, "missing.nca")
	ticket := expected[testRightsId+".tik"]
	ticket[0] ^= 1
	expected[testRightsId+".tik"] = ticket
	if err := VerifyCompressed(context.Background(), target, expected, nil); err == nil {
		t.Fatal("a ticket that differs from the original")
	}
}

// The check of an NSZ moves on while each NCZ is decompressed, and ends at its total.
func TestCheckOfAnNszMovesOnSmoothly(t *testing.T) {
	initNszKeys(t, "")
	source, _ := writeTestNsp(t, t.TempDir())
	target := NszPath(source)
	if _, err := CompressGame(context.Background(), source, target, CompressOptions{}); err != nil {
		t.Fatal(err)
	}
	steps, last, total := 0, int64(0), int64(0)
	if _, err := VerifyFile(context.Background(), target, func(done, all int64) {
		if done < last {
			t.Fatalf("the progress goes back: %d after %d", done, last)
		}
		steps++
		last, total = done, all
	}); err != nil {
		t.Fatal(err)
	}
	// the old check moved once per file: 5 steps for this package
	if last != total || steps < 10 {
		t.Fatalf("%d steps, %d of %d", steps, last, total)
	}
}

// The decompression of an NSZ moves on while each NCZ is decompressed too: it stayed at 0%
// for minutes on a big game, as if it had hung.
func TestDecompressionOfAnNszMovesOnSmoothly(t *testing.T) {
	initNszKeys(t, "")
	folder := t.TempDir()
	source, _ := writeTestNsp(t, folder)
	compressed := NszPath(source)
	if _, err := CompressGame(context.Background(), source, compressed, CompressOptions{}); err != nil {
		t.Fatal(err)
	}
	steps, last, total := 0, int64(0), int64(0)
	if err := DecompressNsz(context.Background(), compressed, filepath.Join(folder, "back.nsp"), func(done, all int64) {
		if done < last {
			t.Fatalf("the progress goes back: %d after %d", done, last)
		}
		steps++
		last, total = done, all
	}); err != nil {
		t.Fatal(err)
	}
	if last != total || steps < 10 {
		t.Fatalf("%d steps, %d of %d", steps, last, total)
	}
}
