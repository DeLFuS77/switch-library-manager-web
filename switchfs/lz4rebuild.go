package switchfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

// NCZSECT3 (experimental, archive only): an NCZ whose stream holds the LZ4 blocks of the
// compressed storage of its NCA decompressed, so that zstd finds what LZ4 hid, and that makes
// them again with the compressors of Nintendo (lz4nintendo.go) when the NCA is rebuilt.
//
//   - The section table is NCZSECT2's, under the magic "NCZSECT3": tools that do not know it stop
//     there, as with NCZSECT2.
//   - In the stream, each block of the table takes its decompressed size in place of its own.
//   - After the stream: "NCZLZ4RB", the number of blocks (u64), the size of the table (u64) and
//     the table compressed with zstd: per block its offset in the NCA (u64), its size there
//     (u32), its decompressed size (u32) and its level (s8), 17 bytes.
//   - The last 16 bytes: where "NCZLZ4RB" starts after the start of the NCZ (u64) and
//     "NCZLZ4FT".
//
// A block is written so only when it is made again byte for byte; any other stays as LZ4.
// Installers do not read it: the NCZ is turned back to an NSP or an NSZ before installing.
const (
	nczSectionMagicV3 = "NCZSECT3"
	nczLz4TableMagic  = "NCZLZ4RB"
	nczLz4FooterMagic = "NCZLZ4FT"
	lz4TableEntrySize = 17
)

// countingWriter tells how many bytes went through it.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// lz4Expander passes the decrypted NCA on to the encoder, and every LZ4 block that is made again
// byte for byte decompressed. position is the place in the NCA of the next byte written.
type lz4Expander struct {
	out      io.Writer
	blocks   []lz4Block
	next     int
	position uint64
	current  []byte
	kept     []lz4Block
	keptRaw  int64
}

func (e *lz4Expander) Write(p []byte) (int, error) {
	written := len(p)
	for len(p) > 0 {
		if e.current != nil {
			block := e.blocks[e.next-1]
			take := min(len(p), int(block.size)-len(e.current))
			e.current = append(e.current, p[:take]...)
			p = p[take:]
			e.position += uint64(take)
			if len(e.current) == int(block.size) {
				if err := e.flushBlock(block); err != nil {
					return 0, err
				}
			}
			continue
		}
		if e.next >= len(e.blocks) || e.blocks[e.next].offset >= e.position+uint64(len(p)) {
			if _, err := e.out.Write(p); err != nil {
				return 0, err
			}
			e.position += uint64(len(p))
			break
		}
		block := e.blocks[e.next]
		if block.offset > e.position {
			before := block.offset - e.position
			if _, err := e.out.Write(p[:before]); err != nil {
				return 0, err
			}
			p = p[before:]
			e.position += before
		}
		e.current = make([]byte, 0, block.size)
		e.next++
	}
	return written, nil
}

// flushBlock writes a whole block: decompressed when it is made again byte for byte, else as it is.
func (e *lz4Expander) flushBlock(block lz4Block) error {
	stored := e.current
	e.current = nil
	if raw, err := lz4Decompress(stored, int(block.rawSize)); err == nil {
		if made, known := nintendoLz4(raw, block.level); known && bytes.Equal(made, stored) {
			e.kept = append(e.kept, block)
			e.keptRaw += int64(len(raw))
			_, err := e.out.Write(raw)
			return err
		}
	}
	_, err := e.out.Write(stored)
	return err
}

// writeLz4Table writes the table of the blocks kept decompressed and the footer after the
// stream; start is where the table begins after the start of the NCZ.
func writeLz4Table(output io.Writer, blocks []lz4Block, start int64) error {
	entries := make([]byte, 0, len(blocks)*lz4TableEntrySize)
	for _, block := range blocks {
		entries = binary.LittleEndian.AppendUint64(entries, block.offset)
		entries = binary.LittleEndian.AppendUint32(entries, block.size)
		entries = binary.LittleEndian.AppendUint32(entries, block.rawSize)
		entries = append(entries, byte(block.level))
	}
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBetterCompression))
	if err != nil {
		return err
	}
	packed := encoder.EncodeAll(entries, nil)
	encoder.Close()
	table := []byte(nczLz4TableMagic)
	table = binary.LittleEndian.AppendUint64(table, uint64(len(blocks)))
	table = binary.LittleEndian.AppendUint64(table, uint64(len(packed)))
	table = append(table, packed...)
	table = binary.LittleEndian.AppendUint64(table, uint64(start))
	table = append(table, nczLz4FooterMagic...)
	_, err = output.Write(table)
	return err
}

// readLz4Table reads the table of an NCZSECT3 of size bytes, and gives where its stream ends.
func readLz4Table(reader io.ReaderAt, size int64) ([]lz4Block, int64, error) {
	bad := errors.New("invalid NCZSECT3 table")
	footer := make([]byte, 16)
	if size < 16+24 {
		return nil, 0, bad
	}
	if _, err := reader.ReadAt(footer, size-16); err != nil {
		return nil, 0, err
	}
	start := int64(binary.LittleEndian.Uint64(footer))
	if string(footer[8:]) != nczLz4FooterMagic || start < nczHeaderSize || start > size-16-24 {
		return nil, 0, bad
	}
	head := make([]byte, 24)
	if _, err := reader.ReadAt(head, start); err != nil {
		return nil, 0, err
	}
	count, packedSize := binary.LittleEndian.Uint64(head[8:]), binary.LittleEndian.Uint64(head[16:])
	if string(head[:8]) != nczLz4TableMagic || packedSize != uint64(size-16-start-24) || count > uint64(size) {
		return nil, 0, bad
	}
	packed := make([]byte, packedSize)
	if _, err := reader.ReadAt(packed, start+24); err != nil {
		return nil, 0, err
	}
	// the window of a small table is bigger than the table itself
	limit := count*lz4TableEntrySize + 1
	if limit < 1<<20 {
		limit = 1 << 20
	}
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(limit))
	if err != nil {
		return nil, 0, err
	}
	defer decoder.Close()
	entries, err := decoder.DecodeAll(packed, nil)
	if err != nil || uint64(len(entries)) != count*lz4TableEntrySize {
		return nil, 0, bad
	}
	blocks := make([]lz4Block, count)
	next := uint64(nczHeaderSize)
	for i := range blocks {
		e := entries[i*lz4TableEntrySize:]
		blocks[i] = lz4Block{offset: binary.LittleEndian.Uint64(e), size: binary.LittleEndian.Uint32(e[8:]), rawSize: binary.LittleEndian.Uint32(e[12:]), level: int8(e[16])}
		if blocks[i].offset < next || blocks[i].rawSize > 1<<24 || blocks[i].size == 0 {
			return nil, 0, bad
		}
		next = blocks[i].offset + uint64(blocks[i].size)
	}
	return blocks, start, nil
}

// lz4Rebuilder reads the stream of an NCZSECT3 and gives the decrypted NCA: each block of the
// table is read decompressed and made again. position is the place in the NCA of the next byte.
type lz4Rebuilder struct {
	in       io.Reader
	blocks   []lz4Block
	next     int
	position uint64
	pending  []byte
}

func (r *lz4Rebuilder) Read(p []byte) (int, error) {
	if len(r.pending) > 0 {
		n := copy(p, r.pending)
		r.pending = r.pending[n:]
		r.position += uint64(n)
		return n, nil
	}
	if r.next < len(r.blocks) && r.blocks[r.next].offset == r.position {
		block := r.blocks[r.next]
		r.next++
		raw := make([]byte, block.rawSize)
		if _, err := io.ReadFull(r.in, raw); err != nil {
			return 0, err
		}
		made, known := nintendoLz4(raw, block.level)
		if !known || len(made) != int(block.size) {
			return 0, fmt.Errorf("the LZ4 block at %#x cannot be made again", block.offset)
		}
		r.pending = made
		return r.Read(p)
	}
	if r.next < len(r.blocks) {
		p = p[:min(uint64(len(p)), r.blocks[r.next].offset-r.position)]
	}
	n, err := r.in.Read(p)
	r.position += uint64(n)
	return n, err
}
