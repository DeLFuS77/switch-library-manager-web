package switchfs

import (
	"hash"
	"io"
)

// hashingReaderAt hashes the bytes of one file of a package as a compression reads them, in
// order from its start to its end: the compression reads every byte of the original anyway,
// so its hashes come without reading it a second time. A read out of that order leaves the
// hash incomplete, and the file is hashed on its own instead.
type hashingReaderAt struct {
	input      io.ReaderAt
	start, end int64
	// the next byte to hash
	next   int64
	hash   hash.Hash
	broken bool
}

func newHashingReaderAt(input io.ReaderAt, offset int64, size int64, hash hash.Hash) *hashingReaderAt {
	return &hashingReaderAt{input: input, start: offset, end: offset + size, next: offset, hash: hash}
}

func (h *hashingReaderAt) ReadAt(p []byte, offset int64) (int, error) {
	n, err := h.input.ReadAt(p, offset)
	if h.broken || n == 0 {
		return n, err
	}
	switch {
	case offset == h.next && offset+int64(n) <= h.end:
		// the bytes as they are on disk, before the compression decrypts them in p
		h.hash.Write(p[:n])
		h.next += int64(n)
	case offset+int64(n) <= h.next:
		// bytes already hashed, read again
	default:
		h.broken = true
	}
	return n, err
}

// complete tells the hash covers the whole file, read once in order.
func (h *hashingReaderAt) complete() bool {
	return !h.broken && h.next == h.end
}
