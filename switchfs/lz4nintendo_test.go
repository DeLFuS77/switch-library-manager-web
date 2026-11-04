package switchfs

import (
	"bytes"
	"hash/crc32"
	"io"
	"math/rand"
	"testing"
	"time"
)

// studyData is 64 KiB that LZ4 finds matches in: text, runs, some noise.
func studyData(seed int64) []byte {
	r := rand.New(rand.NewSource(seed))
	words := []string{"switch ", "game ", "update ", "zelda ", "nintendo ", "\x00\x00\x00\x00", "level_", "texture "}
	data := []byte{}
	for len(data) < 64<<10 {
		switch r.Intn(4) {
		case 0:
			data = append(data, words[r.Intn(len(words))]...)
		case 1:
			data = append(data, bytes.Repeat([]byte{byte(r.Intn(256))}, r.Intn(40))...)
		default:
			data = append(data, byte(r.Intn(256)))
		}
	}
	return data[:64<<10]
}

// The compressors of Nintendo write blocks that decompress to what they were given.
func TestNintendoLz4Decompresses(t *testing.T) {
	inputs := [][]byte{make([]byte, 64<<10), bytes.Repeat([]byte("abc"), 100), []byte("short"), studyData(1), studyData(2)}
	for i, input := range inputs {
		for _, level := range []int8{0, lz4Level0Hash4, 1, 5, 9, 16} {
			block, known := nintendoLz4(input, level)
			if !known {
				t.Fatal("level not known")
			}
			back, err := lz4Decompress(block, len(input))
			if err != nil || !bytes.Equal(back, input) {
				t.Fatalf("input %d level %d: %v", i, level, err)
			}
			if len(input) > 1000 && len(block) >= len(input) {
				t.Errorf("input %d level %d: %d bytes from %d", i, level, len(block), len(input))
			}
		}
	}
	for _, level := range []int8{-1, 17, lz4Level0Hash4 - 1} {
		if _, known := nintendoLz4(inputs[0], level); known {
			t.Errorf("level %d is not one of Nintendo's", level)
		}
	}
}

// The compressors make what they made when they made every block of the games tried again: the
// fast one with each hash and LZ4HC at levels 5 and 16 searching its chain more, which finds
// longer matches. The sizes and the CRC-32 of their blocks of one input are kept here, so that
// any change to them shows.
func TestNintendoLz4MakesTheSameBlocks(t *testing.T) {
	input := studyData(1)
	made := map[int8][]byte{}
	for _, want := range []struct {
		level int8
		size  int
		crc   uint32
	}{{0, 22469, 0x4f72d095}, {lz4Level0Hash4, 22579, 0x6b6f0e99}, {5, 19312, 0x556aefdb}, {16, 18187, 0xe6acdf4c}} {
		block, _ := nintendoLz4(input, want.level)
		made[want.level] = block
		if len(block) != want.size || crc32.ChecksumIEEE(block) != want.crc {
			t.Errorf("level %d: %d bytes, CRC-32 %#08x", want.level, len(block), crc32.ChecksumIEEE(block))
		}
	}
	if bytes.Equal(made[0], made[lz4Level0Hash4]) || len(made[16]) >= len(made[5]) {
		t.Error("the compressors are not told apart")
	}
}

// A block of level 0 is tried with each hash of the fast compressor.
func TestNintendoLz4LevelsOfABlock(t *testing.T) {
	if levels := nintendoLz4Levels(0); len(levels) != 2 || levels[0] != 0 || levels[1] != lz4Level0Hash4 {
		t.Errorf("level 0: %v", levels)
	}
	if levels := nintendoLz4Levels(16); len(levels) != 1 || levels[0] != 16 {
		t.Errorf("level 16: %v", levels)
	}
}

// A damaged block is refused, not decompressed into something else.
func TestLz4DecompressRefusesDamagedBlocks(t *testing.T) {
	block, _ := nintendoLz4(studyData(3), 0)
	for _, damaged := range [][]byte{block[:len(block)-3], append([]byte{0xF0}, block...), {0x1F, 0x41, 0xFF, 0xFF}} {
		if _, err := lz4Decompress(damaged, 64<<10); err == nil {
			t.Error("a damaged block was decompressed")
		}
	}
}

// NCZSECT3: the blocks of the table go into the stream decompressed and come back byte for byte;
// a block the compressors do not make again stays as it is. A block of level 0 made with the hash
// of 4 bytes is told so in the table.
func TestLz4BlocksKeptDecompressedComeBack(t *testing.T) {
	rawFast, rawHC, rawFast4, rawHC16 := studyData(4), studyData(5), studyData(6), studyData(7)
	fast, _ := nintendoLz4(rawFast, 0)
	hc, _ := nintendoLz4(rawHC, 5)
	fast4, _ := nintendoLz4(rawFast4, lz4Level0Hash4)
	hc16, _ := nintendoLz4(rawHC16, 16)
	foreign, _ := nintendoLz4(rawFast, 5)
	nca := bytes.Repeat([]byte{0x42}, 0x100)
	at := func(block []byte) uint64 { return uint64(nczHeaderSize + len(nca)) }
	blocks := []lz4Block{}
	add := func(block []byte, raw []byte, level int8) {
		blocks = append(blocks, lz4Block{offset: at(block), size: uint32(len(block)), rawSize: uint32(len(raw)), level: level})
		nca = append(nca, block...)
		nca = append(nca, "between"...)
	}
	add(fast, rawFast, 0)
	add(hc, rawHC, 5)
	// told as level 0, but made at level 5: not made again, so kept as it is
	add(foreign, rawFast, 0)
	add(fast4, rawFast4, 0)
	add(hc16, rawHC16, 16)

	stream := &bytes.Buffer{}
	expander := newLz4Expander(stream, blocks, nczHeaderSize, 3)
	// written in pieces that cut the blocks anywhere
	for rest := nca; len(rest) > 0; {
		n := min(len(rest), 1000)
		if _, err := expander.Write(rest[:n]); err != nil {
			t.Fatal(err)
		}
		rest = rest[n:]
	}
	kept, err := expander.finish()
	if err != nil || len(kept) != 4 || kept[0].level != 0 || kept[1].level != 5 || kept[2].level != lz4Level0Hash4 || kept[3].level != 16 {
		t.Fatalf("kept %+v: %v", kept, err)
	}
	if stream.Len() != len(nca)-len(fast)-len(hc)-len(fast4)-len(hc16)+len(rawFast)+len(rawHC)+len(rawFast4)+len(rawHC16) {
		t.Fatalf("stream of %d bytes", stream.Len())
	}

	table := &bytes.Buffer{}
	if err := writeLz4Table(table, kept, nczHeaderSize+int64(stream.Len())); err != nil {
		t.Fatal(err)
	}
	file := append(append(make([]byte, nczHeaderSize), stream.Bytes()...), table.Bytes()...)
	kept, end, err := readLz4Table(bytes.NewReader(file), int64(len(file)))
	if err != nil || len(kept) != 4 || kept[2].level != lz4Level0Hash4 || end != nczHeaderSize+int64(stream.Len()) {
		t.Fatalf("table: %v %d %d", err, len(kept), end)
	}
	back := &bytes.Buffer{}
	rebuilder := newLz4Rebuilder(bytes.NewReader(stream.Bytes()), kept, nczHeaderSize, 3)
	defer rebuilder.Close()
	if _, err := back.ReadFrom(rebuilder); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back.Bytes(), nca) {
		t.Fatal("the NCA does not come back")
	}
}

// archiveOf builds an NCA of many LZ4 blocks with bytes between them, the stream an NCZSECT3
// keeps of it and its table.
func archiveOf(t *testing.T, count int) ([]byte, []byte, []lz4Block) {
	nca, stream, blocks := []byte{}, []byte{}, []lz4Block{}
	r := rand.New(rand.NewSource(7))
	for i := 0; i < count; i++ {
		gap := bytes.Repeat([]byte{byte(i)}, r.Intn(3000))
		nca = append(nca, gap...)
		stream = append(stream, gap...)
		raw := studyData(int64(i))[:1000+r.Intn(64<<10-1000)]
		level := int8(5 * (i % 2))
		block, _ := nintendoLz4(raw, level)
		blocks = append(blocks, lz4Block{offset: nczHeaderSize + uint64(len(nca)), size: uint32(len(block)), rawSize: uint32(len(raw)), level: level})
		nca = append(nca, block...)
		stream = append(stream, raw...)
	}
	tail := []byte("the end of the NCA")
	return append(nca, tail...), append(stream, tail...), blocks
}

// Blocks made again on several workers come out in the order of the NCA, whatever the size of
// the reads.
func TestLz4RebuildOnWorkersKeepsTheOrder(t *testing.T) {
	nca, stream, blocks := archiveOf(t, 40)
	for _, workers := range []int{1, 8} {
		rebuilder := newLz4Rebuilder(bytes.NewReader(stream), blocks, nczHeaderSize, workers)
		back := &bytes.Buffer{}
		if _, err := io.CopyBuffer(back, struct{ io.Reader }{rebuilder}, make([]byte, 777)); err != nil {
			t.Fatal(err)
		}
		rebuilder.Close()
		if !bytes.Equal(back.Bytes(), nca) {
			t.Fatalf("%d workers: the NCA does not come back", workers)
		}
	}
}

// A block that is not made again to its size, or a stream that ends early, stops the reading
// with an error.
func TestLz4RebuildTellsWhatFails(t *testing.T) {
	_, stream, blocks := archiveOf(t, 6)
	wrong := append([]lz4Block(nil), blocks...)
	wrong[3].size++
	rebuilder := newLz4Rebuilder(bytes.NewReader(stream), wrong, nczHeaderSize, 4)
	if _, err := io.Copy(io.Discard, rebuilder); err == nil {
		t.Error("a block of the wrong size was accepted")
	}
	rebuilder.Close()
	short := newLz4Rebuilder(bytes.NewReader(stream[:len(stream)/2]), blocks, nczHeaderSize, 4)
	if _, err := io.Copy(io.Discard, short); err == nil {
		t.Error("a stream that ends early was accepted")
	}
	short.Close()
}

// Closed halfway, as when a decompression is cancelled, the rebuilder stops its goroutines.
func TestLz4RebuildClosesHalfway(t *testing.T) {
	_, stream, blocks := archiveOf(t, 40)
	rebuilder := newLz4Rebuilder(bytes.NewReader(stream), blocks, nczHeaderSize, 4)
	io.ReadFull(rebuilder, make([]byte, 5000))
	done := make(chan struct{})
	go func() {
		rebuilder.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return")
	}
}

// Blocks checked on several workers go into the stream in the order of the NCA, and all the
// ones made again are kept, whatever the size of the writes.
func TestLz4ExpandOnWorkersKeepsTheOrder(t *testing.T) {
	nca, want, blocks := archiveOf(t, 40)
	for _, workers := range []int{1, 8} {
		stream := &bytes.Buffer{}
		expander := newLz4Expander(stream, blocks, nczHeaderSize, workers)
		for rest := nca; len(rest) > 0; {
			n := min(len(rest), 4321)
			expander.Write(rest[:n])
			rest = rest[n:]
		}
		kept, err := expander.finish()
		if err != nil || len(kept) != len(blocks) {
			t.Fatalf("%d workers: kept %d of %d: %v", workers, len(kept), len(blocks), err)
		}
		for i := range kept {
			if kept[i] != blocks[i] {
				t.Fatalf("%d workers: block %d out of order", workers, i)
			}
		}
		if !bytes.Equal(stream.Bytes(), want) {
			t.Fatalf("%d workers: the stream is not the NCA with its blocks decompressed", workers)
		}
	}
}

type failingWriter struct{ left int }

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.left -= len(p); w.left < 0 {
		return 0, io.ErrShortWrite
	}
	return len(p), nil
}

// A write that fails halfway is told by finish, which still stops every goroutine.
func TestLz4ExpandTellsAFailedWrite(t *testing.T) {
	nca, _, blocks := archiveOf(t, 20)
	expander := newLz4Expander(&failingWriter{left: 100 << 10}, blocks, nczHeaderSize, 4)
	expander.Write(nca)
	done := make(chan error)
	go func() {
		_, err := expander.finish()
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("the failed write was not told")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("finish did not return")
	}
}
