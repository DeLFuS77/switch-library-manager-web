package switchfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"
)

// An NSP becomes a game card dump the other way round: its files go into the secure partition
// of an XCI, and the files of an NSZ, compressed as they are, into the one of an XCZ. A game
// card made this way has a header written here, as the tools that make XCI files from NSP files
// do: it is not signed by Nintendo, which installers and emulators do not look at, and the
// update and normal partitions are left empty, as an XCZ has them. The tickets and the
// certificate of the NSP go with its NCA files, as in a game card packed with its updates
// (see PackIntoCard): its NCA files are still encrypted with their title keys.

const (
	// where the root HFS0 of a game card starts, after its header and certificate areas
	cardRootOffset = 0xF000
	cardPage       = 0x200
)

// cardKeepsFile tells whether a file of an NSP goes into a game card: its content (NCA or NCZ),
// its tickets and certificate; the XML and images some tools add next to them do not.
func cardKeepsFile(name string) bool {
	lower := strings.ToLower(name)
	for _, extension := range []string{".nca", ".ncz", ".tik", ".cert"} {
		if strings.HasSuffix(lower, extension) {
			return true
		}
	}
	return false
}

// cardRomSize is the byte of the header that tells the size of the card that holds size bytes.
func cardRomSize(size int64) byte {
	const gb = int64(1) << 30
	switch {
	case size <= 1*gb:
		return 0xFA
	case size <= 2*gb:
		return 0xF8
	case size <= 4*gb:
		return 0xF0
	case size <= 8*gb:
		return 0xE0
	case size <= 16*gb:
		return 0xE1
	default:
		return 0xE2
	}
}

// fillCardHeader writes the header of a game card written by xczWriter with the prefix of
// cardRootOffset bytes: where its root partition and secure area are, how big it is, and the
// hash of the header of its root partition.
func fillCardHeader(file *os.File, secureStart int64) error {
	end, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	// the file is written only: its root partition is read through another handle
	reader, err := os.Open(file.Name())
	if err != nil {
		return err
	}
	defer reader.Close()
	head := make([]byte, 0x10)
	if _, err := reader.ReadAt(head, cardRootOffset); err != nil {
		return err
	}
	count := int64(binary.LittleEndian.Uint32(head[4:8]))
	table := int64(binary.LittleEndian.Uint32(head[8:12]))
	root := make([]byte, 0x10+hfs0EntrySize*count+table)
	if _, err := reader.ReadAt(root, cardRootOffset); err != nil {
		return err
	}
	header := make([]byte, cardPage)
	copy(header[0x100:], "HEAD")
	binary.LittleEndian.PutUint32(header[0x104:], uint32(secureStart/cardPage))
	binary.LittleEndian.PutUint32(header[0x108:], 0xFFFFFFFF) // no backup area
	header[0x10D] = cardRomSize(end)
	binary.LittleEndian.PutUint32(header[0x118:], uint32(end/cardPage-1))
	binary.LittleEndian.PutUint64(header[0x130:], cardRootOffset)
	binary.LittleEndian.PutUint64(header[0x138:], uint64(len(root)))
	hash := sha256.Sum256(root)
	copy(header[0x140:], hash[:])
	binary.LittleEndian.PutUint32(header[0x180:], 1) // secure mode
	binary.LittleEndian.PutUint32(header[0x184:], 2) // title key of T1
	binary.LittleEndian.PutUint32(header[0x18C:], uint32(secureStart/cardPage))
	_, err = file.WriteAt(header, 0)
	return err
}

// convertNspToXci writes the files of an NSP or NSZ into the secure partition of a new game
// card, an XCI or XCZ, at target; the target is removed if the conversion fails.
func convertNspToXci(ctx context.Context, source string, target string, progress func(done int64, total int64)) ([]*writtenFile, error) {
	input, err := os.Open(source)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	pkg, err := openGamePackage(input)
	if err != nil {
		return nil, err
	}
	if pkg.xci {
		return nil, errors.New("not an NSP or NSZ file")
	}
	files := []packedFile{}
	names := []string{}
	var total, done int64
	for _, file := range pkg.files {
		if !cardKeepsFile(file.name) {
			continue
		}
		files = append(files, file)
		names = append(names, file.name)
		total += file.size
	}
	if len(files) == 0 {
		return nil, errors.New("the package holds no NCA files")
	}
	report := func(n int64) {
		done += n
		if progress != nil {
			progress(done, total)
		}
	}

	output, err := CreateNew(target)
	if err != nil {
		return nil, err
	}
	card := &gamePackage{xci: true, rootOffset: cardRootOffset, partitions: []string{"update", "normal", "secure"}}
	writer := &xczWriter{file: output, input: bytes.NewReader(make([]byte, cardRootOffset)), pkg: card, names: names}
	written := make([]*writtenFile, len(files))
	defer endHashes(written)
	err = writer.begin()
	for i, file := range files {
		if err != nil {
			break
		}
		if err = writer.startFile(); err != nil {
			break
		}
		written[i] = newWrittenFile(names[i])
		err = copyRange(ctx, io.MultiWriter(output, written[i]), input, file.offset, file.size, report)
		if err == nil {
			err = writer.endFile()
		}
	}
	if err == nil {
		err = writer.finish()
	}
	if err == nil {
		err = fillCardHeader(output, writer.start)
	}
	if closeErr := output.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(target)
		return nil, err
	}
	return written, nil
}
