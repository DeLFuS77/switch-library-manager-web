package switchfs

import (
	"io"

	"github.com/klauspost/compress/zstd"
)

// newStreamEncoder gives the encoder of the zstd stream of an NCZ: the zstd library in C,
// several threads on one stream, for the balanced and the max level when the app is built
// with it (see zstd_native.go), else the encoder in Go, one thread on one stream. Both write
// the same standard zstd, with a window of at most nczWindowSize, which every reader of NSZ
// reads alike.
func newStreamEncoder(output io.Writer, level string, workers int) (io.WriteCloser, error) {
	if workers < 1 {
		workers = 1
	}
	if encoder, ok, err := newNativeStreamEncoder(output, level, workers); ok {
		return encoder, err
	}
	return zstd.NewWriter(output, zstd.WithEncoderLevel(encoderLevel(level)),
		zstd.WithEncoderConcurrency(workers), zstd.WithWindowSize(nczWindowSize))
}
