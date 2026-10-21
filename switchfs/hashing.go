package switchfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"
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

// writtenEdge is how much of the start and of the end of a written file is kept, to read
// them back from the disk.
const writtenEdge = 64 << 10

// writtenFile is a file of a package as it was written: its SHA-256, its size, its start and
// its end.
type writtenFile struct {
	name string
	size int64
	hash hash.Hash
	head []byte
	tail []byte
}

func newWrittenFile(name string) *writtenFile {
	return &writtenFile{name: name, hash: sha256.New()}
}

func (w *writtenFile) Write(p []byte) (int, error) {
	w.hash.Write(p)
	w.size += int64(len(p))
	if missing := writtenEdge - len(w.head); missing > 0 {
		w.head = append(w.head, p[:min(len(p), missing)]...)
	}
	if len(p) >= writtenEdge {
		w.tail = append(w.tail[:0], p[len(p)-writtenEdge:]...)
	} else {
		w.tail = append(w.tail, p...)
		if extra := len(w.tail) - writtenEdge; extra > 0 {
			w.tail = append(w.tail[:0], w.tail[extra:]...)
		}
	}
	return len(p), nil
}

// checkWritten checks a package from what was written of its files: it is opened again and
// its files, their sizes and their start and end on the disk must be the ones written; an
// NCA whose hash is not its content ID is checked by the hash trees of its data.
func checkWritten(ctx context.Context, path string, written []*writtenFile) (FileCheck, error) {
	check := FileCheck{}
	input, err := os.Open(path)
	if err != nil {
		return check, err
	}
	defer input.Close()
	pkg, err := openGamePackage(input)
	if err != nil {
		return check, err
	}
	if len(pkg.files) != len(written) {
		return check, fmt.Errorf("the package holds %d files instead of %d", len(pkg.files), len(written))
	}
	var titleKeys map[string][]byte
	for i, file := range pkg.files {
		if err := ctx.Err(); err != nil {
			return check, err
		}
		want := written[i]
		if file.name != want.name || file.size != want.size {
			return check, fmt.Errorf("%s (%d bytes) was written as %s (%d bytes)", file.name, file.size, want.name, want.size)
		}
		// the start and the end of the file, from the disk
		head := make([]byte, len(want.head))
		tail := make([]byte, len(want.tail))
		if err := readAtFull(input, head, file.offset); err != nil {
			return check, err
		}
		if err := readAtFull(input, tail, file.offset+file.size-int64(len(tail))); err != nil {
			return check, err
		}
		if !bytes.Equal(head, want.head) || !bytes.Equal(tail, want.tail) {
			return check, fmt.Errorf("%s on the disk differs from what was written", file.name)
		}
		// an NCZ is checked by what it decompresses to, from the disk: its own bytes are not
		// what its content ID tells
		if strings.HasSuffix(strings.ToLower(file.name), ".ncz") {
			original := strings.TrimSuffix(file.name, ".ncz") + ".nca"
			plain := sha256.New()
			if err := decompressNcz(ctx, plain, input, file.offset, file.size, nil); err != nil {
				return check, fmt.Errorf("%s: %w", file.name, err)
			}
			if id := contentId(file.name); id == "" || hex.EncodeToString(plain.Sum(nil)[:16]) == id {
				continue
			}
			result, err := checkNczIntegrity(ctx, input, file.offset, file.size)
			if err != nil || result.Checked == 0 {
				reason := "it cannot be checked further"
				if err != nil {
					reason = err.Error()
				}
				return check, fmt.Errorf("%s does not match its content ID: the file is damaged or was modified (%s)", file.name, reason)
			}
			check.Modified = append(check.Modified, original)
			check.Partial = check.Partial || result.Unchecked > 0
			continue
		}
		if !strings.HasSuffix(strings.ToLower(file.name), ".nca") {
			continue
		}
		sum := want.hash.Sum(nil)
		if id := contentId(file.name); id == "" || hex.EncodeToString(sum[:16]) == id {
			continue
		}
		if titleKeys == nil {
			if titleKeys, err = readTickets(input, pkg.files); err != nil {
				return check, err
			}
		}
		result, err := checkNcaIntegrity(ctx, input, file.offset, file.size, titleKeys)
		if err != nil || result.Checked == 0 {
			reason := "it cannot be checked further"
			if err != nil {
				reason = err.Error()
			}
			return check, fmt.Errorf("%s does not match its content ID: the file is damaged or was modified (%s)", file.name, reason)
		}
		check.Modified = append(check.Modified, file.name)
		check.Partial = check.Partial || result.Unchecked > 0
	}
	return check, nil
}
