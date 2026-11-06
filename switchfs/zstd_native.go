//go:build cgo && !slm_purego

package switchfs

import (
	"errors"
	"io"

	"github.com/DataDog/zstd"
)

// NativeZstd tells the app is built with the zstd library in C: its balanced and max levels
// compress one stream on several threads.
const NativeZstd = true

// the levels of the zstd library in C for the levels of the app: 11 compresses as well as
// the max level in Go and as fast as its balanced one with several threads, 19 is the default
// of nsz. Both keep the window at 8 MiB at most (4 MiB at 11), which a console always has.
var nativeLevels = map[string]int{LevelBalanced: 11, LevelMax: 19}

var errEncoderClosed = errors.New("the zstd encoder is closed")

// nativeEncoder is the encoder of the zstd library in C, closed once: its Close frees the
// memory of C, and a second one freed it again, which broke the memory of the app (the encoder
// in Go does not mind).
// Its frame gets the checksum the encoder in Go writes (zstd_checksum.go).
type nativeEncoder struct {
	writer *zstd.Writer
	frame  *ZstdChecksumFrame
	closed bool
	err    error
}

func (e *nativeEncoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, errEncoderClosed
	}
	n, err := e.writer.Write(p)
	e.frame.Content(p[:n])
	return n, err
}

func (e *nativeEncoder) Close() error {
	if e.closed {
		return e.err
	}
	e.closed = true
	e.err = e.writer.Close()
	if e.err == nil {
		e.err = e.frame.Finish()
	}
	return e.err
}

// nativeLevel is the level of the zstd library in C for a level of the app; ok is false for
// the levels left to the encoder in Go.
func nativeLevel(level string) (int, bool) {
	if level == "" {
		level = LevelBalanced
	}
	if native, ok := nativeLevels[level]; ok {
		return native, true
	}
	return levelNumber(level)
}

// newNativeStreamEncoder gives the encoder of the zstd library in C for a level it covers,
// with workers threads; ok is false for the levels left to the encoder in Go.
func newNativeStreamEncoder(output io.Writer, level string, workers int) (io.WriteCloser, bool, error) {
	native, ok := nativeLevel(level)
	if !ok {
		return nil, false, nil
	}
	frame := NewZstdChecksumFrame(output)
	encoder := &nativeEncoder{writer: zstd.NewWriterLevel(frame, native), frame: frame}
	if workers > 1 {
		if err := encoder.writer.SetNbWorkers(workers); err != nil {
			encoder.Close()
			return nil, true, err
		}
	}
	return encoder, true, nil
}

// nativeJobSize is the data each thread of the zstd library in C takes at once on one stream
// at a level of the app, 0 for the levels left to the encoder in Go: 4 times the window of
// the level (zstdmt_compress.c), which grows with the level (clevels.h).
func nativeJobSize(level string) int64 {
	native, ok := nativeLevel(level)
	if !ok {
		return 0
	}
	windowLog := 23
	switch {
	case native <= 1:
		windowLog = 19
	case native == 2:
		windowLog = 20
	case native <= 8:
		windowLog = 21
	case native <= 16:
		windowLog = 22
	}
	return 1 << (windowLog + 2)
}

// nativeBlockEncoder compresses each block with the zstd library in C, one context per
// worker. A block is 1 MiB and its size is known, so the library keeps the window to it.
type nativeBlockEncoder struct {
	level    int
	contexts []zstd.Ctx
}

func (e nativeBlockEncoder) encode(worker int, dst, src []byte) ([]byte, error) {
	out, err := e.contexts[worker].CompressLevel(dst, src, e.level)
	if err != nil {
		return nil, err
	}
	return AddZstdChecksum(out, src)
}

// Close leaves the memory of the contexts to their finalizers, which free it.
func (e nativeBlockEncoder) Close() error {
	return nil
}

// newNativeBlockEncoder gives the block encoder of the zstd library in C for a level it
// covers, for workers goroutines; ok is false for the levels left to the encoder in Go.
func newNativeBlockEncoder(level string, workers int, _ int) (blockEncoder, bool) {
	native, ok := nativeLevel(level)
	if !ok {
		return nil, false
	}
	encoder := nativeBlockEncoder{level: native, contexts: make([]zstd.Ctx, workers)}
	for i := range encoder.contexts {
		encoder.contexts[i] = zstd.NewCtx()
	}
	return encoder, true
}
