package switchfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// NCZ files made by other tools (nsz above all) must decompress byte for byte, or good
// files would be reported as damaged. These are written by hand from the format, with
// sections without encryption, so the stream is the NCA itself after its header.

func compatNca(size int) []byte {
	nca := make([]byte, size)
	random := rand.New(rand.NewSource(1))
	for i := range nca {
		if i%4096 < 3000 {
			nca[i] = byte(i / 512)
		} else {
			nca[i] = byte(random.Intn(256))
		}
	}
	return nca
}

// buildNcz writes an NCZ with the given section table (offset and size of each), solid or
// in blocks of 2^14 bytes.
func buildNcz(t *testing.T, nca []byte, sections [][2]uint64, blocks bool) []byte {
	t.Helper()
	var out bytes.Buffer
	out.Write(nca[:nczHeaderSize])
	out.WriteString(nczSectionMagic)
	binary.Write(&out, binary.LittleEndian, uint64(len(sections)))
	for _, s := range sections {
		binary.Write(&out, binary.LittleEndian, []uint64{s[0], s[1], 1, 0})
		out.Write(make([]byte, 32))
	}
	stream := nca[nczHeaderSize:]
	if !blocks {
		encoder, _ := zstd.NewWriter(&out)
		encoder.Write(stream)
		encoder.Close()
		return out.Bytes()
	}
	const exponent = 14
	encoder, _ := zstd.NewWriter(nil)
	parts := [][]byte{}
	for start := 0; start < len(stream); start += 1 << exponent {
		end := min(start+1<<exponent, len(stream))
		block := stream[start:end]
		compressed := encoder.EncodeAll(block, nil)
		if len(compressed) >= len(block) {
			compressed = block
		}
		parts = append(parts, compressed)
	}
	out.WriteString(nczBlockMagic)
	out.Write([]byte{2, 1, 0, exponent})
	binary.Write(&out, binary.LittleEndian, uint32(len(parts)))
	binary.Write(&out, binary.LittleEndian, uint64(len(stream)))
	for _, part := range parts {
		binary.Write(&out, binary.LittleEndian, uint32(len(part)))
	}
	for _, part := range parts {
		out.Write(part)
	}
	return out.Bytes()
}

func decompressBytes(t *testing.T, ncz []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := decompressNcz(context.Background(), &out, bytes.NewReader(ncz), 0, int64(len(ncz)), nil); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestNczWithManySections(t *testing.T) {
	// an update split by its subsection table: far more than 64 sections
	nca := compatNca(nczHeaderSize + 300*0x200)
	sections := [][2]uint64{}
	for i := 0; i < 300; i++ {
		sections = append(sections, [2]uint64{uint64(nczHeaderSize + i*0x200), 0x200})
	}
	if got := decompressBytes(t, buildNcz(t, nca, sections, false)); !bytes.Equal(got, nca) {
		t.Fatal("300 sections")
	}
}

func TestNczWhoseFirstSectionDoesNotStartAfterTheHeader(t *testing.T) {
	nca := compatNca(0x20000)
	// starts later: the bytes in between are in the stream, without encryption
	later := [][2]uint64{{0x6000, 0x20000 - 0x6000}}
	if got := decompressBytes(t, buildNcz(t, nca, later, false)); !bytes.Equal(got, nca) {
		t.Fatal("first section after 0x4000")
	}
	// starts earlier: the stream goes on where the copied header ends
	earlier := [][2]uint64{{0xC00, 0x20000 - 0xC00}}
	if got := decompressBytes(t, buildNcz(t, nca, earlier, false)); !bytes.Equal(got, nca) {
		t.Fatal("first section before 0x4000")
	}
}

func TestNczBlocksWithAShortStoredLastBlock(t *testing.T) {
	// the last block is random (stored as it is) and shorter than the others
	nca := compatNca(nczHeaderSize + 5*(1<<14) + 1000)
	rand.New(rand.NewSource(2)).Read(nca[len(nca)-1000:])
	ncz := buildNcz(t, nca, [][2]uint64{{nczHeaderSize, uint64(len(nca) - nczHeaderSize)}}, true)
	if got := decompressBytes(t, ncz); !bytes.Equal(got, nca) {
		t.Fatal("block NCZ")
	}
}

// TestExportCompatNcz writes the NCZ files above, named by the content ID of their NCA, to
// check them with the decompressor of nsz (SLM_NCZ_EXPORT is the folder).
func TestExportCompatNcz(t *testing.T) {
	folder := os.Getenv("SLM_NCZ_EXPORT")
	if folder == "" {
		t.Skip("set SLM_NCZ_EXPORT")
	}
	write := func(nca []byte, ncz []byte) {
		os.WriteFile(filepath.Join(folder, contentName(nca, ".ncz")), ncz, 0o644)
	}
	many := compatNca(nczHeaderSize + 300*0x200)
	sections := [][2]uint64{}
	for i := 0; i < 300; i++ {
		sections = append(sections, [2]uint64{uint64(nczHeaderSize + i*0x200), 0x200})
	}
	write(many, buildNcz(t, many, sections, false))
	shifted := compatNca(0x20000)
	write(shifted, buildNcz(t, shifted, [][2]uint64{{0x6000, 0x20000 - 0x6000}}, false))
	blocks := compatNca(nczHeaderSize + 5*(1<<14) + 1000)
	rand.New(rand.NewSource(2)).Read(blocks[len(blocks)-1000:])
	write(blocks, buildNcz(t, blocks, [][2]uint64{{nczHeaderSize, uint64(len(blocks) - nczHeaderSize)}}, true))
}
