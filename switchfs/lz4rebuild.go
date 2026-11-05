package switchfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"

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
//     (u32), its decompressed size (u32) and its level (s8), 17 bytes. The level is the one of
//     the NCA, but 64 for a block of level 0 made with the hash of 4 bytes (lz4Level0Hash4).
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
// byte for byte decompressed. Each block is checked on one of the workers, one per processor but
// one, and a goroutine writes what comes out in the order of the NCA. position is the place in
// the NCA of the next byte written; finish waits for the rest and tells the blocks kept.
type lz4Expander struct {
	blocks   []lz4Block
	next     int
	position uint64
	current  []byte
	pieces   chan *expandPiece
	jobs     chan *expandPiece
	written  chan struct{}
	workers  sync.WaitGroup
	once     sync.Once
	// set by the writing goroutine, read once it is done
	err  error
	kept []lz4Block
}

// expandPiece is a run of the NCA: bytes to write as they are, or a whole block to check, done
// once its channel is closed.
type expandPiece struct {
	data  []byte
	block lz4Block
	keep  bool
	ready chan struct{}
}

func newLz4Expander(out io.Writer, blocks []lz4Block, position uint64, workers int) *lz4Expander {
	if workers < 1 {
		workers = 1
	}
	e := &lz4Expander{blocks: blocks, position: position, pieces: make(chan *expandPiece, workers*lz4RebuildAhead),
		jobs: make(chan *expandPiece, workers*lz4RebuildAhead), written: make(chan struct{})}
	for i := 0; i < workers; i++ {
		e.workers.Add(1)
		go func() {
			defer e.workers.Done()
			for piece := range e.jobs {
				if raw, err := lz4Decompress(piece.data, int(piece.block.rawSize)); err == nil {
					for _, level := range nintendoLz4Levels(piece.block.level) {
						if made, known := nintendoLz4(raw, level); known && bytes.Equal(made, piece.data) {
							piece.data, piece.keep = raw, true
							piece.block.level = level
							break
						}
					}
				}
				close(piece.ready)
			}
		}()
	}
	go func() {
		defer close(e.written)
		for piece := range e.pieces {
			<-piece.ready
			if e.err != nil {
				continue
			}
			if _, err := out.Write(piece.data); err != nil {
				e.err = err
				continue
			}
			if piece.keep {
				e.kept = append(e.kept, piece.block)
			}
		}
	}()
	return e
}

// pass hands bytes as they are to the writing goroutine, in pieces of lz4RebuildGap at most.
func (e *lz4Expander) pass(p []byte) {
	for len(p) > 0 {
		n := min(len(p), lz4RebuildGap)
		piece := &expandPiece{data: append([]byte(nil), p[:n]...), ready: make(chan struct{})}
		close(piece.ready)
		e.pieces <- piece
		p = p[n:]
	}
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
				piece := &expandPiece{data: e.current, block: block, ready: make(chan struct{})}
				e.current = nil
				e.pieces <- piece
				e.jobs <- piece
			}
			continue
		}
		if e.next >= len(e.blocks) || e.blocks[e.next].offset >= e.position+uint64(len(p)) {
			e.pass(p)
			e.position += uint64(len(p))
			break
		}
		block := e.blocks[e.next]
		if block.offset > e.position {
			before := block.offset - e.position
			e.pass(p[:before])
			p = p[before:]
			e.position += before
		}
		e.current = make([]byte, 0, block.size)
		e.next++
	}
	return written, nil
}

// finish waits until everything handed is written, stops the goroutines and tells the blocks kept
// decompressed, in the order of the NCA. It may be called more than once.
func (e *lz4Expander) finish() ([]lz4Block, error) {
	e.once.Do(func() {
		close(e.jobs)
		close(e.pieces)
		<-e.written
		e.workers.Wait()
	})
	return e.kept, e.err
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
// table is read decompressed and made again. A goroutine reads the stream ahead and hands the
// blocks to workers, one per processor: LZ4HC makes about 55 MB a second on one core, so a game
// of 7 GB of blocks would take two minutes on one. What comes out keeps the order of the NCA.
type lz4Rebuilder struct {
	pieces  chan *lz4Piece
	stop    chan struct{}
	stopped sync.WaitGroup
	once    sync.Once
	// the piece being read, and what is left of it
	current *lz4Piece
	rest    []byte
	err     error
}

// lz4Piece is a run of the NCA: bytes as they are, or a block to make again, done once its
// channel is closed.
type lz4Piece struct {
	data []byte
	err  error
	done chan struct{}
}

// lz4RebuildAhead is how many pieces may wait to be read, per worker; the bytes between blocks
// are read lz4RebuildGap at most at a time.
const (
	lz4RebuildAhead = 4
	lz4RebuildGap   = 256 << 10
)

func newLz4Rebuilder(in io.Reader, blocks []lz4Block, position uint64, workers int) *lz4Rebuilder {
	if workers < 1 {
		workers = 1
	}
	r := &lz4Rebuilder{pieces: make(chan *lz4Piece, workers*lz4RebuildAhead), stop: make(chan struct{})}
	type job struct {
		piece *lz4Piece
		block lz4Block
		raw   []byte
	}
	jobs := make(chan job, workers*lz4RebuildAhead)
	for i := 0; i < workers; i++ {
		r.stopped.Add(1)
		go func() {
			defer r.stopped.Done()
			for j := range jobs {
				made, known := nintendoLz4(j.raw, j.block.level)
				if !known || len(made) != int(j.block.size) {
					j.piece.err = fmt.Errorf("the LZ4 block at %#x cannot be made again", j.block.offset)
				}
				j.piece.data = made
				close(j.piece.done)
			}
		}()
	}
	r.stopped.Add(1)
	go func() {
		defer r.stopped.Done()
		defer close(r.pieces)
		defer close(jobs)
		send := func(piece *lz4Piece) bool {
			select {
			case r.pieces <- piece:
				return true
			case <-r.stop:
				return false
			}
		}
		failed := func(err error) {
			piece := &lz4Piece{err: err, done: make(chan struct{})}
			close(piece.done)
			send(piece)
		}
		next := 0
		for {
			if next < len(blocks) && blocks[next].offset == position {
				block := blocks[next]
				next++
				raw := make([]byte, block.rawSize)
				if _, err := io.ReadFull(in, raw); err != nil {
					failed(err)
					return
				}
				piece := &lz4Piece{done: make(chan struct{})}
				if !send(piece) {
					return
				}
				select {
				case jobs <- job{piece, block, raw}:
				case <-r.stop:
					return
				}
				position += uint64(block.size)
				continue
			}
			// the bytes up to the next block, or to the end, as they are: in small pieces, as
			// many of them may wait to be read
			size := uint64(lz4RebuildGap)
			if next < len(blocks) {
				size = min(size, blocks[next].offset-position)
			}
			data := make([]byte, size)
			n, err := io.ReadFull(in, data)
			if n > 0 {
				piece := &lz4Piece{data: data[:n], done: make(chan struct{})}
				close(piece.done)
				if !send(piece) {
					return
				}
				position += uint64(n)
			}
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				if next < len(blocks) {
					failed(io.ErrUnexpectedEOF)
				}
				return
			}
			if err != nil {
				failed(err)
				return
			}
		}
	}()
	return r
}

func (r *lz4Rebuilder) Read(p []byte) (int, error) {
	for len(r.rest) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		piece, ok := <-r.pieces
		if !ok {
			r.err = io.EOF
			continue
		}
		<-piece.done
		if piece.err != nil {
			r.err = piece.err
			continue
		}
		r.rest = piece.data
	}
	n := copy(p, r.rest)
	r.rest = r.rest[n:]
	return n, nil
}

// Close stops the goroutines; the reader of the stream is not read any more once it returns.
func (r *lz4Rebuilder) Close() {
	r.once.Do(func() { close(r.stop) })
	// the workers end once the reader closes their jobs; pieces left are drained
	go func() {
		for range r.pieces {
		}
	}()
	r.stopped.Wait()
}

// lz4RebuildWorkers is how many blocks of one file are made again (or checked) at once: the
// threads of that file (CompressOptions.Workers, WithThreads), which the caller has shared
// already among the files worked on at once, at least one, and never more than every processor
// but one, which decompresses, decrypts and writes meanwhile. With no threads set (0), every
// processor but one. Each worker makes the blocks with LZ4HC (lz4nintendo.go), which keeps a
// processor busy: two files at once, each on every processor but one beside the threads of
// zstd, left the computer slow to answer.
func lz4RebuildWorkers(threads int) int {
	// the package has a max of its own, for bytes
	most := runtime.NumCPU() - 1
	if most < 1 {
		most = 1
	}
	if threads < 1 || threads > most {
		return most
	}
	return threads
}
