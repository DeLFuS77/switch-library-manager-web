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

// blockEncoder compresses the blocks of a block compressed NCZ, each as a zstd frame of its
// own. encode may be called by several goroutines at once, each with its own worker, from 0
// to the workers the encoder was made for: a worker may keep state of its own.
type blockEncoder interface {
	encode(worker int, dst, src []byte) ([]byte, error)
	Close() error
}

// newBlockEncoder gives the encoder of the blocks of an NCZ: the zstd library in C at the
// level itself for the levels it covers (see newStreamEncoder), else the encoder in Go,
// whose four levels compress less from the balanced level on.
func newBlockEncoder(level string, workers int) (blockEncoder, error) {
	if workers < 1 {
		workers = 1
	}
	if encoder, ok := newNativeBlockEncoder(level, workers); ok {
		return encoder, nil
	}
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(encoderLevel(level)),
		zstd.WithEncoderConcurrency(workers), zstd.WithWindowSize(1<<nczBlockExponent))
	if err != nil {
		return nil, err
	}
	return goBlockEncoder{encoder}, nil
}

type goBlockEncoder struct {
	*zstd.Encoder
}

func (e goBlockEncoder) encode(_ int, dst, src []byte) ([]byte, error) {
	return e.EncodeAll(src, dst), nil
}
