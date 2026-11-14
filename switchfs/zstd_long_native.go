//go:build cgo && !slm_purego

package switchfs

/*
#include <stddef.h>
#include <stdlib.h>

// the zstd library in C that github.com/DataDog/zstd builds into the program (1.5.7): only the
// few functions of its advanced API this file needs, which that package does not offer
typedef struct ZSTD_CCtx_s ZSTD_CCtx;
typedef struct { const void* src; size_t size; size_t pos; } ZSTD_inBuffer;
typedef struct { void* dst; size_t size; size_t pos; } ZSTD_outBuffer;
ZSTD_CCtx* ZSTD_createCCtx(void);
size_t ZSTD_freeCCtx(ZSTD_CCtx* cctx);
size_t ZSTD_CCtx_setParameter(ZSTD_CCtx* cctx, int param, int value);
size_t ZSTD_compressStream2(ZSTD_CCtx* cctx, ZSTD_outBuffer* output, ZSTD_inBuffer* input, int endOp);
unsigned ZSTD_isError(size_t code);
const char* ZSTD_getErrorName(size_t code);
*/
import "C"

import (
	"errors"
	"fmt"
	"io"
	"unsafe"
)

// the parameters of ZSTD_CCtx_setParameter and the directives of ZSTD_compressStream2 (zstd.h)
const (
	zstdCompressionLevel  = 100
	zstdWindowLog         = 101
	zstdEnableLongMatches = 160
	zstdNbWorkers         = 400
	zstdEndContinue       = 0
	zstdEndEnd            = 2
)

const longOutputSize = 1 << 20

// longEncoder is a zstd stream of the zstd library in C with parameters DataDog/zstd does not
// set: a window of 1 << windowLog and long distance matching, which finds the data a game
// repeats further apart than the window of the levels (8 MiB).
type longEncoder struct {
	cctx   *C.ZSTD_CCtx
	output io.Writer
	buf    []byte
	closed bool
	err    error
}

func zstdCheck(code C.size_t) error {
	if C.ZSTD_isError(code) != 0 {
		return errors.New("zstd: " + C.GoString(C.ZSTD_getErrorName(code)))
	}
	return nil
}

// newZstdEncoder is a zstd stream at a level of the library (1 to 22) with a window of
// 1 << windowLog, and long distance matching when ldm.
func newZstdEncoder(output io.Writer, native int, workers int, windowLog int, ldm bool) (io.WriteCloser, error) {
	if windowLog < 10 || windowLog > LongWindowLog {
		return nil, fmt.Errorf("the window of zstd must be from 2^10 to 2^%d, not 2^%d", LongWindowLog, windowLog)
	}
	cctx := C.ZSTD_createCCtx()
	if cctx == nil {
		return nil, errors.New("zstd: out of memory")
	}
	e := &longEncoder{cctx: cctx, output: output, buf: make([]byte, longOutputSize)}
	lm := 2 // ZSTD_ps_disable
	if ldm {
		lm = 1 // ZSTD_ps_enable
	}
	params := [][2]int{{zstdCompressionLevel, native}, {zstdWindowLog, windowLog}, {zstdEnableLongMatches, lm}}
	if workers > 1 {
		params = append(params, [2]int{zstdNbWorkers, workers})
	}
	for _, p := range params {
		if err := zstdCheck(C.ZSTD_CCtx_setParameter(cctx, C.int(p[0]), C.int(p[1]))); err != nil {
			C.ZSTD_freeCCtx(cctx)
			return nil, err
		}
	}
	return e, nil
}

// step gives the library input until it took it all, writing out what it gives back; with
// end, until the frame is finished.
func (e *longEncoder) step(p []byte, end bool) error {
	var in C.ZSTD_inBuffer
	if len(p) > 0 {
		// the library copies the input into its own buffers before it returns
		pinned := C.CBytes(p)
		defer C.free(pinned)
		in = C.ZSTD_inBuffer{src: pinned, size: C.size_t(len(p))}
	}
	directive := zstdEndContinue
	if end {
		directive = zstdEndEnd
	}
	out := (*C.char)(C.malloc(C.size_t(len(e.buf))))
	defer C.free(unsafe.Pointer(out))
	for {
		ob := C.ZSTD_outBuffer{dst: unsafe.Pointer(out), size: C.size_t(len(e.buf))}
		left := C.ZSTD_compressStream2(e.cctx, &ob, &in, C.int(directive))
		if err := zstdCheck(left); err != nil {
			return err
		}
		if ob.pos > 0 {
			if _, err := e.output.Write(C.GoBytes(unsafe.Pointer(out), C.int(ob.pos))); err != nil {
				return err
			}
		}
		if end {
			if left == 0 {
				return nil
			}
		} else if in.pos == in.size {
			return nil
		}
	}
}

func (e *longEncoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, errEncoderClosed
	}
	if err := e.step(p, false); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (e *longEncoder) Close() error {
	if e.closed {
		return e.err
	}
	e.closed = true
	e.err = e.step(nil, true)
	C.ZSTD_freeCCtx(e.cctx)
	return e.err
}
