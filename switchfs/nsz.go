package switchfs

// NSZ compression, compatible with the format of nsz (https://github.com/nicoboss/nsz),
// read by installers such as DBI. An NSZ is an NSP whose large NCA files are
// replaced by NCZ files:
//
//	0x0000  the first 0x4000 bytes of the NCA, unchanged (encrypted header)
//	0x4000  "NCZSECTN", number of sections, then per section: offset, size, crypto type,
//	        padding (8 bytes each, little endian), AES key (16) and counter (16)
//	        a zstd stream of the decrypted NCA from offset 0x4000 to its end, or an
//	        "NCZBLOCK" header followed by independently compressed blocks
//
// Decompressing encrypts the sections again with their key and counter, which gives the
// original NCA back byte for byte; its SHA-256 matches the content ID in the NCA name.
// Implemented here from the format, without code of nsz.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs/_crypto"
	"github.com/klauspost/compress/zstd"
)

const (
	nczHeaderSize   = 0x4000
	nczSectionMagic = "NCZSECTN"
	nczBlockMagic   = "NCZBLOCK"
	nczChunkSize    = 4 << 20
	// more sections than an update ever has means a damaged table
	nczMaxSections = 1 << 20
	// decoders on the console need a bounded window
	nczWindowSize = 8 << 20
	// the goroutines of the decoder of a zstd stream: it decodes the blocks of the stream
	// and executes them on goroutines of their own, 1.7 times as fast as on one
	nczStreamDecoders = 4
	// the blocks of a block compressed NCZ: 1 MB, as nsz writes them by default
	nczBlockExponent = 20
)

// Compression levels offered to the user.
const (
	LevelFast     = "fast"
	LevelBalanced = "balanced"
	LevelMax      = "max"
)

// MaxLevel is the highest numbered level ("1" to "19", as nsz numbers them): above it the
// zstd window grows past the 8 MiB (nczWindowSize) the installers of the console decompress with.
const MaxLevel = 19

// levelNumber is the zstd level of a numbered level.
func levelNumber(level string) (int, bool) {
	n, err := strconv.Atoi(level)
	if err != nil || n < 1 || n > MaxLevel {
		return 0, false
	}
	return n, true
}

// CompressOptions tune a compression.
type CompressOptions struct {
	// LevelFast, LevelBalanced (default) or LevelMax, or a numbered level from "1" to "19"
	Level string
	// threads used by the encoder; 0 means 1
	Workers int
	// blocks compressed on their own instead of one stream, which lets a reader start
	// anywhere: what nsz writes for XCZ by default. A game card is always compressed so.
	Blocks bool
	// called with the bytes of the source processed so far and the total
	Progress func(done int64, total int64)
}

// CompressResult describes a finished compression.
type CompressResult struct {
	InputSize  int64
	OutputSize int64
	// NCA files compressed, and stored as they are (small, metadata or update patches)
	Compressed int
	Stored     int
	// the SHA-256 of every file of the original by name, taken while it was read to compress
	// it: what its compressed copy must give back (see CheckHashes and VerifyCompressed)
	Hashes map[string][32]byte
}

// ErrNotCompressible is returned when no NCA of an NSP can be compressed.
var ErrNotCompressible = errors.New("nothing in this file can be compressed")

// ErrNoTitleKey is returned for an NSP without a ticket whose title key title.keys does not
// have either: its contents cannot be decrypted.
var ErrNoTitleKey = errors.New("the NSP has no ticket and title.keys does not list its title key")

// ErrSparseStorage is ErrNotCompressible for a game in sparse storage: a layout of some recent
// games whose sections are mapped through a table onto less data than they cover. No
// compressor reads it yet (nsz neither); the game stays as it is and installs normally.
var ErrSparseStorage = fmt.Errorf("%w: it uses sparse storage, which cannot be compressed yet", ErrNotCompressible)

// reasonSparse is the reason of planNcz for an NCA in sparse storage.
const reasonSparse = "sparse storage"

// ErrAlreadyCompressed is ErrNotCompressible for a package that holds NCZ files already: an
// NSZ or XCZ named as an NSP or XCI.
var ErrAlreadyCompressed = fmt.Errorf("%w: it is compressed already (it holds NCZ files), an NSZ or XCZ under another name", ErrNotCompressible)

// ncaContentTypes name the content types of NCA files, to tell why one is not compressed.
var ncaContentTypes = map[byte]string{
	NcaContentType_Program: "program", NcaContentType_Meta: "meta", NcaContentType_Control: "control",
	NcaContentType_Manual: "manual", NcaContentType_Data: "data", NcaContentType_PublicData: "public data",
}

type nczSection struct {
	offset     uint64
	size       uint64
	cryptoType uint64
	key        []byte
	counter    []byte
}

type nczPlan struct {
	sections []nczSection
}

// planGame plans the compression of each NCA of a game, and gives the names its files take
// in the compressed package. When none can be compressed, the error tells why: compressed
// already, sparse storage, an encryption or NCA files with no game data.
func planGame(input io.ReaderAt, pkg *gamePackage, titleKeys map[string][]byte) ([]*nczPlan, []string, error) {
	plans := make([]*nczPlan, len(pkg.files))
	// why the NCA files worth compressing were stored as they are, to tell when none is left
	reasons := []string{}
	names := make([]string, len(pkg.files))
	ncas, nczs, compressed := 0, 0, 0
	for i, file := range pkg.files {
		names[i] = file.name
		if strings.HasSuffix(strings.ToLower(file.name), ".ncz") {
			nczs++
		}
		if !isCompressibleNcaName(file.name) || file.size <= nczHeaderSize {
			continue
		}
		ncas++
		plan, reason, err := planNcz(input, file.offset, uint64(file.size), titleKeys)
		if err != nil {
			return plans, names, fmt.Errorf("%s: %w", file.name, err)
		}
		if plan == nil && reason != "" {
			reasons = append(reasons, file.name+": "+reason)
		}
		if plan != nil {
			plans[i] = plan
			names[i] = strings.TrimSuffix(file.name, ".nca") + ".ncz"
			compressed++
		}
	}
	if compressed > 0 {
		return plans, names, nil
	}
	if nczs > 0 {
		return plans, names, ErrAlreadyCompressed
	}
	for _, reason := range reasons {
		if strings.HasSuffix(reason, ": "+reasonSparse) {
			return plans, names, ErrSparseStorage
		}
	}
	if len(reasons) > 0 {
		return plans, names, fmt.Errorf("%w: %s", ErrNotCompressible, strings.Join(reasons, "; "))
	}
	if ncas == 0 {
		return plans, names, fmt.Errorf("%w: it holds no NCA file but metadata", ErrNotCompressible)
	}
	return plans, names, ErrNotCompressible
}

// NotCompressible tells why nothing of a game can be compressed, as a compression would,
// without writing anything: nil when something can.
func NotCompressible(source string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	pkg, err := openGamePackage(input)
	if err != nil {
		return err
	}
	titleKeys, err := readTickets(input, pkg.files)
	if err != nil {
		return err
	}
	_, _, err = planGame(input, pkg, titleKeys)
	return err
}

// CompressGame writes the NSZ of an NSP, or the XCZ of an XCI, to target. It does not
// verify the result, see VerifyCompressed.
func CompressGame(ctx context.Context, source string, target string, options CompressOptions) (CompressResult, error) {
	result := CompressResult{Hashes: map[string][32]byte{}}
	input, err := os.Open(source)
	if err != nil {
		return result, err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return result, err
	}
	result.InputSize = info.Size()

	pkg, err := openGamePackage(input)
	if err != nil {
		return result, err
	}
	titleKeys, err := readTickets(input, pkg.files)
	if err != nil {
		return result, err
	}

	plans, names, err := planGame(input, pkg, titleKeys)
	for _, plan := range plans {
		if plan != nil {
			result.Compressed++
		}
	}
	if err != nil {
		return result, err
	}

	output, err := CreateNew(target)
	if err != nil {
		return result, err
	}
	writer := newPackageWriter(output, input, pkg, names)
	err = writer.begin()

	var done int64
	progress := func(n int64) {
		done += n
		if options.Progress != nil {
			options.Progress(done, result.InputSize)
		}
	}
	for i, file := range pkg.files {
		if err != nil {
			break
		}
		if err = ctx.Err(); err != nil {
			break
		}
		writer.startFile()
		// the original is hashed as it is read, instead of in a pass of its own
		reader := newHashingReaderAt(input, file.offset, file.size)
		if plans[i] != nil && (options.Blocks || pkg.xci) {
			err = writeNczBlocks(ctx, output, reader, file.offset, file.size, plans[i], options, progress)
		} else if plans[i] != nil {
			err = writeNcz(ctx, output, reader, file.offset, file.size, plans[i], options, progress)
		} else {
			result.Stored++
			err = copyRange(ctx, output, reader, file.offset, file.size, progress)
		}
		sum, complete := reader.finish()
		if err == nil {
			err = writer.endFile()
		}
		if err == nil {
			if !complete {
				// not read whole in order: hashed on its own
				whole := sha256.New()
				if err = copyRange(ctx, whole, input, file.offset, file.size, nil); err == nil {
					copy(sum[:], whole.Sum(nil))
				}
			}
			result.Hashes[file.name] = sum
		}
	}
	if err == nil {
		err = writer.finish()
	}
	closeErr := output.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(target)
		return result, err
	}
	if info, statErr := os.Stat(target); statErr == nil {
		result.OutputSize = info.Size()
	}
	// the files are counted as they are read, the header of the package with them at the end:
	// the progress reaches its total
	if options.Progress != nil && done < result.InputSize {
		options.Progress(result.InputSize, result.InputSize)
	}
	return result, nil
}

// CompressNsp is CompressGame, kept for the NSP only callers.
func CompressNsp(ctx context.Context, source string, target string, options CompressOptions) (CompressResult, error) {
	return CompressGame(ctx, source, target, options)
}

// isCompressibleNcaName tells the NCA files worth compressing; metadata NCA stay as
// they are, like nsz does.
func isCompressibleNcaName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".nca") && !strings.HasSuffix(lower, ".cnmt.nca")
}

// planNcz decides how an NCA is compressed, or returns nil to store it as it is, with the
// reason when the NCA was worth compressing. Like nsz, a section the app cannot decrypt, the
// bytes between sections and after the last one are kept in the stream as they are: they are
// rebuilt exactly, they only do not shrink, and the rest of the NCA is still compressed.
func planNcz(input io.ReaderAt, offset int64, size uint64, titleKeys map[string][]byte) (*nczPlan, string, error) {
	keys, _ := settings.SwitchKeys()
	if keys == nil || keys.GetKey("header_key") == "" {
		return nil, "", errors.New("prod.keys is needed to compress")
	}
	encHeader := make([]byte, 0xC00)
	if err := readAtFull(input, encHeader, offset); err != nil {
		return nil, "", err
	}
	header, err := DecryptNcaHeader(keys.GetKey("header_key"), encHeader)
	if err != nil {
		return nil, "", err
	}
	// program and public data hold the game; the rest is small and stays as it is, told only
	// when no NCA of the package is compressed
	if header.contentType != NcaContentType_Program && header.contentType != NcaContentType_PublicData {
		name, ok := ncaContentTypes[header.contentType]
		if !ok {
			name = fmt.Sprintf("content type %d", header.contentType)
		}
		return nil, "it holds no game data (" + name + ")", nil
	}

	sectionKey, err := ncaSectionKey(keys, header, titleKeys)
	if err != nil {
		return nil, "", err
	}

	// as stored: the bytes are kept as they are, encrypted or not
	asStored := func(start, length uint64) nczSection {
		return nczSection{offset: start, size: length, cryptoType: 1, key: make([]byte, 16), counter: make([]byte, 16)}
	}
	sections := []nczSection{}
	readable := false
	for index := 0; index < 4; index++ {
		entry := getFsEntry(header, index)
		if entry.Size == 0 {
			continue
		}
		fsh, err := getFsHeader(header, index)
		if err != nil {
			return nil, "", err
		}
		// the size of the table of a sparse section: its range is not where its data is
		if !isZero(fsh.fsHeaderBytes[0x148+0x8 : 0x148+0x10]) {
			return nil, reasonSparse, nil
		}
		counter := make([]byte, 16)
		for i := 0; i < 8; i++ {
			counter[i] = fsh.fsHeaderBytes[0x147-i]
		}
		whole := nczSection{
			offset:     uint64(entry.StartOffset),
			size:       uint64(entry.Size),
			cryptoType: uint64(fsh.encType),
			key:        sectionKey,
			counter:    counter,
		}
		switch fsh.encType {
		case 1, 3:
			sections = append(sections, whole)
			readable = true
		case 5:
			// AES-CTR without the hash of a layer: encrypted like 3
			whole.cryptoType = 3
			sections = append(sections, whole)
			readable = true
		case 4, 6:
			// update patches: every subsection has its own counter. Read with the wrong
			// counter the data would still be rebuilt exactly, but not compress at all.
			whole.cryptoType = 4
			if subsections, err := bktrSections(input, offset, whole, fsh); err == nil {
				sections = append(sections, subsections...)
				readable = true
			} else if fsh.encType == 4 {
				sections = append(sections, whole)
				readable = true
			} else {
				sections = append(sections, asStored(whole.offset, whole.size))
			}
		default:
			sections = append(sections, asStored(whole.offset, whole.size))
		}
	}
	if len(sections) == 0 {
		return nil, "no sections", nil
	}
	if !readable {
		return nil, "its sections use an encryption the app does not read", nil
	}

	// the stream starts right after the copied header and covers the rest of the NCA without
	// gaps: the bytes between the sections and after the last one are kept as they are, and
	// a first section that starts within the header goes on where the header ends, as nsz does
	sort.Slice(sections, func(i, j int) bool { return sections[i].offset < sections[j].offset })
	planned := []nczSection{}
	next := uint64(nczHeaderSize)
	for _, section := range sections {
		end := section.offset + section.size
		if end < section.offset || section.offset < next && len(planned) > 0 {
			return nil, "its sections overlap", nil
		}
		if end <= next {
			continue
		}
		if section.offset < next {
			section.size -= next - section.offset
			section.offset = next
		}
		if section.offset > next {
			planned = append(planned, asStored(next, section.offset-next))
		}
		planned = append(planned, section)
		next = end
	}
	if next > size {
		return nil, "its sections go beyond its end", nil
	}
	if next < size {
		planned = append(planned, asStored(next, size-next))
	}
	return &nczPlan{sections: planned}, "", nil
}

// bktrSections splits an AesCtrEx section of an update patch by its subsection table,
// which gives the counter of every part of the section. The table itself, at the end of
// the section, is encrypted with the counter of the section.
func bktrSections(input io.ReaderAt, ncaOffset int64, section nczSection, fsh *fsHeader) ([]nczSection, error) {
	info := fsh.fsHeaderBytes[0x120:0x140]
	tableOffset := binary.LittleEndian.Uint64(info[0:8])
	tableSize := binary.LittleEndian.Uint64(info[8:16])
	if string(info[16:20]) != "BKTR" || tableSize < 0x8000 || tableOffset+tableSize > section.size {
		return nil, errors.New("no subsection table")
	}

	block, err := aes.NewCipher(section.key)
	if err != nil {
		return nil, err
	}
	read := func(position uint64, size int) ([]byte, error) {
		data := make([]byte, size)
		if err := readAtFull(input, data, ncaOffset+int64(position)); err != nil {
			return nil, err
		}
		cipher.NewCTR(block, nczCounter(section.counter, position)).XORKeyStream(data, data)
		return data, nil
	}

	tableStart := section.offset + tableOffset
	header, err := read(tableStart, 0x4000)
	if err != nil {
		return nil, err
	}
	buckets := binary.LittleEndian.Uint32(header[4:8])
	if buckets == 0 || uint64(buckets+1)*0x4000 > tableSize {
		return nil, errors.New("invalid subsection table")
	}

	sections := []nczSection{}
	next := section.offset
	for b := uint32(0); b < buckets; b++ {
		bucket, err := read(tableStart+uint64(b+1)*0x4000, 0x4000)
		if err != nil {
			return nil, err
		}
		count := binary.LittleEndian.Uint32(bucket[4:8])
		end := binary.LittleEndian.Uint64(bucket[8:16])
		if count == 0 || count > (0x4000-0x10)/0x10 {
			return nil, errors.New("invalid subsection bucket")
		}
		for i := uint32(0); i < count; i++ {
			entry := bucket[0x10+0x10*i:]
			start := section.offset + binary.LittleEndian.Uint64(entry[0:8])
			stop := section.offset + end
			if i+1 < count {
				stop = section.offset + binary.LittleEndian.Uint64(bucket[0x10+0x10*(i+1):])
			}
			if start != next || stop <= start {
				return nil, errors.New("subsections do not follow each other")
			}
			counter := append([]byte(nil), section.counter...)
			binary.BigEndian.PutUint32(counter[4:8], binary.LittleEndian.Uint32(entry[12:16]))
			sections = append(sections, nczSection{offset: start, size: stop - start, cryptoType: 4, key: section.key, counter: counter})
			next = stop
		}
	}
	// the rest of the section, with the tables, uses the counter of the section
	if end := section.offset + section.size; next < end {
		sections = append(sections, nczSection{offset: next, size: end - next, cryptoType: 4, key: section.key, counter: section.counter})
	} else if next > end {
		return nil, errors.New("subsections exceed the section")
	}
	return sections, nil
}

// keySet is the keys an operation reads, taken once from settings.SwitchKeys: they are loaded
// again before every synchronization, and may change or be gone meanwhile.
type keySet interface {
	GetKey(name string) string
	TitleKey(rightsId string) (string, bool)
}

// ncaSectionKey returns the AES key of the sections of an NCA: the title key of its
// ticket (or title.keys) for titles with a rights ID, else the key in the key area.
func ncaSectionKey(keys keySet, header *ncaHeader, titleKeys map[string][]byte) ([]byte, error) {
	if keys == nil {
		return nil, &MissingKeyError{KeyName: "header_key"}
	}
	revision := fmt.Sprintf("%02x", header.getKeyRevision())

	if header.HasRightsId() {
		rightsId := hex.EncodeToString(header.rightsId)
		encrypted, ok := titleKeys[rightsId]
		if !ok {
			if fromFile, found := keys.TitleKey(rightsId); found {
				encrypted, _ = hex.DecodeString(fromFile)
			}
		}
		if len(encrypted) != 16 {
			return nil, fmt.Errorf("rights ID %s: %w", rightsId, ErrNoTitleKey)
		}
		kekName := "titlekek_" + revision
		kek, _ := hex.DecodeString(keys.GetKey(kekName))
		if len(kek) != 16 {
			return nil, &MissingKeyError{KeyName: kekName}
		}
		return _crypto.DecryptAes128Ecb(encrypted, kek), nil
	}

	if header.cryptoType != 0 {
		return nil, errors.New("unsupported key area")
	}
	keyName := "key_area_key_application_" + revision
	areaKey, _ := hex.DecodeString(keys.GetKey(keyName))
	if len(areaKey) != 16 {
		return nil, &MissingKeyError{KeyName: keyName}
	}
	if len(header.encryptedKeys) < 0x30 {
		return nil, errors.New("truncated NCA key area")
	}
	return _crypto.DecryptAes128Ecb(header.encryptedKeys[0x20:0x30], areaKey), nil
}

// readTickets returns the encrypted title keys of the common tickets of an NSP, by
// rights ID. Personalized tickets need the console's keys and are left out.
func readTickets(input io.ReaderAt, files []packedFile) (map[string][]byte, error) {
	result := map[string][]byte{}
	for _, file := range files {
		if !strings.HasSuffix(strings.ToLower(file.name), ".tik") || file.size > 0x1000 {
			continue
		}
		if file.size < 0 || file.offset < 0 {
			return nil, fmt.Errorf("%s has an invalid size", file.name)
		}
		ticket := make([]byte, file.size)
		if err := readAtFull(input, ticket, file.offset); err != nil {
			return nil, err
		}
		if len(ticket) < 4 {
			continue
		}
		// the signature type is little endian in Switch tickets
		signatureSize := ticketSignatureSize(binary.LittleEndian.Uint32(ticket[0:4]))
		if signatureSize == 0 {
			signatureSize = ticketSignatureSize(binary.BigEndian.Uint32(ticket[0:4]))
		}
		if signatureSize == 0 {
			continue
		}
		data := 4 + signatureSize
		if len(ticket) < data+0x170 {
			continue
		}
		if ticket[data+0x141] != 0 {
			continue
		}
		rightsId := hex.EncodeToString(ticket[data+0x160 : data+0x170])
		result[rightsId] = append([]byte(nil), ticket[data+0x40:data+0x50]...)
	}
	return result, nil
}

// ticketSignatureSize is the size of the signature and its padding, or 0 if unknown.
func ticketSignatureSize(signatureType uint32) int {
	switch signatureType {
	case 0x010000, 0x010003:
		return 0x200 + 0x3C
	case 0x010001, 0x010004:
		return 0x100 + 0x3C
	case 0x010002, 0x010005:
		return 0x3C + 0x40
	}
	return 0
}

// writeNcz writes the NCZ of an NCA.
func writeNcz(ctx context.Context, output io.Writer, input io.ReaderAt, offset int64, size int64, plan *nczPlan, options CompressOptions, progress func(int64)) error {
	if err := writeNczHeader(output, input, offset, plan, progress); err != nil {
		return err
	}
	return writeNczStream(ctx, output, input, offset, plan, options, progress)
}

// writeNczHeader writes the header of the NCA as it is and the table of its sections.
func writeNczHeader(output io.Writer, input io.ReaderAt, offset int64, plan *nczPlan, progress func(int64)) error {
	header := make([]byte, nczHeaderSize)
	if err := readAtFull(input, header, offset); err != nil {
		return err
	}
	if _, err := output.Write(header); err != nil {
		return err
	}
	progress(nczHeaderSize)

	table := bytes.NewBufferString(nczSectionMagic)
	binary.Write(table, binary.LittleEndian, uint64(len(plan.sections)))
	for _, section := range plan.sections {
		binary.Write(table, binary.LittleEndian, section.offset)
		binary.Write(table, binary.LittleEndian, section.size)
		binary.Write(table, binary.LittleEndian, section.cryptoType)
		binary.Write(table, binary.LittleEndian, uint64(0))
		table.Write(section.key)
		table.Write(section.counter)
	}
	_, err := output.Write(table.Bytes())
	return err
}

// writeNczStream writes the decrypted sections as one zstd stream.
func writeNczStream(ctx context.Context, output io.Writer, input io.ReaderAt, offset int64, plan *nczPlan, options CompressOptions, progress func(int64)) error {
	encoder, err := newStreamEncoder(output, options.Level, options.Workers)
	if err != nil {
		return err
	}

	buffer := make([]byte, nczChunkSize)
	for _, section := range plan.sections {
		block, err := aes.NewCipher(section.key)
		if err != nil {
			encoder.Close()
			return err
		}
		for position := section.offset; position < section.offset+section.size; {
			if err := ctx.Err(); err != nil {
				encoder.Close()
				return err
			}
			length := uint64(len(buffer))
			if remaining := section.offset + section.size - position; remaining < length {
				length = remaining
			}
			chunk := buffer[:length]
			if err := readAtFull(input, chunk, offset+int64(position)); err != nil {
				encoder.Close()
				return err
			}
			if section.cryptoType == 3 || section.cryptoType == 4 {
				cipher.NewCTR(block, nczCounter(section.counter, position)).XORKeyStream(chunk, chunk)
			}
			if _, err := encoder.Write(chunk); err != nil {
				encoder.Close()
				return err
			}
			progress(int64(length))
			position += length
		}
	}
	return encoder.Close()
}

// writeNczBlocks writes the NCZ of an NCA with its decrypted sections in blocks compressed
// on their own: "NCZBLOCK", version 2, type 1, a byte unused, the exponent of the block
// size, the number of blocks, the size of the data (little endian), the size of each block
// as written, then the blocks. A block that does not shrink is written as it is, as nsz
// does. The sizes are only known at the end, so the table is written again over its place.
func writeNczBlocks(ctx context.Context, output *os.File, input io.ReaderAt, offset int64, size int64, plan *nczPlan, options CompressOptions, progress func(int64)) error {
	if err := writeNczHeader(output, input, offset, plan, progress); err != nil {
		return err
	}
	plain := nczPlainReader{input: input, offset: offset, sections: plan.sections}
	next := uint64(nczHeaderSize)
	fill := func(data []byte) error {
		err := plain.read(data, next)
		next += uint64(len(data))
		return err
	}
	return writeNczBlockData(ctx, output, uint64(size)-nczHeaderSize, fill, options, progress)
}

// writeNczBlockData writes the blocks of an NCZ after its section table: the block header,
// the table of their sizes and the blocks, of dataSize bytes taken in order from fill.
func writeNczBlockData(ctx context.Context, output *os.File, dataSize uint64, fill func([]byte) error, options CompressOptions, progress func(int64)) error {
	blockSize := uint64(1) << nczBlockExponent
	count := (dataSize + blockSize - 1) / blockSize
	header := make([]byte, 24)
	copy(header, nczBlockMagic)
	header[8], header[9], header[11] = 2, 1, nczBlockExponent
	binary.LittleEndian.PutUint32(header[12:], uint32(count))
	binary.LittleEndian.PutUint64(header[16:], dataSize)
	if _, err := output.Write(header); err != nil {
		return err
	}
	tableAt, err := output.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	sizes := make([]uint32, count)
	if err := binary.Write(output, binary.LittleEndian, sizes); err != nil {
		return err
	}

	workers := options.Workers
	if workers < 1 {
		workers = 1
	}
	encoder, err := newBlockEncoder(options.Level, workers)
	if err != nil {
		return err
	}
	defer encoder.Close()

	// a batch of blocks is read, compressed by every worker at once, then written in order
	type job struct {
		data, out []byte
		err       error
	}
	batch := make([]job, workers)
	for i := range batch {
		batch[i].data = make([]byte, blockSize)
	}
	for first := uint64(0); first < count; first += uint64(workers) {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(uint64(workers), count-first)
		for i := uint64(0); i < n; i++ {
			start := (first + i) * blockSize
			length := min(blockSize, dataSize-start)
			batch[i].data = batch[i].data[:length]
			if err := fill(batch[i].data); err != nil {
				return err
			}
		}
		var wait sync.WaitGroup
		for i := uint64(0); i < n; i++ {
			wait.Add(1)
			go func(worker int, j *job) {
				defer wait.Done()
				j.out, j.err = encoder.encode(worker, j.out[:0], j.data)
			}(int(i), &batch[i])
		}
		wait.Wait()
		for i := uint64(0); i < n; i++ {
			if batch[i].err != nil {
				return batch[i].err
			}
			block := batch[i].out
			if len(block) >= len(batch[i].data) {
				block = batch[i].data
			}
			if _, err := output.Write(block); err != nil {
				return err
			}
			sizes[first+i] = uint32(len(block))
			progress(int64(len(batch[i].data)))
		}
	}

	end, err := output.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err := output.Seek(tableAt, io.SeekStart); err != nil {
		return err
	}
	if err := binary.Write(output, binary.LittleEndian, sizes); err != nil {
		return err
	}
	_, err = output.Seek(end, io.SeekStart)
	return err
}

// nczPlainReader reads the NCA at offset in input as an NCZ stores it: decrypted by the
// sections of its plan, which cover it from its header to its end.
type nczPlainReader struct {
	input    io.ReaderAt
	offset   int64
	sections []nczSection
}

// read fills data with the NCA from position on.
func (r nczPlainReader) read(data []byte, position uint64) error {
	for _, section := range r.sections {
		end := section.offset + section.size
		if len(data) == 0 {
			return nil
		}
		if position >= end || position < section.offset {
			continue
		}
		piece := data[:min(uint64(len(data)), end-position)]
		if err := readAtFull(r.input, piece, r.offset+int64(position)); err != nil {
			return err
		}
		if section.cryptoType == 3 || section.cryptoType == 4 {
			block, err := aes.NewCipher(section.key)
			if err != nil {
				return err
			}
			stream := cipher.NewCTR(block, nczCounter(section.counter, position&^15))
			if skip := position & 15; skip > 0 {
				stream.XORKeyStream(make([]byte, skip), make([]byte, skip))
			}
			stream.XORKeyStream(piece, piece)
		}
		data = data[len(piece):]
		position += uint64(len(piece))
	}
	if len(data) > 0 {
		return errors.New("the sections do not cover the NCA")
	}
	return nil
}

func encoderLevel(level string) zstd.EncoderLevel {
	// a numbered level, in the four levels of the encoder in Go
	if n, ok := levelNumber(level); ok {
		switch {
		case n <= 3:
			return zstd.SpeedFastest
		case n <= 9:
			return zstd.SpeedDefault
		case n <= 15:
			return zstd.SpeedBetterCompression
		}
		return zstd.SpeedBestCompression
	}
	switch level {
	case LevelFast:
		return zstd.SpeedDefault
	case LevelMax:
		return zstd.SpeedBestCompression
	default:
		return zstd.SpeedBetterCompression
	}
}

// nczCounter is the AES-CTR counter at an offset of a section: the upper half from the
// section, the lower half the offset in 16 byte blocks.
func nczCounter(sectionCounter []byte, offset uint64) []byte {
	counter := make([]byte, 16)
	copy(counter[:8], sectionCounter[:8])
	binary.BigEndian.PutUint64(counter[8:], offset>>4)
	return counter
}

// decompressNcz writes the original NCA of an NCZ stored at offset in input.
func decompressNcz(ctx context.Context, output io.Writer, input io.ReaderAt, offset int64, size int64, progress func(int64)) error {
	return decompressNczPlain(ctx, output, input, offset, size, progress, nil)
}

// decompressNczPlain is decompressNcz that also gives plain the decrypted bytes of the NCA
// after its header, with their offset in the NCA, to check its hashes.
func decompressNczPlain(ctx context.Context, output io.Writer, input io.ReaderAt, offset int64, size int64, progress func(int64), plain func(int64, []byte)) error {
	reader := io.NewSectionReader(input, offset, size)
	header := make([]byte, nczHeaderSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return err
	}
	if _, err := output.Write(header); err != nil {
		return err
	}

	magic := make([]byte, 8)
	if _, err := io.ReadFull(reader, magic); err != nil || string(magic) != nczSectionMagic {
		return errors.New("not an NCZ file")
	}
	// updates split their sections by the subsection table: thousands of entries of 0x40
	// bytes are possible, but never more than the NCZ holds
	var count uint64
	if err := binary.Read(reader, binary.LittleEndian, &count); err != nil || count == 0 || count > uint64(size)/0x40 || count > nczMaxSections {
		return errors.New("invalid NCZ section table")
	}
	// read one by one: a damaged count must not reserve memory before the entries exist
	sections := []nczSection{}
	for i := uint64(0); i < count; i++ {
		values := make([]uint64, 4)
		if err := binary.Read(reader, binary.LittleEndian, values); err != nil {
			return err
		}
		section := nczSection{offset: values[0], size: values[1], cryptoType: values[2], key: make([]byte, 16), counter: make([]byte, 16)}
		if _, err := io.ReadFull(reader, section.key); err != nil {
			return err
		}
		if _, err := io.ReadFull(reader, section.counter); err != nil {
			return err
		}
		sections = append(sections, section)
	}

	// like nsz: the bytes between the copied header and a first section that starts later
	// are stored without encryption, and a first section that starts earlier continues
	// where the copied header ends
	if sections[0].offset > nczHeaderSize {
		gap := nczSection{offset: nczHeaderSize, size: sections[0].offset - nczHeaderSize, cryptoType: 1, key: make([]byte, 16), counter: make([]byte, 16)}
		sections = append([]nczSection{gap}, sections...)
	} else if sections[0].offset < nczHeaderSize {
		skip := nczHeaderSize - sections[0].offset
		if skip >= sections[0].size {
			return errors.New("invalid NCZ section table")
		}
		sections[0].offset += skip
		sections[0].size -= skip
	}
	// the sections follow each other from the end of the copied header, as nsz writes them
	next := uint64(nczHeaderSize)
	for _, section := range sections {
		if section.offset != next || section.offset+section.size < section.offset {
			return errors.New("invalid NCZ section table")
		}
		next = section.offset + section.size
	}

	position, _ := reader.Seek(0, io.SeekCurrent)
	if _, err := io.ReadFull(reader, magic); err != nil {
		return err
	}
	var stream io.Reader
	if string(magic) == nczBlockMagic {
		blocks, err := newNczBlockReader(reader)
		if err != nil {
			return err
		}
		defer blocks.Close()
		stream = blocks
	} else {
		reader.Seek(position, io.SeekStart)
		// zstd asks for a few bytes at a time: read the file in big pieces, one system call
		// each, or the reads take more time than the decompression. With low memory on (its
		// default) zstd keeps the window plus 1 MiB and moves the whole window down every
		// MiB: on the 128 MiB window of other tools that was 4 times slower. Off, it keeps
		// twice the window (16 MiB for the 8 MiB of this app) and moves it once per window.
		decoder, err := zstd.NewReader(bufio.NewReaderSize(reader, nczChunkSize), zstd.WithDecoderConcurrency(nczStreamDecoders), zstd.WithDecoderMaxWindow(1<<30), zstd.WithDecoderLowmem(false))
		if err != nil {
			return err
		}
		defer decoder.Close()
		stream = decoder
	}
	// the data is decompressed ahead while what came before is decrypted and written
	ahead := newReadAhead(stream, nczChunkSize, 3)
	defer ahead.Close()
	stream = ahead

	buffer := make([]byte, nczChunkSize)
	for _, section := range sections {
		block, err := aes.NewCipher(section.key)
		if err != nil {
			return err
		}
		for position := section.offset; position < section.offset+section.size; {
			if err := ctx.Err(); err != nil {
				return err
			}
			length := uint64(len(buffer))
			if remaining := section.offset + section.size - position; remaining < length {
				length = remaining
			}
			chunk := buffer[:length]
			if _, err := io.ReadFull(stream, chunk); err != nil {
				return fmt.Errorf("NCZ data ends early: %w", err)
			}
			if plain != nil {
				plain(int64(position), chunk)
			}
			if section.cryptoType == 3 || section.cryptoType == 4 {
				cipher.NewCTR(block, nczCounter(section.counter, position)).XORKeyStream(chunk, chunk)
			}
			if _, err := output.Write(chunk); err != nil {
				return err
			}
			if progress != nil {
				progress(int64(length))
			}
			position += length
		}
	}
	return nil
}

// nczBlockDecoders is how many blocks of an NCZ are decompressed at once, each on a
// goroutine of its own, as long as they hold nczBlockBatch at most.
const (
	nczBlockDecoders = 8
	nczBlockBatch    = 64 << 20
)

// nczBlockReader reads the block compressed variant of NCZ, decompressing several blocks at
// once: they do not depend on each other.
type nczBlockReader struct {
	source    io.Reader
	blockSize int
	sizes     []uint32
	current   []byte
	// the blocks decompressed and not given yet, in order
	ready   [][]byte
	decoder *zstd.Decoder
	// decompressed bytes not given yet, to know the size of the last block
	remaining uint64
}

func newNczBlockReader(source io.Reader) (*nczBlockReader, error) {
	header := make([]byte, 8)
	if _, err := io.ReadFull(source, header); err != nil {
		return nil, err
	}
	exponent := header[3]
	count := binary.LittleEndian.Uint32(header[4:8])
	if exponent < 14 || exponent > 30 || count > 1<<24 {
		return nil, errors.New("invalid NCZ block header")
	}
	var decompressedSize uint64
	if err := binary.Read(source, binary.LittleEndian, &decompressedSize); err != nil {
		return nil, err
	}
	blockSize := uint64(1) << exponent
	if (decompressedSize+blockSize-1)/blockSize != uint64(count) {
		return nil, errors.New("invalid NCZ block header")
	}
	sizes := make([]uint32, count)
	if err := binary.Read(source, binary.LittleEndian, sizes); err != nil {
		return nil, err
	}
	// a block is never bigger compressed than as it is: nsz stores it as it is then
	for _, size := range sizes {
		if uint64(size) > blockSize {
			return nil, errors.New("invalid NCZ block size")
		}
	}
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(nczBlockDecoders), zstd.WithDecoderMaxMemory(blockSize))
	if err != nil {
		return nil, err
	}
	return &nczBlockReader{source: source, blockSize: 1 << exponent, sizes: sizes, decoder: decoder, remaining: decompressedSize}, nil
}

// Close frees the decoder.
func (b *nczBlockReader) Close() {
	b.decoder.Close()
}

func (b *nczBlockReader) Read(p []byte) (int, error) {
	for len(b.current) == 0 {
		if len(b.ready) == 0 {
			if len(b.sizes) == 0 {
				return 0, io.EOF
			}
			if err := b.decodeBatch(); err != nil {
				return 0, err
			}
		}
		b.current, b.ready = b.ready[0], b.ready[1:]
	}
	n := copy(p, b.current)
	b.current = b.current[n:]
	return n, nil
}

// decodeBatch reads the next blocks, as many as are decompressed at once, and decompresses
// them together into ready.
func (b *nczBlockReader) decodeBatch() error {
	// (max is a function of this package for bytes)
	count := min(len(b.sizes), nczBlockDecoders, nczBlockBatch/b.blockSize)
	if count < 1 {
		count = 1
	}
	blocks := make([][]byte, count)
	errs := make([]error, count)
	var wait sync.WaitGroup
	for i := range blocks {
		compressed := make([]byte, b.sizes[i])
		if _, err := io.ReadFull(b.source, compressed); err != nil {
			wait.Wait()
			return err
		}
		// a block is stored as it is when compressing it gave nothing; the last block is
		// shorter than the others (nsz issue 210)
		size := uint64(b.blockSize)
		if b.remaining < size {
			size = b.remaining
		}
		b.remaining -= size
		if uint64(len(compressed)) >= size {
			blocks[i] = compressed[:size]
			continue
		}
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			decoded, err := b.decoder.DecodeAll(compressed, make([]byte, 0, size))
			if err == nil && uint64(len(decoded)) != size {
				err = errors.New("an NCZ block does not have its size")
			}
			blocks[i], errs[i] = decoded, err
		}(i)
	}
	wait.Wait()
	b.sizes = b.sizes[count:]
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	b.ready = blocks
	return nil
}

// FileCheck tells what the check of a game file found besides damage.
type FileCheck struct {
	// NCA files rewritten by a tool (their header no longer matches their content ID)
	// whose data still matches all its hashes: they install and play like the originals
	Modified []string
	// some sections of those files cannot be checked on their own (the romfs of an
	// update, or no key to decrypt them), or an NCA has no content ID in its name to be
	// checked against
	Partial bool
}

// VerifyCompressed checks every file of an NSZ or XCZ: compressed ones are decompressed,
// and the SHA-256 of each NCA must match the content ID in its name. expected holds the
// hashes of the files of the original by name, when known: every one of them must then be
// there with the same content.
func VerifyCompressed(ctx context.Context, path string, expected map[string][32]byte, progress func(done int64, total int64)) error {
	_, _, err := checkPackage(ctx, path, expected, progress)
	return err
}

// VerifyNsz is VerifyCompressed.
func VerifyNsz(ctx context.Context, path string, expected map[string][32]byte, progress func(done int64, total int64)) error {
	return VerifyCompressed(ctx, path, expected, progress)
}

// VerifyFile checks a game file of the library (NSP, NSZ, XCI or XCZ). An NCA that does
// not match its content ID is only damaged when its data does not match its hashes.
func VerifyFile(ctx context.Context, path string, progress func(done int64, total int64)) (FileCheck, error) {
	_, check, err := checkPackage(ctx, path, nil, progress)
	return check, err
}

// contentId returns the content ID in the name of an NCA, or "" if it has none.
func contentId(name string) string {
	id := strings.ToLower(name)
	if i := strings.Index(id, "."); i >= 0 {
		id = id[:i]
	}
	if len(id) != 32 {
		return ""
	}
	if _, err := hex.DecodeString(id); err != nil {
		return ""
	}
	return id
}

// CheckHashes is what checkPackage tells of an original from the hashes of its files, taken
// while it was compressed: an NCA whose hash is not its content ID is checked by the hash
// trees of its data, rewritten by a tool or damaged. Only those are read again.
func CheckHashes(ctx context.Context, path string, hashes map[string][32]byte) (FileCheck, error) {
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
	var titleKeys map[string][]byte
	for _, file := range pkg.files {
		sum, ok := hashes[file.name]
		if !ok {
			return check, fmt.Errorf("%s was not hashed", file.name)
		}
		if !strings.HasSuffix(strings.ToLower(file.name), ".nca") {
			continue
		}
		id := contentId(file.name)
		if id == "" {
			// nothing tells what its hash must be
			check.Partial = true
			continue
		}
		if hex.EncodeToString(sum[:16]) == id {
			continue
		}
		if titleKeys == nil {
			if titleKeys, err = readTickets(input, pkg.files); err != nil {
				return check, err
			}
		}
		result, err := checkNcaIntegrity(ctx, input, file.offset, file.size, titleKeys)
		if err := check.addMismatch(file.name, file.name, result, err); err != nil {
			return check, err
		}
	}
	return check, nil
}

// HashNcas returns the SHA-256 of every file of an NSP or XCI by name, to compare with
// its compressed copy. It tells when an NCA is damaged; one that was only modified by a
// tool (see FileCheck) is not an error.
func HashNcas(ctx context.Context, path string, progress func(done int64, total int64)) (map[string][32]byte, error) {
	hashes, _, err := checkPackage(ctx, path, nil, progress)
	return hashes, err
}

// HashAndCheck is HashNcas that also gives what the check of the file found: reading every
// file of it is the whole check, so it is not done again.
func HashAndCheck(ctx context.Context, path string, progress func(done int64, total int64)) (map[string][32]byte, FileCheck, error) {
	return checkPackage(ctx, path, nil, progress)
}

// checkPackage hashes every file of a package (NCZ files decompressed) and checks them:
// against expected when given, else every NCA against its content ID and, when that
// fails, against the hash trees of its data.
func checkPackage(ctx context.Context, path string, expected map[string][32]byte, progress func(done int64, total int64)) (map[string][32]byte, FileCheck, error) {
	check := FileCheck{}
	input, err := os.Open(path)
	if err != nil {
		return nil, check, err
	}
	defer input.Close()
	pkg, err := openGamePackage(input)
	if err != nil {
		return nil, check, err
	}
	var total, done int64
	for _, file := range pkg.files {
		total += file.size
	}
	report := func(n int64) {
		done += n
		if progress != nil {
			progress(done, total)
		}
	}
	var titleKeys map[string][]byte

	hashes := map[string][32]byte{}
	for _, file := range pkg.files {
		original := file.name
		compressed := strings.HasSuffix(strings.ToLower(file.name), ".ncz")
		if compressed {
			original = file.name[:len(file.name)-4] + ".nca"
		}
		// hashed beside the reading and the decompression, not after them
		hash := newBackgroundHash()
		if compressed {
			// the progress follows the compressed bytes read, so a big NCZ moves on while it
			// is decompressed instead of all at once at its end
			counter := &countingReaderAt{reader: input, start: file.offset, end: file.offset + file.size, report: report}
			err = decompressNcz(ctx, hash, counter, file.offset, file.size, nil)
			if rest := file.size - counter.counted; err == nil && rest > 0 {
				report(rest)
			}
		} else {
			err = copyRange(ctx, hash, input, file.offset, file.size, report)
		}
		sum := hash.Sum()
		if err != nil {
			return nil, check, fmt.Errorf("%s: %w", file.name, err)
		}
		hashes[original] = sum

		if expected != nil {
			// a copy: every file must be the one of the original
			want, ok := expected[original]
			if !ok {
				return nil, check, fmt.Errorf("%s is not in the original", file.name)
			}
			if want != sum {
				return nil, check, fmt.Errorf("%s: the content differs from the original", file.name)
			}
			continue
		}
		if !strings.HasSuffix(strings.ToLower(original), ".nca") {
			continue
		}
		id := contentId(file.name)
		if id == "" {
			// nothing tells what its hash must be
			check.Partial = true
			continue
		}
		if hex.EncodeToString(sum[:16]) == id {
			continue
		}
		// the NCA does not match its content ID: modified by a tool, or damaged
		var result IntegrityResult
		if compressed {
			result, err = checkNczIntegrity(ctx, input, file.offset, file.size)
		} else {
			if titleKeys == nil {
				if titleKeys, err = readTickets(input, pkg.files); err != nil {
					return nil, check, err
				}
			}
			result, err = checkNcaIntegrity(ctx, input, file.offset, file.size, titleKeys)
		}
		if err := check.addMismatch(file.name, original, result, err); err != nil {
			return nil, check, err
		}
	}
	for name := range expected {
		if _, ok := hashes[name]; !ok {
			return nil, check, fmt.Errorf("%s of the original is missing", name)
		}
	}
	return hashes, check, nil
}

// countingReaderAt reports the bytes read from a part of a file, each byte once, as its
// progress.
type countingReaderAt struct {
	reader     io.ReaderAt
	start, end int64
	// the furthest position read so far, and the bytes reported
	furthest int64
	counted  int64
	report   func(int64)
}

func (c *countingReaderAt) ReadAt(p []byte, offset int64) (int, error) {
	n, err := c.reader.ReadAt(p, offset)
	if reached := offset + int64(n); reached > c.furthest && offset >= c.start {
		if reached > c.end {
			reached = c.end
		}
		if c.furthest < c.start {
			c.furthest = c.start
		}
		if step := reached - c.furthest; step > 0 {
			c.furthest = reached
			c.counted += step
			c.report(step)
		}
	}
	return n, err
}

// checkNczIntegrity checks the hash trees of the NCA of an NCZ: its stream holds the data
// already decrypted, so no key is needed.
func checkNczIntegrity(ctx context.Context, input io.ReaderAt, offset int64, size int64) (IntegrityResult, error) {
	encryptedHeader := make([]byte, 0xC00)
	if err := readAtFull(input, encryptedHeader, offset); err != nil {
		return IntegrityResult{}, err
	}
	keys, _ := settings.SwitchKeys()
	check, _, err := newNcaIntegrity(keys, encryptedHeader)
	if err != nil {
		return IntegrityResult{}, err
	}
	if err := decompressNczPlain(ctx, io.Discard, input, offset, size, nil, check.feed); err != nil {
		return IntegrityResult{}, err
	}
	return check.finish()
}

// DecompressNsz writes the NSP of an NSZ, or the XCI of an XCZ, to target.
func DecompressNsz(ctx context.Context, source string, target string, progress func(done int64, total int64)) error {
	_, err := decompressNsz(ctx, source, target, progress)
	return err
}

// DecompressAndCheck is DecompressNsz that checks the NSP (or XCI) while writing it, instead
// of reading it again: the SHA-256 of every file is taken as it is written, and an NCA whose
// hash is not its content ID is checked by the hash trees of its data (rewritten by a tool,
// or damaged), as VerifyFile does. The file is then opened again: its list of files, their
// sizes and the start and the end of each one, read back from the disk, must be what was
// written. On an error the file is removed.
func DecompressAndCheck(ctx context.Context, source string, target string, progress func(done int64, total int64)) (FileCheck, error) {
	written, err := decompressNsz(ctx, source, target, progress)
	if err != nil {
		return FileCheck{}, err
	}
	check, err := checkWritten(ctx, target, written)
	if err != nil {
		os.Remove(target)
	}
	return check, err
}

// decompressNsz writes the files of an NSZ or an XCZ again with their NCZ decompressed: an
// NSZ gives an NSP, an XCZ an XCI laid out as the XCZ is (the gamecard header and the area up
// to the root partition kept, the secure partition holding the files, the other partitions
// empty), as nsz does.
func decompressNsz(ctx context.Context, source string, target string, progress func(done int64, total int64)) ([]*writtenFile, error) {
	input, err := os.Open(source)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	pkg, err := openGamePackage(input)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		// a package cut short is told as such, as when its files are read
		return nil, err
	}
	if err != nil {
		return nil, errors.New("only NSZ and XCZ files can be decompressed")
	}
	names := make([]string, len(pkg.files))
	var total, done int64
	for i, file := range pkg.files {
		names[i] = file.name
		if strings.HasSuffix(strings.ToLower(file.name), ".ncz") {
			names[i] = strings.TrimSuffix(file.name, ".ncz") + ".nca"
		}
		total += file.size
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
	written := make([]*writtenFile, len(pkg.files))
	defer endHashes(written)
	writer := newPackageWriter(output, input, pkg, names)
	err = writer.begin()
	for i, file := range pkg.files {
		if err != nil {
			break
		}
		writer.startFile()
		// what is written of the file is hashed meanwhile, with its start and its end kept
		written[i] = newWrittenFile(names[i])
		out := io.MultiWriter(output, written[i])
		if names[i] != file.name {
			// the progress follows the compressed bytes read, like the check: a big NCZ moves
			// on while it is decompressed instead of all at once at its end
			counter := &countingReaderAt{reader: input, start: file.offset, end: file.offset + file.size, report: report}
			err = decompressNcz(ctx, out, counter, file.offset, file.size, nil)
			if rest := file.size - counter.counted; err == nil && rest > 0 {
				report(rest)
			}
		} else {
			err = copyRange(ctx, out, input, file.offset, file.size, report)
		}
		if err == nil {
			err = writer.endFile()
		}
	}
	if err == nil {
		err = writer.finish()
	}
	closeErr := output.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(target)
		return nil, err
	}
	return written, nil
}

// pfs0Writer writes a PFS0 container: the header space is reserved first and written
// when the sizes of the files are known.
type pfs0Writer struct {
	file      *os.File
	names     []string
	headerLen int64
	starts    []int64
	sizes     []int64
	current   int64
}

func (w *pfs0Writer) begin() error {
	needed := int64(0x10 + PfsfileEntryTableSize*len(w.names))
	for _, name := range w.names {
		needed += int64(len(name)) + 1
	}
	if needed > w.headerLen {
		// a new header, or names longer than in the original: aligned to 0x10, as the tools that
		// make NSP files from installed titles write it, so a part split from a pack is the NSP
		// it was made of byte for byte
		w.headerLen = pfs0HeaderLen(needed)
	}
	_, err := w.file.Write(make([]byte, w.headerLen))
	return err
}

// pfs0HeaderLen is the length of a PFS0 header that needs the bytes given.
func pfs0HeaderLen(needed int64) int64 {
	return (needed + 0xF) &^ 0xF
}

func (w *pfs0Writer) startFile() {
	position, _ := w.file.Seek(0, io.SeekCurrent)
	w.current = position
}

func (w *pfs0Writer) endFile() error {
	position, err := w.file.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	w.starts = append(w.starts, w.current-w.headerLen)
	w.sizes = append(w.sizes, position-w.current)
	return nil
}

func (w *pfs0Writer) finish() error {
	stringsLen := w.headerLen - int64(0x10+PfsfileEntryTableSize*len(w.names))
	header := make([]byte, w.headerLen)
	copy(header, pfs0Magic)
	binary.LittleEndian.PutUint32(header[4:8], uint32(len(w.names)))
	binary.LittleEndian.PutUint32(header[8:12], uint32(stringsLen))
	nameOffset := 0
	table := header[0x10+PfsfileEntryTableSize*len(w.names):]
	for i, name := range w.names {
		entry := header[0x10+PfsfileEntryTableSize*i:]
		binary.LittleEndian.PutUint64(entry[0:8], uint64(w.starts[i]))
		binary.LittleEndian.PutUint64(entry[8:16], uint64(w.sizes[i]))
		binary.LittleEndian.PutUint32(entry[16:20], uint32(nameOffset))
		copy(table[nameOffset:], name)
		nameOffset += len(name) + 1
	}
	if _, err := w.file.WriteAt(header, 0); err != nil {
		return err
	}
	return w.file.Sync()
}

func copyRange(ctx context.Context, output io.Writer, input io.ReaderAt, offset int64, size int64, progress func(int64)) error {
	// a negative size would copy nothing, and its hash be taken for the file's
	if offset < 0 || size < 0 {
		return errors.New("invalid file range")
	}
	buffer := make([]byte, nczChunkSize)
	for done := int64(0); done < size; {
		if err := ctx.Err(); err != nil {
			return err
		}
		length := int64(len(buffer))
		if size-done < length {
			length = size - done
		}
		if err := readAtFull(input, buffer[:length], offset+done); err != nil {
			return err
		}
		if _, err := output.Write(buffer[:length]); err != nil {
			return err
		}
		if progress != nil {
			progress(length)
		}
		done += length
	}
	return nil
}

func mustRead(input io.ReaderAt, offset int64, size int) []byte {
	data := make([]byte, size)
	readAtFull(input, data, offset)
	return data
}

// NszPath is the name of the NSZ of an NSP.
func NszPath(nspPath string) string {
	return CompressedPath(nspPath)
}

// DecompressedPath is the name of the NSP of an NSZ, or of the XCI of an XCZ.
func DecompressedPath(nszPath string) string {
	extension := ".nsp"
	if strings.EqualFold(filepath.Ext(nszPath), ".xcz") {
		extension = ".xci"
	}
	return strings.TrimSuffix(nszPath, filepath.Ext(nszPath)) + extension
}
