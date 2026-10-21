//go:build !cgo || slm_purego

package switchfs

import "io"

// NativeZstd tells the app is built with the zstd library in C; this build has the encoder in
// Go only.
const NativeZstd = false

func newNativeStreamEncoder(output io.Writer, level string, workers int) (io.WriteCloser, bool, error) {
	return nil, false, nil
}
