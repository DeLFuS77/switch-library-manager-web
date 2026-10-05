package switchfs

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// header space reserved for every HFS0 written, like nsz does
	hfs0ReservedHeader = 0x8000
	hfs0EntrySize      = 0x40
)

// packedFile is a file inside an NSP, or inside the secure partition of an XCI, at an
// absolute offset of the package.
type packedFile struct {
	name   string
	offset int64
	size   int64
}

// gamePackage is an NSP or an XCI, with the files that can be compressed.
type gamePackage struct {
	xci   bool
	files []packedFile
	// NSP: length of the PFS0 header
	headerLen int64
	// XCI: offset of the root HFS0 and the names of its partitions, in order
	rootOffset int64
	partitions []string
}

// openGamePackage reads the layout of an NSP (or NSZ) or XCI (or XCZ).
func openGamePackage(input io.ReaderAt) (*gamePackage, error) {
	magic := make([]byte, 4)
	if err := readAtFull(input, magic, 0); err != nil {
		return nil, err
	}
	if string(magic) == pfs0Magic {
		pfs0, err := readPfs0(input, 0)
		if err != nil {
			return nil, err
		}
		pkg := &gamePackage{headerLen: int64(pfs0.HeaderLen)}
		for _, file := range pfs0.Files {
			pkg.files = append(pkg.files, packedFile{name: file.Name, offset: int64(file.StartOffset), size: int64(file.Size)})
		}
		return pkg, nil
	}

	header := make([]byte, 0x200)
	if err := readAtFull(input, header, 0); err != nil {
		return nil, err
	}
	if string(header[0x100:0x104]) != "HEAD" {
		return nil, errors.New("not an NSP or XCI file")
	}
	rootOffset := binary.LittleEndian.Uint64(header[0x130:0x138])
	if rootOffset < 0x200 || rootOffset > 1<<40 {
		return nil, errors.New("invalid XCI root partition offset")
	}
	root, err := readPfs0(input, int64(rootOffset))
	if err != nil {
		return nil, err
	}
	pkg := &gamePackage{xci: true, rootOffset: int64(rootOffset)}
	for _, partition := range root.Files {
		pkg.partitions = append(pkg.partitions, partition.Name)
	}
	secure, secureOffset, err := readSecurePartition(input, root, rootOffset)
	if err != nil {
		return nil, err
	}
	if secure == nil {
		return nil, errors.New("XCI secure partition not found")
	}
	for _, file := range secure.Files {
		pkg.files = append(pkg.files, packedFile{name: file.Name, offset: secureOffset + int64(file.StartOffset), size: int64(file.Size)})
	}
	return pkg, nil
}

// packageWriter writes the files of a compressed package in order.
type packageWriter interface {
	begin() error
	startFile()
	endFile() error
	finish() error
}

// newPackageWriter returns a writer for an NSZ or an XCZ with the given file names.
func newPackageWriter(output *os.File, input io.ReaderAt, pkg *gamePackage, names []string) packageWriter {
	if pkg.xci {
		return &xczWriter{file: output, input: input, pkg: pkg, names: names}
	}
	return &pfs0Writer{file: output, names: names, headerLen: pkg.headerLen}
}

// xczWriter writes an XCZ like nsz does: the original gamecard header and the area up to
// the root partition, then a root HFS0 whose secure partition holds the files. The other
// partitions (update, normal, logo) are left empty: installers only use the secure one.
type xczWriter struct {
	file   *os.File
	input  io.ReaderAt
	pkg    *gamePackage
	names  []string
	secure *hfs0Writer
	root   *hfs0Writer
	start  int64
}

func (w *xczWriter) begin() error {
	prefix := make([]byte, w.pkg.rootOffset)
	if err := readAtFull(w.input, prefix, 0); err != nil {
		return err
	}
	if _, err := w.file.Write(prefix); err != nil {
		return err
	}
	w.root = &hfs0Writer{file: w.file, base: w.pkg.rootOffset}
	if err := w.root.begin(); err != nil {
		return err
	}
	// partitions before the secure one stay empty
	for _, name := range w.pkg.partitions {
		if name == "secure" {
			break
		}
		if err := w.emptyPartition(name); err != nil {
			return err
		}
	}
	position, err := w.file.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	w.start = position
	w.secure = &hfs0Writer{file: w.file, base: position, names: w.names}
	return w.secure.begin()
}

func (w *xczWriter) emptyPartition(name string) error {
	position, err := w.file.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	partition := &hfs0Writer{file: w.file, base: position, compact: true}
	if err := partition.begin(); err != nil {
		return err
	}
	if err := partition.finish(); err != nil {
		return err
	}
	return w.root.addFile(name, position, w.alignedEnd())
}

func (w *xczWriter) startFile() {
	w.secure.startFile()
}

func (w *xczWriter) endFile() error {
	return w.secure.endFile()
}

func (w *xczWriter) finish() error {
	if err := w.secure.finish(); err != nil {
		return err
	}
	if err := w.root.addFile("secure", w.start, w.alignedEnd()); err != nil {
		return err
	}
	for _, name := range w.pkg.partitions[indexOf(w.pkg.partitions, "secure")+1:] {
		if err := w.emptyPartition(name); err != nil {
			return err
		}
	}
	if err := w.root.finish(); err != nil {
		return err
	}
	return w.file.Sync()
}

// alignedEnd pads the file to a multiple of 0x200 and returns the end.
func (w *xczWriter) alignedEnd() int64 {
	position, _ := w.file.Seek(0, io.SeekEnd)
	if padding := (0x200 - position%0x200) % 0x200; padding > 0 {
		w.file.Write(make([]byte, padding))
		position += padding
	}
	return position
}

func indexOf(values []string, value string) int {
	for i, v := range values {
		if v == value {
			return i
		}
	}
	return len(values) - 1
}

// hfs0Writer writes an HFS0 at base: the header space is reserved first and the
// header written when the files are known. Hashes are left empty, like nsz does.
type hfs0Writer struct {
	file    *os.File
	base    int64
	names   []string
	starts  []int64
	sizes   []int64
	current int64
	// an empty partition: only the header, without reserved space
	compact bool
}

func (w *hfs0Writer) begin() error {
	if w.compact {
		_, err := w.file.Write(make([]byte, 0x200))
		return err
	}
	_, err := w.file.Write(make([]byte, hfs0ReservedHeader))
	return err
}

func (w *hfs0Writer) startFile() {
	position, _ := w.file.Seek(0, io.SeekCurrent)
	w.current = position
}

func (w *hfs0Writer) endFile() error {
	position, err := w.file.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	w.starts = append(w.starts, w.current)
	w.sizes = append(w.sizes, position-w.current)
	return nil
}

// addFile records a file written elsewhere, from start to end.
func (w *hfs0Writer) addFile(name string, start int64, end int64) error {
	w.names = append(w.names, name)
	w.starts = append(w.starts, start)
	w.sizes = append(w.sizes, end-start)
	w.file.Seek(0, io.SeekEnd)
	return nil
}

func (w *hfs0Writer) finish() error {
	var table []byte
	nameOffsets := make([]uint32, len(w.names))
	for i, name := range w.names {
		nameOffsets[i] = uint32(len(table))
		table = append(append(table, name...), 0)
	}
	headerLen := int64(0x10 + hfs0EntrySize*len(w.names) + len(table))
	if headerLen > hfs0ReservedHeader || (w.compact && headerLen > 0x200) {
		return errors.New("too many files for the HFS0 header")
	}
	header := make([]byte, headerLen)
	copy(header, hfs0Magic)
	binary.LittleEndian.PutUint32(header[4:8], uint32(len(w.names)))
	binary.LittleEndian.PutUint32(header[8:12], uint32(len(table)))
	for i := range w.names {
		entry := header[0x10+hfs0EntrySize*i:]
		binary.LittleEndian.PutUint64(entry[0:8], uint64(w.starts[i]-w.base-headerLen))
		binary.LittleEndian.PutUint64(entry[8:16], uint64(w.sizes[i]))
		binary.LittleEndian.PutUint32(entry[16:20], nameOffsets[i])
	}
	copy(header[0x10+hfs0EntrySize*len(w.names):], table)
	if _, err := w.file.WriteAt(header, w.base); err != nil {
		return err
	}
	_, err := w.file.Seek(0, io.SeekEnd)
	return err
}

// CompressedPath is the name of the compressed file of an NSP (.nsz) or XCI (.xcz).
func CompressedPath(path string) string {
	extension := ".nsz"
	if strings.EqualFold(filepath.Ext(path), ".xci") {
		extension = ".xcz"
	}
	return strings.TrimSuffix(path, filepath.Ext(path)) + extension
}

// DecompressedSize returns the size of the NSP an NSZ decompresses to, from the section
// tables of its NCZ files, so free space can be checked first.
func DecompressedSize(path string) (int64, error) {
	input, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer input.Close()
	pkg, err := openGamePackage(input)
	if err != nil {
		return 0, err
	}
	size := pkg.headerLen
	for _, file := range pkg.files {
		if !strings.HasSuffix(strings.ToLower(file.name), ".ncz") {
			size += file.size
			continue
		}
		table := make([]byte, 0x10)
		if err := readAtFull(input, table, file.offset+nczHeaderSize); err != nil {
			return 0, err
		}
		count := binary.LittleEndian.Uint64(table[8:16])
		if string(table[:8]) != nczSectionMagic || count == 0 || count > 64 {
			return 0, errors.New("invalid NCZ section table")
		}
		last := make([]byte, 0x10)
		if err := readAtFull(input, last, file.offset+nczHeaderSize+0x10+int64(count-1)*0x40); err != nil {
			return 0, err
		}
		size += int64(binary.LittleEndian.Uint64(last[0:8]) + binary.LittleEndian.Uint64(last[8:16]))
	}
	return size, nil
}
