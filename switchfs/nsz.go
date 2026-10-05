package switchfs

// NSZ compression, compatible with the format of nsz (https://github.com/nicoboss/nsz),
// read by Tinfoil, DBI and other installers. An NSZ is an NSP whose large NCA files are
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
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs/_crypto"
	"github.com/klauspost/compress/zstd"
)

const (
	nczHeaderSize   = 0x4000
	nczSectionMagic = "NCZSECTN"
	nczBlockMagic   = "NCZBLOCK"
	nczChunkSize    = 4 << 20
	// decoders on the console need a bounded window
	nczWindowSize = 8 << 20
)

// Compression levels offered to the user.
const (
	LevelFast     = "fast"
	LevelBalanced = "balanced"
	LevelMax      = "max"
)

// CompressOptions tune a compression.
type CompressOptions struct {
	// LevelFast, LevelBalanced (default) or LevelMax
	Level string
	// threads used by the encoder; 0 means 1
	Workers int
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
}

// ErrNotCompressible is returned when no NCA of an NSP can be compressed.
var ErrNotCompressible = errors.New("nothing in this file can be compressed")

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

// CompressNsp writes the NSZ of source to target. It does not verify the result, see
// VerifyNsz.
func CompressNsp(ctx context.Context, source string, target string, options CompressOptions) (CompressResult, error) {
	result := CompressResult{}
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

	pfs0, err := readPfs0(input, 0)
	if err != nil {
		return result, err
	}
	if string(mustRead(input, 0, 4)) != pfs0Magic {
		return result, errors.New("not an NSP file")
	}

	titleKeys, err := readTickets(input, pfs0)
	if err != nil {
		return result, err
	}

	plans := make([]*nczPlan, len(pfs0.Files))
	names := make([]string, len(pfs0.Files))
	for i, file := range pfs0.Files {
		names[i] = file.Name
		if !isCompressibleNcaName(file.Name) || file.Size <= nczHeaderSize {
			continue
		}
		plan, err := planNcz(input, int64(file.StartOffset), file.Size, titleKeys)
		if err != nil {
			return result, fmt.Errorf("%s: %w", file.Name, err)
		}
		if plan != nil {
			plans[i] = plan
			names[i] = strings.TrimSuffix(file.Name, ".nca") + ".ncz"
		}
	}
	for _, plan := range plans {
		if plan != nil {
			result.Compressed++
		}
	}
	if result.Compressed == 0 {
		return result, ErrNotCompressible
	}

	output, err := os.Create(target)
	if err != nil {
		return result, err
	}
	writer := &pfs0Writer{file: output, names: names, headerLen: int64(pfs0.HeaderLen)}
	err = writer.begin()

	var done int64
	progress := func(n int64) {
		done += n
		if options.Progress != nil {
			options.Progress(done, result.InputSize)
		}
	}
	for i, file := range pfs0.Files {
		if err != nil {
			break
		}
		if err = ctx.Err(); err != nil {
			break
		}
		writer.startFile()
		if plans[i] != nil {
			err = writeNcz(ctx, writer.file, input, int64(file.StartOffset), int64(file.Size), plans[i], options, progress)
		} else {
			result.Stored++
			err = copyRange(ctx, writer.file, input, int64(file.StartOffset), int64(file.Size), progress)
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
		return result, err
	}
	if info, statErr := os.Stat(target); statErr == nil {
		result.OutputSize = info.Size()
	}
	return result, nil
}

// isCompressibleNcaName tells the NCA files worth compressing; metadata NCA stay as
// they are, like nsz does.
func isCompressibleNcaName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".nca") && !strings.HasSuffix(lower, ".cnmt.nca")
}

// planNcz decides how an NCA is compressed, or returns nil to store it as it is.
func planNcz(input io.ReaderAt, offset int64, size uint64, titleKeys map[string][]byte) (*nczPlan, error) {
	keys, _ := settings.SwitchKeys()
	if keys == nil || keys.GetKey("header_key") == "" {
		return nil, errors.New("prod.keys is needed to compress")
	}
	encHeader := make([]byte, 0xC00)
	if err := readAtFull(input, encHeader, offset); err != nil {
		return nil, err
	}
	header, err := DecryptNcaHeader(keys.GetKey("header_key"), encHeader)
	if err != nil {
		return nil, err
	}
	// program and public data hold the game; the rest is small
	if header.contentType != NcaContentType_Program && header.contentType != NcaContentType_PublicData {
		return nil, nil
	}

	sectionKey, err := ncaSectionKey(header, titleKeys)
	if err != nil {
		return nil, err
	}

	sections := []nczSection{}
	for index := 0; index < 4; index++ {
		entry := getFsEntry(header, index)
		if entry.Size == 0 {
			continue
		}
		fsh, err := getFsHeader(header, index)
		if err != nil {
			return nil, err
		}
		switch fsh.encType {
		case 1, 3:
		default:
			// AesCtrEx sections of update patches use changing counters: stored as they are
			return nil, nil
		}
		counter := make([]byte, 16)
		for i := 0; i < 8; i++ {
			counter[i] = fsh.fsHeaderBytes[0x147-i]
		}
		sections = append(sections, nczSection{
			offset:     uint64(entry.StartOffset),
			size:       uint64(entry.Size),
			cryptoType: uint64(fsh.encType),
			key:        sectionKey,
			counter:    counter,
		})
	}
	if len(sections) == 0 {
		return nil, nil
	}

	// the stream starts right after the copied header and the sections must cover the
	// rest of the NCA without gaps, otherwise decoders cannot rebuild it
	sort.Slice(sections, func(i, j int) bool { return sections[i].offset < sections[j].offset })
	next := uint64(nczHeaderSize)
	for _, section := range sections {
		if section.offset != next {
			return nil, nil
		}
		next = section.offset + section.size
	}
	if next != size {
		return nil, nil
	}
	return &nczPlan{sections: sections}, nil
}

// ncaSectionKey returns the AES key of the sections of an NCA: the title key of its
// ticket (or title.keys) for titles with a rights ID, else the key in the key area.
func ncaSectionKey(header *ncaHeader, titleKeys map[string][]byte) ([]byte, error) {
	keys, _ := settings.SwitchKeys()
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
			return nil, fmt.Errorf("no title key for rights ID %s: the NSP has no ticket and title.keys does not list it", rightsId)
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
func readTickets(input io.ReaderAt, pfs0 *PFS0) (map[string][]byte, error) {
	result := map[string][]byte{}
	for _, file := range pfs0.Files {
		if !strings.HasSuffix(strings.ToLower(file.Name), ".tik") || file.Size > 0x1000 {
			continue
		}
		ticket := make([]byte, file.Size)
		if err := readAtFull(input, ticket, int64(file.StartOffset)); err != nil {
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
	if _, err := output.Write(table.Bytes()); err != nil {
		return err
	}

	workers := options.Workers
	if workers < 1 {
		workers = 1
	}
	encoder, err := zstd.NewWriter(output, zstd.WithEncoderLevel(encoderLevel(options.Level)),
		zstd.WithEncoderConcurrency(workers), zstd.WithWindowSize(nczWindowSize))
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
			if section.cryptoType == 3 {
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

func encoderLevel(level string) zstd.EncoderLevel {
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
	var count uint64
	if err := binary.Read(reader, binary.LittleEndian, &count); err != nil || count == 0 || count > 64 {
		return errors.New("invalid NCZ section table")
	}
	sections := make([]nczSection, count)
	for i := range sections {
		values := make([]uint64, 4)
		if err := binary.Read(reader, binary.LittleEndian, values); err != nil {
			return err
		}
		sections[i] = nczSection{offset: values[0], size: values[1], cryptoType: values[2], key: make([]byte, 16), counter: make([]byte, 16)}
		if _, err := io.ReadFull(reader, sections[i].key); err != nil {
			return err
		}
		if _, err := io.ReadFull(reader, sections[i].counter); err != nil {
			return err
		}
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
		stream = blocks
	} else {
		reader.Seek(position, io.SeekStart)
		decoder, err := zstd.NewReader(reader, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(1<<30))
		if err != nil {
			return err
		}
		defer decoder.Close()
		stream = decoder
	}

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

// nczBlockReader reads the block compressed variant of NCZ.
type nczBlockReader struct {
	source    io.Reader
	blockSize int
	sizes     []uint32
	current   []byte
	decoder   *zstd.Decoder
}

func newNczBlockReader(source io.Reader) (*nczBlockReader, error) {
	header := make([]byte, 8)
	if _, err := io.ReadFull(source, header); err != nil {
		return nil, err
	}
	exponent := header[3]
	count := binary.LittleEndian.Uint32(header[4:8])
	if exponent < 14 || exponent > 32 || count > 1<<24 {
		return nil, errors.New("invalid NCZ block header")
	}
	var decompressedSize uint64
	if err := binary.Read(source, binary.LittleEndian, &decompressedSize); err != nil {
		return nil, err
	}
	sizes := make([]uint32, count)
	if err := binary.Read(source, binary.LittleEndian, sizes); err != nil {
		return nil, err
	}
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	return &nczBlockReader{source: source, blockSize: 1 << exponent, sizes: sizes, decoder: decoder}, nil
}

func (b *nczBlockReader) Read(p []byte) (int, error) {
	for len(b.current) == 0 {
		if len(b.sizes) == 0 {
			b.decoder.Close()
			return 0, io.EOF
		}
		compressed := make([]byte, b.sizes[0])
		b.sizes = b.sizes[1:]
		if _, err := io.ReadFull(b.source, compressed); err != nil {
			return 0, err
		}
		if len(compressed) < b.blockSize {
			decoded, err := b.decoder.DecodeAll(compressed, nil)
			if err != nil {
				return 0, err
			}
			b.current = decoded
		} else {
			b.current = compressed
		}
	}
	n := copy(p, b.current)
	b.current = b.current[n:]
	return n, nil
}

// VerifyNsz checks every NCA of an NSZ: compressed ones are decompressed, and the
// SHA-256 of each NCA must match the content ID in its name. expected holds the hashes of
// the original NCA files by name, when known.
func VerifyNsz(ctx context.Context, path string, expected map[string][32]byte, progress func(done int64, total int64)) error {
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer input.Close()
	pfs0, err := readPfs0(input, 0)
	if err != nil {
		return err
	}
	var total, done int64
	for _, file := range pfs0.Files {
		total += int64(file.Size)
	}
	report := func(n int64) {
		done += n
		if progress != nil {
			progress(done, total)
		}
	}

	for _, file := range pfs0.Files {
		lower := strings.ToLower(file.Name)
		hash := sha256.New()
		switch {
		case strings.HasSuffix(lower, ".ncz"):
			if err := decompressNcz(ctx, hash, input, int64(file.StartOffset), int64(file.Size), nil); err != nil {
				return fmt.Errorf("%s: %w", file.Name, err)
			}
			report(int64(file.Size))
		case strings.HasSuffix(lower, ".nca"):
			if err := copyRange(ctx, hash, input, int64(file.StartOffset), int64(file.Size), report); err != nil {
				return err
			}
		default:
			report(int64(file.Size))
			continue
		}
		var sum [32]byte
		copy(sum[:], hash.Sum(nil))
		original := strings.TrimSuffix(strings.TrimSuffix(file.Name, ".ncz"), ".nca") + ".nca"
		if want, ok := expected[original]; ok && want != sum {
			return fmt.Errorf("%s: the content differs from the original", file.Name)
		}
		if id := contentId(file.Name); id != "" && hex.EncodeToString(sum[:16]) != id {
			return fmt.Errorf("%s: the content does not match its ID", file.Name)
		}
	}
	return nil
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

// HashNcas returns the SHA-256 of every NCA of an NSP by name, to compare with the NSZ.
// It also tells when an NCA does not match the content ID in its name, which means the
// file is damaged or was modified.
func HashNcas(ctx context.Context, path string, progress func(done int64, total int64)) (map[string][32]byte, error) {
	input, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	pfs0, err := readPfs0(input, 0)
	if err != nil {
		return nil, err
	}
	var total, done int64
	for _, file := range pfs0.Files {
		total += int64(file.Size)
	}
	result := map[string][32]byte{}
	for _, file := range pfs0.Files {
		if !strings.HasSuffix(strings.ToLower(file.Name), ".nca") {
			continue
		}
		hash := sha256.New()
		if err := copyRange(ctx, hash, input, int64(file.StartOffset), int64(file.Size), func(n int64) {
			done += n
			if progress != nil {
				progress(done, total)
			}
		}); err != nil {
			return nil, err
		}
		var sum [32]byte
		copy(sum[:], hash.Sum(nil))
		if id := contentId(file.Name); id != "" && hex.EncodeToString(sum[:16]) != id {
			return nil, fmt.Errorf("%s does not match its content ID: the file is damaged or was modified", file.Name)
		}
		result[file.Name] = sum
	}
	return result, nil
}

// DecompressNsz writes the NSP of an NSZ to target.
func DecompressNsz(ctx context.Context, source string, target string, progress func(done int64, total int64)) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	pfs0, err := readPfs0(input, 0)
	if err != nil {
		return err
	}
	names := make([]string, len(pfs0.Files))
	var total, done int64
	for i, file := range pfs0.Files {
		names[i] = file.Name
		if strings.HasSuffix(strings.ToLower(file.Name), ".ncz") {
			names[i] = strings.TrimSuffix(file.Name, ".ncz") + ".nca"
		}
		total += int64(file.Size)
	}
	report := func(n int64) {
		done += n
		if progress != nil {
			progress(done, total)
		}
	}

	output, err := os.Create(target)
	if err != nil {
		return err
	}
	writer := &pfs0Writer{file: output, names: names, headerLen: int64(pfs0.HeaderLen)}
	err = writer.begin()
	for i, file := range pfs0.Files {
		if err != nil {
			break
		}
		writer.startFile()
		if names[i] != file.Name {
			err = decompressNcz(ctx, writer.file, input, int64(file.StartOffset), int64(file.Size), nil)
			report(int64(file.Size))
		} else {
			err = copyRange(ctx, writer.file, input, int64(file.StartOffset), int64(file.Size), report)
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
	}
	return err
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
		// names got longer than in the original: use a bigger, aligned header
		w.headerLen = (needed + 0x1F) &^ 0x1F
	}
	_, err := w.file.Write(make([]byte, w.headerLen))
	return err
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
	return strings.TrimSuffix(nspPath, filepath.Ext(nspPath)) + ".nsz"
}
