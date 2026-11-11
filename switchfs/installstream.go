package switchfs

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// An install stream gives an installer an NSZ or XCZ it can read: the NCZ files in the
// experimental formats of NXPress (NCZSECT2 and NCZSECT3), which DBI, Tinfoil and the others
// refuse, are given decompressed as the NCA they were made of, and everything else as it is.
// The package comes out as one PFS0 (an NSP, or an NSZ when it still holds NCZ files), built
// while it is read: nothing is written to the disk. An XCZ gives the files of its secure
// partition, as an installer takes them from it.
//
// The NCA of an NCZ written as one stream can only be made from its start, so the readers
// share their decoders (installDecoders): a reader that asks for the next piece, as an installer
// does with one request after another, goes on where the last one stopped.

// InstallStream is the layout of the package given to an installer.
type InstallStream struct {
	path   string
	header []byte
	parts  []installPart
	size   int64
	// the NCZ files given decompressed, and the ones given as they are
	Translated int
	Kept       int
}

// installPart is a file of the package given: copied from the source, or the NCA of an NCZ.
type installPart struct {
	name string
	// where it is in the package given, and its size there
	start, size int64
	// where it is in the source, and its size there
	source, sourceSize int64
	decompress         bool
}

// OpenInstallStream reads the layout of an NSP, NSZ, XCI or XCZ. A file without an NCZ in an
// experimental format has Translated 0: it is served as it is.
func OpenInstallStream(path string) (*InstallStream, error) {
	input, err := OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	pkg, err := openGamePackage(input)
	if err != nil {
		return nil, err
	}
	stream := &InstallStream{path: path}
	for _, file := range pkg.files {
		part := installPart{name: file.name, size: file.size, source: file.offset, sourceSize: file.size}
		if strings.HasSuffix(strings.ToLower(file.name), ".ncz") {
			magic := make([]byte, 8)
			if err := readAtFull(input, magic, file.offset+nczHeaderSize); err != nil {
				return nil, err
			}
			switch string(magic) {
			case nczSectionMagicV2, nczSectionMagicV3:
				size, err := nczDecompressedSize(input, file)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", file.name, err)
				}
				part.name = strings.TrimSuffix(file.name, filepath.Ext(file.name)) + ".nca"
				part.size, part.decompress = size, true
				stream.Translated++
			default:
				stream.Kept++
			}
		}
		stream.parts = append(stream.parts, part)
	}
	stream.layout()
	return stream, nil
}

// nczDecompressedSize is the size of the NCA of an NCZ: where its last section ends.
func nczDecompressedSize(input io.ReaderAt, file packedFile) (int64, error) {
	table := make([]byte, 0x10)
	if err := readAtFull(input, table, file.offset+nczHeaderSize); err != nil {
		return 0, err
	}
	count := binary.LittleEndian.Uint64(table[8:16])
	if count == 0 || count > nczMaxSections || count > uint64(file.size)/0x40 {
		return 0, errors.New("invalid NCZ section table")
	}
	last := make([]byte, 0x10)
	if err := readAtFull(input, last, file.offset+nczHeaderSize+0x10+int64(count-1)*0x40); err != nil {
		return 0, err
	}
	end := binary.LittleEndian.Uint64(last[0:8]) + binary.LittleEndian.Uint64(last[8:16])
	if end < nczHeaderSize || end > 1<<40 {
		return 0, errors.New("invalid NCZ section table")
	}
	return int64(end), nil
}

// layout writes the PFS0 header and places the files after it.
func (s *InstallStream) layout() {
	needed := int64(0x10 + PfsfileEntryTableSize*len(s.parts))
	for _, part := range s.parts {
		needed += int64(len(part.name)) + 1
	}
	headerLen := pfs0HeaderLen(needed)
	header := make([]byte, headerLen)
	copy(header, pfs0Magic)
	binary.LittleEndian.PutUint32(header[4:8], uint32(len(s.parts)))
	binary.LittleEndian.PutUint32(header[8:12], uint32(headerLen-int64(0x10+PfsfileEntryTableSize*len(s.parts))))
	table := header[0x10+PfsfileEntryTableSize*len(s.parts):]
	position, nameOffset := int64(0), 0
	for i := range s.parts {
		part := &s.parts[i]
		part.start = headerLen + position
		entry := header[0x10+PfsfileEntryTableSize*i:]
		binary.LittleEndian.PutUint64(entry[0:8], uint64(position))
		binary.LittleEndian.PutUint64(entry[8:16], uint64(part.size))
		binary.LittleEndian.PutUint32(entry[16:20], uint32(nameOffset))
		copy(table[nameOffset:], part.name)
		nameOffset += len(part.name) + 1
		position += part.size
	}
	s.header, s.size = header, headerLen+position
}

// Size is the size of the package given.
func (s *InstallStream) Size() int64 {
	return s.size
}

// Extension is the extension of the package given: ".nsz" while it holds NCZ files, else ".nsp".
func (s *InstallStream) Extension() string {
	if s.Kept > 0 {
		return ".nsz"
	}
	return ".nsp"
}

// Reader reads the package given. Close it when done: it hands its decoder to the next reader.
func (s *InstallStream) Reader() *InstallReader {
	return &InstallReader{stream: s}
}

// InstallReader reads an install stream; http.ServeContent answers range requests with it.
type InstallReader struct {
	stream   *InstallStream
	position int64
	file     ReadAtCloser
	decoder  *installDecoder
}

func (r *InstallReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += r.position
	case io.SeekEnd:
		offset += r.stream.size
	default:
		return 0, errors.New("invalid whence")
	}
	if offset < 0 {
		return 0, errors.New("negative position")
	}
	r.position = offset
	return offset, nil
}

func (r *InstallReader) Read(p []byte) (int, error) {
	if r.position >= r.stream.size {
		return 0, io.EOF
	}
	if int64(len(p)) > r.stream.size-r.position {
		p = p[:r.stream.size-r.position]
	}
	if r.position < int64(len(r.stream.header)) {
		n := copy(p, r.stream.header[r.position:])
		r.position += int64(n)
		return n, nil
	}
	part := r.stream.partAt(r.position)
	inside := r.position - part.start
	if int64(len(p)) > part.size-inside {
		p = p[:part.size-inside]
	}
	if !part.decompress {
		if r.file == nil {
			file, err := OpenFile(r.stream.path)
			if err != nil {
				return 0, err
			}
			r.file = file
		}
		n, err := r.file.ReadAt(p, part.source+inside)
		r.position += int64(n)
		if err == io.EOF && n == len(p) {
			err = nil
		}
		return n, err
	}
	if r.decoder == nil || r.decoder.part != part || r.decoder.position != inside {
		if r.decoder != nil {
			installDecoders.give(r.decoder)
		}
		decoder, err := installDecoders.take(r.stream.path, part, inside)
		if err != nil {
			r.decoder = nil
			return 0, err
		}
		r.decoder = decoder
	}
	n, err := r.decoder.read(p)
	r.position += int64(n)
	if err != nil {
		installDecoders.drop(r.decoder)
		r.decoder = nil
	}
	return n, err
}

// Close ends the reader; its decoder waits a while for the reader of the next piece.
func (r *InstallReader) Close() error {
	if r.decoder != nil {
		installDecoders.give(r.decoder)
		r.decoder = nil
	}
	if r.file != nil {
		r.file.Close()
		r.file = nil
	}
	return nil
}

// partAt is the file of the package at a position after the header.
func (s *InstallStream) partAt(position int64) *installPart {
	for i := range s.parts {
		if position < s.parts[i].start+s.parts[i].size {
			return &s.parts[i]
		}
	}
	return &s.parts[len(s.parts)-1]
}

// installDecoder makes the NCA of one NCZ, from its start, into a pipe.
type installDecoder struct {
	path     string
	part     *installPart
	position int64
	pipe     *io.PipeReader
	cancel   context.CancelFunc
	idle     time.Time
}

func startInstallDecoder(path string, part *installPart) (*installDecoder, error) {
	file, err := OpenFile(path)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	go func() {
		defer file.Close()
		counted := &countingWriter{w: writer}
		err := decompressNcz(ctx, counted, file, part.source, part.sourceSize, nil)
		if err == nil && counted.n != part.size {
			err = fmt.Errorf("%s gave %d bytes instead of %d", part.name, counted.n, part.size)
		}
		writer.CloseWithError(err)
	}()
	return &installDecoder{path: path, part: part, pipe: reader, cancel: cancel}, nil
}

// read gives the next bytes of the NCA; a short NCA is an error, not the end of it.
func (d *installDecoder) read(p []byte) (int, error) {
	n, err := io.ReadFull(d.pipe, p)
	d.position += int64(n)
	if err == io.ErrUnexpectedEOF || err == io.EOF && len(p) > 0 {
		err = fmt.Errorf("%s ended early: %w", d.part.name, io.ErrUnexpectedEOF)
	}
	return n, err
}

// skip moves the decoder forward, decompressing what is passed over.
func (d *installDecoder) skip(to int64) error {
	if to < d.position {
		return errors.New("a decoder cannot go back")
	}
	n, err := io.CopyN(io.Discard, d.pipe, to-d.position)
	d.position += n
	return err
}

func (d *installDecoder) close() {
	d.cancel()
	d.pipe.Close()
}

const (
	// decoders that wait for the reader of the next piece, at most, and for how long
	maxIdleDecoders = 4
	decoderIdleTime = 2 * time.Minute
	// a decoder this little behind a piece asked for goes on to it rather than a new one
	// starting again from the start of the NCA
	maxDecoderSkip = 256 << 20
)

// decoderPool keeps the decoders no reader uses, for the reader of the next piece.
type decoderPool struct {
	mutex sync.Mutex
	idle  []*installDecoder
}

var installDecoders = &decoderPool{}

// take gives a decoder of the NCA of a part at a position: an idle one at or a little before
// it, or a new one.
func (p *decoderPool) take(path string, part *installPart, position int64) (*installDecoder, error) {
	p.mutex.Lock()
	p.expire(time.Now())
	best := -1
	for i, decoder := range p.idle {
		if decoder.path != path || decoder.part.source != part.source || decoder.part.sourceSize != part.sourceSize ||
			decoder.position > position || position-decoder.position > maxDecoderSkip {
			continue
		}
		if best < 0 || decoder.position > p.idle[best].position {
			best = i
		}
	}
	var decoder *installDecoder
	if best >= 0 {
		decoder = p.idle[best]
		p.idle = append(p.idle[:best], p.idle[best+1:]...)
	}
	p.mutex.Unlock()
	if decoder == nil {
		var err error
		if decoder, err = startInstallDecoder(path, part); err != nil {
			return nil, err
		}
	}
	decoder.part = part
	if err := decoder.skip(position); err != nil {
		decoder.close()
		return nil, err
	}
	return decoder, nil
}

// give keeps a decoder for the next reader; the oldest goes when too many wait.
func (p *decoderPool) give(decoder *installDecoder) {
	if decoder.position >= decoder.part.size {
		decoder.close()
		return
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	decoder.idle = time.Now()
	p.idle = append(p.idle, decoder)
	for len(p.idle) > maxIdleDecoders {
		p.idle[0].close()
		p.idle = p.idle[1:]
	}
	p.expire(time.Now())
}

// drop ends a decoder that failed.
func (p *decoderPool) drop(decoder *installDecoder) {
	decoder.close()
}

// expire ends the decoders nobody took for decoderIdleTime; called with the mutex held.
func (p *decoderPool) expire(now time.Time) {
	kept := p.idle[:0]
	for _, decoder := range p.idle {
		if now.Sub(decoder.idle) > decoderIdleTime {
			decoder.close()
			continue
		}
		kept = append(kept, decoder)
	}
	p.idle = kept
}

// IdleInstallDecoders is how many decoders wait for a reader, for the tests.
func IdleInstallDecoders() int {
	installDecoders.mutex.Lock()
	defer installDecoders.mutex.Unlock()
	return len(installDecoders.idle)
}

// CloseIdleInstallDecoders ends the decoders that wait for a reader: when the app stops, and in
// the tests, so their files are closed.
func CloseIdleInstallDecoders() {
	installDecoders.mutex.Lock()
	defer installDecoders.mutex.Unlock()
	for _, decoder := range installDecoders.idle {
		decoder.close()
	}
	installDecoders.idle = nil
}
