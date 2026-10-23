package switchfs

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"testing/iotest"
)

// What is read ahead comes out as it went in, in pieces of any size, also when it ends in the
// middle of a chunk; an error of the reader comes after the data before it; closed early, the
// goroutine stops.
func TestReadAhead(t *testing.T) {
	data := bytes.Repeat([]byte("switch library "), 10000)
	for _, size := range []int{1, 7, 4096, len(data), len(data) + 1} {
		ahead := newReadAhead(bytes.NewReader(data), size, 3)
		back, err := io.ReadAll(iotest.OneByteReader(ahead))
		ahead.Close()
		if err != nil || !bytes.Equal(back, data) {
			t.Fatalf("chunks of %d: %d bytes back (%v)", size, len(back), err)
		}
	}

	broken := errors.New("broken")
	ahead := newReadAhead(io.MultiReader(bytes.NewReader(data[:5000]), iotest.ErrReader(broken)), 4096, 2)
	back, err := io.ReadAll(ahead)
	ahead.Close()
	if !errors.Is(err, broken) || !bytes.Equal(back, data[:5000]) {
		t.Fatalf("error: %d bytes back (%v)", len(back), err)
	}

	ahead = newReadAhead(bytes.NewReader(data), 16, 2)
	if _, err := io.ReadFull(ahead, make([]byte, 10)); err != nil {
		t.Fatal(err)
	}
	ahead.Close()
	ahead.Close()
}

// The blocks of an NCZ are decompressed several at once and given back in their order, over
// more blocks than a batch holds, with blocks stored as they are among them and a short last
// block; a damaged block is an error.
func TestNczBlockReaderInBatches(t *testing.T) {
	const exponent = 14
	blockSize := 1 << exponent
	encoder, _ := newBlockEncoder(LevelFast, 1)
	defer encoder.Close()
	random := make([]byte, blockSize)
	for i := range random {
		random[i] = byte(i*7919 + i>>3)
	}
	var data, stream []byte
	var sizes []uint32
	blocks := nczBlockDecoders*2 + 3
	for i := 0; i < blocks; i++ {
		block := bytes.Repeat([]byte{byte(i)}, blockSize)
		if i%5 == 0 {
			block = append([]byte(nil), random...)
			block[0] = byte(i)
		}
		if i == blocks-1 {
			block = block[:blockSize/3]
		}
		data = append(data, block...)
		out, _ := encoder.encode(0, nil, block)
		if len(out) >= len(block) {
			out = block
		}
		stream = append(stream, out...)
		sizes = append(sizes, uint32(len(out)))
	}
	head := make([]byte, 16)
	head[3] = exponent
	head[4] = byte(len(sizes))
	for i := 0; i < 8; i++ {
		head[8+i] = byte(uint64(len(data)) >> (8 * i))
	}
	table := make([]byte, 4*len(sizes))
	for i, size := range sizes {
		table[4*i], table[4*i+1], table[4*i+2], table[4*i+3] = byte(size), byte(size>>8), byte(size>>16), byte(size>>24)
	}
	read := func(stream []byte) ([]byte, error) {
		reader, err := newNczBlockReader(bytes.NewReader(append(append(append([]byte(nil), head...), table...), stream...)))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return io.ReadAll(reader)
	}
	back, err := read(stream)
	if err != nil || !bytes.Equal(back, data) {
		t.Fatalf("%d bytes back of %d (%v)", len(back), len(data), err)
	}
	damaged := append([]byte(nil), stream...)
	damaged[int(sizes[0])+int(sizes[1])+2] ^= 0xFF
	if _, err := read(damaged); err == nil {
		t.Fatal("a damaged block was read")
	}
}
