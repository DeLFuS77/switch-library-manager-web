package switchfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// The XXH64 of the checksum gives the values of its reference, and the low 32 bits the encoder
// in Go writes after its frames, for sizes around its stripes of 32 bytes.
func TestXXH64(t *testing.T) {
	for text, want := range map[string]uint64{
		"":    0xEF46DB3751D8E999,
		"a":   0xD24EC4F1A98C6E5B,
		"abc": 0x44BC2CF5AD770999,
		"Nobody inspects the spammish repetition": 0xFBCEA83C8A378BF1,
	} {
		h := newXXH64()
		h.Write([]byte(text))
		if got := h.Sum64(); got != want {
			t.Errorf("%q: %016x, want %016x", text, got, want)
		}
	}
	encoder, _ := zstd.NewWriter(nil, zstd.WithEncoderCRC(true))
	defer encoder.Close()
	random := rand.New(rand.NewSource(1))
	for _, n := range []int{1, 3, 7, 8, 31, 32, 33, 63, 64, 65, 1000, 1<<20 + 7} {
		data := make([]byte, n)
		random.Read(data)
		frame := encoder.EncodeAll(data, nil)
		// in pieces of every size, as a stream gives them
		h := newXXH64()
		for rest := data; len(rest) > 0; {
			k := min(len(rest), 1+random.Intn(70))
			h.Write(rest[:k])
			rest = rest[k:]
		}
		if got, want := uint32(h.Sum64()), binary.LittleEndian.Uint32(frame[len(frame)-4:]); got != want {
			t.Fatalf("%d bytes: %08x, the encoder in Go writes %08x", n, got, want)
		}
	}
}

// A frame without checksum gets one that decoders check: the frame decodes, and a wrong
// checksum is refused. The same for a frame written as a stream through ZstdChecksumFrame.
func TestAddZstdChecksum(t *testing.T) {
	plain, _ := zstd.NewWriter(nil, zstd.WithEncoderCRC(false))
	defer plain.Close()
	decoder, _ := zstd.NewReader(nil)
	defer decoder.Close()
	data := bytes.Repeat([]byte("checked data, "), 100_000)
	frame, err := AddZstdChecksum(plain.EncodeAll(data, nil), data)
	if err != nil || frame[4]&0x04 == 0 {
		t.Fatalf("%v, descriptor %02x", err, frame[4])
	}
	if got, err := decoder.DecodeAll(frame, nil); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("the frame with checksum: %v", err)
	}
	frame[len(frame)-1] ^= 1
	if _, err := decoder.DecodeAll(frame, nil); err == nil {
		t.Fatal("a wrong checksum is taken")
	}
	if _, err := AddZstdChecksum([]byte("not zstd"), data); err == nil {
		t.Fatal("a frame that is not zstd takes a checksum")
	}

	var out bytes.Buffer
	stream := NewZstdChecksumFrame(&out)
	writer, _ := zstd.NewWriter(stream, zstd.WithEncoderCRC(false), zstd.WithEncoderConcurrency(2))
	for rest := data; len(rest) > 0; {
		k := min(len(rest), 77_777)
		stream.Content(rest[:k])
		writer.Write(rest[:k])
		rest = rest[k:]
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stream.Finish(); err != nil {
		t.Fatal(err)
	}
	got, err := decoder.DecodeAll(out.Bytes(), nil)
	if err != nil || !bytes.Equal(got, data) || out.Bytes()[4]&0x04 == 0 {
		t.Fatalf("the stream with checksum: %v", err)
	}
	b := out.Bytes()
	b[len(b)-2] ^= 1
	if _, err := decoder.DecodeAll(b, nil); err == nil {
		t.Fatal("a wrong checksum of a stream is taken")
	}
}

// The encoders of an NCZ, the zstd library in C when the app has it, write frames with their
// checksum, in one stream and in blocks.
func TestNczEncodersWriteChecksums(t *testing.T) {
	data := bytes.Repeat([]byte("an NCZ with checksums "), 200_000)
	decoder, _ := zstd.NewReader(nil)
	defer decoder.Close()
	for _, level := range []string{LevelFast, LevelBalanced, LevelMax, "19"} {
		var out bytes.Buffer
		encoder, err := newStreamEncoder(&out, level, 3)
		if err != nil {
			t.Fatal(err)
		}
		encoder.Write(data[:1000])
		encoder.Write(data[1000:])
		if err := encoder.Close(); err != nil {
			t.Fatal(err)
		}
		if out.Bytes()[4]&0x04 == 0 {
			t.Errorf("%s: the stream has no checksum", level)
		}
		if got, err := decoder.DecodeAll(out.Bytes(), nil); err != nil || !bytes.Equal(got, data) {
			t.Fatalf("%s: %v", level, err)
		}
		blocks, err := newBlockEncoder(level, 2, 16)
		if err != nil {
			t.Fatal(err)
		}
		frame, err := blocks.encode(1, nil, data[:1<<16])
		blocks.Close()
		if err != nil || frame[4]&0x04 == 0 {
			t.Fatalf("%s: a block has no checksum (%v)", level, err)
		}
		if got, err := decoder.DecodeAll(frame, nil); err != nil || !bytes.Equal(got, data[:1<<16]) {
			t.Fatalf("%s: a block: %v", level, err)
		}
	}
}

// An NSZ in blocks of another size has it in its block header, verifies as the original and
// stands on its own; sizes readers do not take are refused.
func TestBlockExponent(t *testing.T) {
	initNszKeys(t, "")
	source := writeOneNcaNsp(t, blocksNca(t))
	expected, err := HashNcas(context.Background(), source, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, exponent := range []int{MinBlockExponent, 16, MaxBlockExponent} {
		target := strings.TrimSuffix(NszPath(source), ".nsz") + "-" + string(rune('a'+exponent-MinBlockExponent)) + ".nsz"
		if _, err := CompressGame(context.Background(), source, target, CompressOptions{Blocks: true, Workers: 3, BlockExponent: exponent}); err != nil {
			t.Fatal(err)
		}
		if err := VerifyCompressed(context.Background(), target, expected, nil); err != nil {
			t.Fatalf("2^%d: %v", exponent, err)
		}
		data, _ := os.ReadFile(target)
		at := bytes.Index(data, []byte(nczBlockMagic))
		if at < 0 || int(data[at+11]) != exponent {
			t.Fatalf("2^%d: the block header says %d", exponent, data[at+11])
		}
	}
	for _, exponent := range []int{MinBlockExponent - 1, MaxBlockExponent + 1} {
		if _, err := CompressGame(context.Background(), source, NszPath(source)+".bad", CompressOptions{Blocks: true, BlockExponent: exponent}); err == nil {
			t.Errorf("2^%d is taken", exponent)
		}
	}
}
