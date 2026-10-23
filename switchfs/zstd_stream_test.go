package switchfs

import (
	"bytes"
	"io"
	"math/rand"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// The stream of an NCZ, from the zstd library in C with several threads or from the encoder in
// Go, is standard zstd with a window of at most nczWindowSize, which a console always has:
// the decoder in Go, limited to that window, gives the data back byte for byte.
func TestStreamEncoderWindowAndRoundTrip(t *testing.T) {
	// compressible data with repeats further apart than a block, like the data of a game
	random := rand.New(rand.NewSource(1))
	pattern := make([]byte, 3<<20)
	random.Read(pattern)
	data := make([]byte, 0, 40<<20)
	for len(data) < cap(data) {
		piece := pattern[random.Intn(len(pattern)-4096):]
		data = append(data, piece[:min(len(piece), 64<<10, cap(data)-len(data))]...)
	}
	for _, level := range []string{LevelFast, LevelBalanced, LevelMax, "1", "9", "18", "19"} {
		for _, workers := range []int{1, 4} {
			var out bytes.Buffer
			encoder, err := newStreamEncoder(&out, level, workers)
			if err != nil {
				t.Fatal(err)
			}
			for chunk := data; len(chunk) > 0; chunk = chunk[min(len(chunk), nczChunkSize):] {
				if _, err := encoder.Write(chunk[:min(len(chunk), nczChunkSize)]); err != nil {
					t.Fatal(err)
				}
			}
			if err := encoder.Close(); err != nil {
				t.Fatal(err)
			}
			var header zstd.Header
			if err := header.Decode(out.Bytes()); err != nil || header.WindowSize > nczWindowSize {
				t.Fatalf("%s with %d threads: window %d (%v)", level, workers, header.WindowSize, err)
			}
			decoder, _ := zstd.NewReader(bytes.NewReader(out.Bytes()), zstd.WithDecoderMaxWindow(nczWindowSize))
			back, err := io.ReadAll(decoder)
			decoder.Close()
			if err != nil || !bytes.Equal(back, data) {
				t.Fatalf("%s with %d threads: the data back differs (%v)", level, workers, err)
			}
			if out.Len() >= len(data) {
				t.Fatalf("%s with %d threads: nothing saved", level, workers)
			}
		}
	}
	t.Logf("zstd library in C: %v", NativeZstd)
}

// The encoder is closed once even when its Close is called again, as a deferred Close after
// the last one does: the encoder in C freed its memory twice.
func TestStreamEncoderClosesOnce(t *testing.T) {
	for _, level := range []string{LevelFast, LevelBalanced, LevelMax, "1", "9", "18", "19"} {
		var out bytes.Buffer
		encoder, err := newStreamEncoder(&out, level, 2)
		if err != nil {
			t.Fatal(err)
		}
		encoder.Write(bytes.Repeat([]byte("switch "), 1<<16))
		if err := encoder.Close(); err != nil {
			t.Fatal(err)
		}
		encoder.Close()
		encoder.Close()
	}
}

// Each block of a block compressed NCZ is a zstd frame of its own with a window of at most a
// block, at every level, from several workers at once; with the zstd
// library in C, its max level saves more than the encoder in Go does.
func TestBlockEncoderWindowAndRoundTrip(t *testing.T) {
	random := rand.New(rand.NewSource(2))
	pattern := make([]byte, 256<<10)
	random.Read(pattern)
	blocks := make([][]byte, 6)
	for i := range blocks {
		block := make([]byte, 0, 1<<nczBlockExponent)
		for len(block) < cap(block) {
			piece := pattern[random.Intn(len(pattern)-4096):]
			block = append(block, piece[:min(len(piece), 16<<10, cap(block)-len(block))]...)
		}
		blocks[i] = block
	}
	sizes := map[string]int{}
	for _, level := range []string{LevelFast, LevelBalanced, LevelMax, "1", "19"} {
		encoder, err := newBlockEncoder(level, len(blocks))
		if err != nil {
			t.Fatal(err)
		}
		out := make([][]byte, len(blocks))
		errs := make([]error, len(blocks))
		done := make(chan struct{})
		for i := range blocks {
			go func(i int) {
				out[i], errs[i] = encoder.encode(i, nil, blocks[i])
				done <- struct{}{}
			}(i)
		}
		for range blocks {
			<-done
		}
		encoder.Close()
		decoder, _ := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(1<<nczBlockExponent))
		for i := range blocks {
			if errs[i] != nil {
				t.Fatal(errs[i])
			}
			var header zstd.Header
			if err := header.Decode(out[i]); err != nil || header.WindowSize > 1<<nczBlockExponent {
				t.Fatalf("%s: window %d (%v)", level, header.WindowSize, err)
			}
			back, err := decoder.DecodeAll(out[i], nil)
			if err != nil || !bytes.Equal(back, blocks[i]) {
				t.Fatalf("%s: block %d back differs (%v)", level, i, err)
			}
			sizes[level] += len(out[i])
		}
		decoder.Close()
	}
	if NativeZstd {
		goEncoder, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(encoderLevel(LevelMax)), zstd.WithWindowSize(1<<nczBlockExponent))
		goSize := 0
		for _, block := range blocks {
			goSize += len(goEncoder.EncodeAll(block, nil))
		}
		goEncoder.Close()
		if sizes[LevelMax] >= goSize {
			t.Fatalf("max level in C %d bytes, in Go %d", sizes[LevelMax], goSize)
		}
		t.Logf("max level: %d bytes in C, %d in Go", sizes[LevelMax], goSize)
	}
}
