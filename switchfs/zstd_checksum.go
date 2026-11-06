package switchfs

import (
	"encoding/binary"
	"errors"
	"io"
	"math/bits"
)

// The checksum of a zstd frame is the low 32 bits of the XXH64 (seed 0) of its content, after
// its last block, and bit 2 of its frame header descriptor tells it is there. The encoder in Go
// writes it; the Go package of the zstd library in C cannot ask for it, so its frames get it
// added here. Every zstd decoder checks it when it is there: a damaged NSZ or NUP fails to
// decompress instead of giving damaged data, and a file can be checked without keys by
// decompressing it alone.

const (
	xxPrime1 uint64 = 11400714785074694791
	xxPrime2 uint64 = 14029467366897019727
	xxPrime3 uint64 = 1609587929392839161
	xxPrime4 uint64 = 9650029242287828579
	xxPrime5 uint64 = 2870177450012600261
)

// xxh64 is the XXH64 hash with seed 0, written as it comes.
type xxh64 struct {
	v      [4]uint64
	total  uint64
	buf    [32]byte
	filled int
}

func newXXH64() *xxh64 {
	// in variables, so the sums wrap as the reference does
	p1, p2 := xxPrime1, xxPrime2
	return &xxh64{v: [4]uint64{p1 + p2, p2, 0, -p1}}
}

func xxRound(acc, input uint64) uint64 {
	acc += input * xxPrime2
	return bits.RotateLeft64(acc, 31) * xxPrime1
}

func xxMerge(acc, v uint64) uint64 {
	acc ^= xxRound(0, v)
	return acc*xxPrime1 + xxPrime4
}

func (h *xxh64) stripe(p []byte) {
	h.v[0] = xxRound(h.v[0], binary.LittleEndian.Uint64(p[0:]))
	h.v[1] = xxRound(h.v[1], binary.LittleEndian.Uint64(p[8:]))
	h.v[2] = xxRound(h.v[2], binary.LittleEndian.Uint64(p[16:]))
	h.v[3] = xxRound(h.v[3], binary.LittleEndian.Uint64(p[24:]))
}

func (h *xxh64) Write(p []byte) (int, error) {
	n := len(p)
	h.total += uint64(n)
	if h.filled > 0 {
		k := copy(h.buf[h.filled:], p)
		h.filled += k
		p = p[k:]
		if h.filled < 32 {
			return n, nil
		}
		h.stripe(h.buf[:])
		h.filled = 0
	}
	for len(p) >= 32 {
		h.stripe(p)
		p = p[32:]
	}
	h.filled = copy(h.buf[:], p)
	return n, nil
}

func (h *xxh64) Sum64() uint64 {
	var acc uint64
	if h.total >= 32 {
		acc = bits.RotateLeft64(h.v[0], 1) + bits.RotateLeft64(h.v[1], 7) + bits.RotateLeft64(h.v[2], 12) + bits.RotateLeft64(h.v[3], 18)
		for _, v := range h.v {
			acc = xxMerge(acc, v)
		}
	} else {
		acc = h.v[2] + xxPrime5
	}
	acc += h.total
	p := h.buf[:h.filled]
	for len(p) >= 8 {
		acc ^= xxRound(0, binary.LittleEndian.Uint64(p))
		acc = bits.RotateLeft64(acc, 27)*xxPrime1 + xxPrime4
		p = p[8:]
	}
	if len(p) >= 4 {
		acc ^= uint64(binary.LittleEndian.Uint32(p)) * xxPrime1
		acc = bits.RotateLeft64(acc, 23)*xxPrime2 + xxPrime3
		p = p[4:]
	}
	for _, b := range p {
		acc ^= uint64(b) * xxPrime5
		acc = bits.RotateLeft64(acc, 11) * xxPrime1
	}
	acc ^= acc >> 33
	acc *= xxPrime2
	acc ^= acc >> 29
	acc *= xxPrime3
	acc ^= acc >> 32
	return acc
}

var errChecksumFrame = errors.New("zstd wrote a frame that cannot take a checksum")

const zstdFrameMagic = 0xFD2FB528

// AddZstdChecksum adds to frame, one zstd frame without a checksum that holds content, the
// checksum of content, and gives the frame with it.
func AddZstdChecksum(frame, content []byte) ([]byte, error) {
	if len(frame) < 6 || binary.LittleEndian.Uint32(frame) != zstdFrameMagic {
		return nil, errChecksumFrame
	}
	if frame[4]&0x04 != 0 {
		return frame, nil
	}
	h := newXXH64()
	h.Write(content)
	frame[4] |= 0x04
	return binary.LittleEndian.AppendUint32(frame, uint32(h.Sum64())), nil
}

// ZstdChecksumFrame passes on the one zstd frame an encoder writes, with its checksum: the
// encoder writes to it, Content is given the data the encoder is given, and Finish adds the
// checksum once the encoder has ended the frame.
type ZstdChecksumFrame struct {
	w    io.Writer
	hash *xxh64
	head []byte
	done bool
	has  bool
}

// NewZstdChecksumFrame gives the writer of a frame with checksum into w.
func NewZstdChecksumFrame(w io.Writer) *ZstdChecksumFrame {
	return &ZstdChecksumFrame{w: w, hash: newXXH64()}
}

// Content takes the data given to the encoder, in order.
func (f *ZstdChecksumFrame) Content(p []byte) {
	f.hash.Write(p)
}

// Write passes on the frame the encoder writes, with the checksum bit set in its header.
func (f *ZstdChecksumFrame) Write(p []byte) (int, error) {
	n := len(p)
	if !f.done {
		take := min(5-len(f.head), len(p))
		f.head = append(f.head, p[:take]...)
		p = p[take:]
		if len(f.head) < 5 {
			return n, nil
		}
		if binary.LittleEndian.Uint32(f.head) != zstdFrameMagic {
			return 0, errChecksumFrame
		}
		f.has = f.head[4]&0x04 != 0
		f.head[4] |= 0x04
		if _, err := f.w.Write(f.head); err != nil {
			return 0, err
		}
		f.done = true
	}
	if len(p) > 0 {
		if _, err := f.w.Write(p); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// Finish writes the checksum after the frame, which the encoder has ended.
func (f *ZstdChecksumFrame) Finish() error {
	if !f.done || f.has {
		return nil
	}
	_, err := f.w.Write(binary.LittleEndian.AppendUint32(nil, uint32(f.hash.Sum64())))
	return err
}
