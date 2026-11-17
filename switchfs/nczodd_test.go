package switchfs

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// Other tools write NSZ and XCZ files this app does not: sections that begin inside the
// copied NCA header, PFS0 and HFS0 headers larger than their entries need, game cards whose
// partitions and files are aligned only to 0x200, blocks whose last one is short and stored
// as it is. Each is read, verified and decompressed, and the NCA files that come out must be
// the originals byte for byte. The files are made by hand from the format.

// oddSection is a section of an NCZ: its place in the NCA and whether it is encrypted
// (AES-CTR, crypto type 3) with the key and counter given.
type oddSection struct {
	offset, size uint64
	encrypted    bool
	key, counter []byte
}

// oddNca is an NCA of random bytes, as an encrypted one looks.
func oddNca(size int, seed int64) []byte {
	nca := make([]byte, size)
	random := rand.New(rand.NewSource(seed))
	for i := range nca {
		// half compressible, so the blocks are not all stored as they are
		if i%8192 < 4096 {
			nca[i] = byte(i / 1024)
		} else {
			nca[i] = byte(random.Intn(256))
		}
	}
	return nca
}

// oddNcz writes the NCZ of nca with the sections given, solid or in blocks of 2^exponent
// bytes. The stream is the NCA from 0x4000 on, with the encrypted sections decrypted.
func oddNcz(t *testing.T, nca []byte, sections []oddSection, blockExponent int) []byte {
	t.Helper()
	stream := append([]byte(nil), nca[nczHeaderSize:]...)
	var out bytes.Buffer
	out.Write(nca[:nczHeaderSize])
	out.WriteString(nczSectionMagic)
	binary.Write(&out, binary.LittleEndian, uint64(len(sections)))
	for _, s := range sections {
		cryptoType := uint64(1)
		key, counter := make([]byte, 16), make([]byte, 16)
		if s.encrypted {
			cryptoType = 3
			key, counter = s.key, s.counter
			block, err := aes.NewCipher(key)
			if err != nil {
				t.Fatal(err)
			}
			// the part of the section after the copied header is in the stream, decrypted
			start := s.offset
			if start < nczHeaderSize {
				start = nczHeaderSize
			}
			for position := start &^ 0xF; position < s.offset+s.size; position += 16 {
				pad := make([]byte, 16)
				cipher.NewCTR(block, nczCounter(counter, position)).XORKeyStream(pad, pad)
				for i := uint64(0); i < 16; i++ {
					at := position + i
					if at >= start && at < s.offset+s.size {
						stream[at-nczHeaderSize] ^= pad[i]
					}
				}
			}
		}
		binary.Write(&out, binary.LittleEndian, []uint64{s.offset, s.size, cryptoType, 0})
		out.Write(key)
		out.Write(counter)
	}
	if blockExponent == 0 {
		encoder, _ := zstd.NewWriter(&out)
		encoder.Write(stream)
		encoder.Close()
		return out.Bytes()
	}
	encoder, _ := zstd.NewWriter(nil)
	parts := [][]byte{}
	for start := 0; start < len(stream); start += 1 << blockExponent {
		block := stream[start:min(start+1<<blockExponent, len(stream))]
		compressed := encoder.EncodeAll(block, nil)
		if len(compressed) >= len(block) {
			compressed = block
		}
		parts = append(parts, compressed)
	}
	out.WriteString(nczBlockMagic)
	out.Write([]byte{2, 1, 0, byte(blockExponent)})
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

// oddContainer writes a PFS0 or HFS0 whose header takes headerLen bytes at least (the string
// table padded with zeros up to it) and whose files start at multiples of align from the
// start of their data.
func oddContainer(magic string, names []string, files [][]byte, headerLen int, align int) []byte {
	entrySize := PfsfileEntryTableSize
	if magic == hfs0Magic {
		entrySize = HfsfileEntryTableSize
	}
	var table []byte
	nameOffsets := []int{}
	for _, name := range names {
		nameOffsets = append(nameOffsets, len(table))
		table = append(append(table, name...), 0)
	}
	if needed := 0x10 + entrySize*len(names) + len(table); headerLen > needed {
		table = append(table, make([]byte, headerLen-needed)...)
	}
	var data []byte
	offsets := []int{}
	for _, file := range files {
		if pad := (align - len(data)%align) % align; pad > 0 {
			data = append(data, make([]byte, pad)...)
		}
		offsets = append(offsets, len(data))
		data = append(data, file...)
	}
	header := make([]byte, 0x10+entrySize*len(names))
	copy(header, magic)
	binary.LittleEndian.PutUint32(header[4:], uint32(len(names)))
	binary.LittleEndian.PutUint32(header[8:], uint32(len(table)))
	for i := range names {
		entry := header[0x10+entrySize*i:]
		binary.LittleEndian.PutUint64(entry[0:], uint64(offsets[i]))
		binary.LittleEndian.PutUint64(entry[8:], uint64(len(files[i])))
		binary.LittleEndian.PutUint32(entry[16:], uint32(nameOffsets[i]))
		if magic == hfs0Magic {
			// the hashed region: 0x200 at most, like the cards
			binary.LittleEndian.PutUint32(entry[20:], uint32(min(len(files[i]), 0x200)))
		}
	}
	return append(append(header, table...), data...)
}

// oddGame is the NCA files of a game and their NCZ: one whose first section begins inside
// the copied header and is encrypted, one in blocks whose last block is short and stored as
// it is, and a metadata NCA left as it is.
type oddGame struct {
	ncas  [][]byte
	names []string
	files [][]byte
}

func makeOddGame(t *testing.T) oddGame {
	t.Helper()
	key := bytes.Repeat([]byte{0x5A}, 16)
	counter := append(bytes.Repeat([]byte{0xC3}, 8), make([]byte, 8)...)

	// (a) sections that begin before 0x4000: a small one wholly inside the header, then an
	// encrypted one that starts in it and goes on after it
	early := oddNca(0x30000, 3)
	earlyNcz := oddNcz(t, early, []oddSection{
		{offset: 0xC00, size: 0x400},
		{offset: 0x1000, size: 0x30000 - 0x1000, encrypted: true, key: key, counter: counter},
	}, 0)

	// (d) blocks of 2^14, the last one short and random, so it is stored as it is
	blocks := oddNca(nczHeaderSize+6*(1<<14)+777, 4)
	rand.New(rand.NewSource(5)).Read(blocks[len(blocks)-777:])
	blocksNcz := oddNcz(t, blocks, []oddSection{
		{offset: nczHeaderSize, size: uint64(len(blocks) - nczHeaderSize), encrypted: true, key: key, counter: counter},
	}, 14)

	meta := bytes.Repeat([]byte("m"), 0x200)
	return oddGame{
		ncas:  [][]byte{early, blocks, meta},
		names: []string{contentName(early, ".ncz"), contentName(blocks, ".ncz"), contentName(meta, ".cnmt.nca")},
		files: [][]byte{earlyNcz, blocksNcz, meta},
	}
}

// checkOddPackage verifies the package at path, decompresses it and checks that every NCA
// that comes out is the original.
func checkOddPackage(t *testing.T, path string, game oddGame, extension string) {
	t.Helper()
	ctx := context.Background()
	if err := VerifyNsz(ctx, path, nil, nil); err != nil {
		t.Fatalf("verify: %v", err)
	}
	target := strings.TrimSuffix(path, filepath.Ext(path)) + extension
	if err := DecompressNsz(ctx, path, target, nil); err != nil {
		t.Fatalf("decompress: %v", err)
	}
	hashes, err := HashNcas(ctx, target, nil)
	if err != nil {
		t.Fatalf("hash the decompressed file: %v", err)
	}
	if err := VerifyNsz(ctx, target, nil, nil); err != nil {
		t.Fatalf("verify the decompressed file: %v", err)
	}
	output, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	pkg, err := openGamePackage(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.files) != len(game.ncas) {
		t.Fatalf("%d files decompressed, %d expected", len(pkg.files), len(game.ncas))
	}
	for i, nca := range game.ncas {
		name := contentName(nca, ".nca")
		if i == len(game.ncas)-1 {
			name = contentName(nca, ".cnmt.nca")
		}
		if _, ok := hashes[name]; !ok {
			t.Fatalf("%s missing after decompression", name)
		}
		file := pkg.files[i]
		got := make([]byte, file.size)
		if err := readAtFull(output, got, file.offset); err != nil {
			t.Fatal(err)
		}
		if file.name != name || !bytes.Equal(got, nca) {
			t.Fatalf("%s: not the original NCA", file.name)
		}
	}
}

// (a), (b) and (d) in an NSZ whose PFS0 header is padded far beyond what its entries need.
func TestOddNsz(t *testing.T) {
	game := makeOddGame(t)
	for _, headerLen := range []int{0, 0x200, 0x4000} {
		folder := t.TempDir()
		path := filepath.Join(folder, "odd.nsz")
		if err := os.WriteFile(path, oddContainer(pfs0Magic, game.names, game.files, headerLen, 1), 0o644); err != nil {
			t.Fatal(err)
		}
		checkOddPackage(t, path, game, ".nsp")
	}
}

// (a), (b), (c) and (d) in an XCZ: a root partition that does not start at 0xF000, partitions
// and files aligned to 0x200 only, HFS0 headers padded beyond what their entries need.
func TestOddXcz(t *testing.T) {
	game := makeOddGame(t)
	for _, layout := range []struct {
		rootOffset int
		headerLen  int
	}{{0xF000, 0}, {0x10200, 0}, {0xF000, 0x8000}, {0x10200, 0x1234}} {
		secure := oddContainer(hfs0Magic, game.names, game.files, layout.headerLen, 0x200)
		update := oddContainer(hfs0Magic, nil, nil, layout.headerLen, 0x200)
		normal := oddContainer(hfs0Magic, nil, nil, 0, 0x200)
		root := oddContainer(hfs0Magic, []string{"update", "normal", "secure"}, [][]byte{update, normal, secure}, layout.headerLen, 0x200)
		card := make([]byte, layout.rootOffset)
		copy(card[0x100:], "HEAD")
		binary.LittleEndian.PutUint64(card[0x130:], uint64(layout.rootOffset))
		folder := t.TempDir()
		path := filepath.Join(folder, "odd.xcz")
		if err := os.WriteFile(path, append(card, root...), 0o644); err != nil {
			t.Fatal(err)
		}
		checkOddPackage(t, path, game, ".xci")
	}
}

// Sections that lie wholly inside the copied header are skipped, however many there are; a
// table whose sections are all inside the header is refused.
func TestNczSectionsInsideTheHeader(t *testing.T) {
	nca := compatNca(0x20000)
	inside := [][2]uint64{{0x200, 0x200}, {0x400, 0x800}, {0xC00, 0x3400}, {nczHeaderSize, 0x20000 - nczHeaderSize}}
	if got := decompressBytes(t, buildNcz(t, nca, inside, false)); !bytes.Equal(got, nca) {
		t.Fatal("sections inside the header")
	}
	across := [][2]uint64{{0x200, 0x200}, {0x400, 0x20000 - 0x400}}
	if got := decompressBytes(t, buildNcz(t, nca, across, false)); !bytes.Equal(got, nca) {
		t.Fatal("a section inside the header, then one across its end")
	}
	var out bytes.Buffer
	only := buildNcz(t, nca[:nczHeaderSize], [][2]uint64{{0x200, 0x200}}, false)
	if decompressNcz(context.Background(), &out, bytes.NewReader(only), 0, int64(len(only)), nil) == nil {
		t.Fatal("only sections inside the header")
	}
}
