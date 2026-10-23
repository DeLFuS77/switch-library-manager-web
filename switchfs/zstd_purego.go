//go:build !cgo || slm_purego

package switchfs

import "io"

// NativeZstd tells the app is built with the zstd library in C; this build has the encoder in
// Go only.
const NativeZstd = false

func newNativeStreamEncoder(output io.Writer, level string, workers int) (io.WriteCloser, bool, error) {
	return nil, false, nil
}

func nativeJobSize(level string) int64 {
	return 0
}

func newNativeBlockEncoder(level string, workers int) (blockEncoder, bool) {
	return nil, false
}
