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
type nativeEncoder struct {
	writer *zstd.Writer
	closed bool
	err    error
}

func (e *nativeEncoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, errEncoderClosed
	}
	return e.writer.Write(p)
}

func (e *nativeEncoder) Close() error {
	if e.closed {
		return e.err
	}
	e.closed = true
	e.err = e.writer.Close()
	return e.err
}

// newNativeStreamEncoder gives the encoder of the zstd library in C for a level it covers,
// with workers threads; ok is false for the levels left to the encoder in Go.
func newNativeStreamEncoder(output io.Writer, level string, workers int) (io.WriteCloser, bool, error) {
	if level == "" {
		level = LevelBalanced
	}
	native, ok := nativeLevels[level]
	if !ok {
		return nil, false, nil
	}
	encoder := &nativeEncoder{writer: zstd.NewWriterLevel(output, native)}
	if workers > 1 {
		if err := encoder.writer.SetNbWorkers(workers); err != nil {
			encoder.Close()
			return nil, true, err
		}
	}
	return encoder, true, nil
}
