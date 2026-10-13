package switchfs

// The data of every section of an NCA is protected by a tree of SHA-256 hashes: the data
// is hashed in blocks, the hashes form a table, and the hash of that table (the master
// hash) is in the header of the section, itself hashed in the header of the NCA. Tools
// such as NSC_Builder rewrite the key area of the header (to remove the title rights), so
// the SHA-256 of the whole NCA no longer matches its content ID, while its data is still
// the original. Checking the trees tells such a file apart from a damaged one, the way
// hactool does it.

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// errSectionDamaged is returned when the data of a section does not match its hashes.
var errSectionDamaged = errors.New("the data does not match its hashes")

// IntegrityResult tells how much of an NCA its hash trees could check.
type IntegrityResult struct {
	// sections whose data matched every hash
	Checked int
	// sections that cannot be checked on their own: the romfs of an update (it is checked
	// with the game it patches), sparse or compressed sections, or no key to decrypt them
	Unchecked int
}

// hashLayer is a region of an NCA hashed in blocks; the hashes are expected in the bytes
// of its parent layer, or in the master hash for the first one.
type hashLayer struct {
	start, size int64
	blockSize   int64
	// the last block is hashed padded with zeros to the block size (romfs), or as it is
	padded bool
	parent *hashLayer
	master []byte
	// a hash table: its bytes are kept to check the layer below
	keep  bool
	bytes []byte

	block     []byte
	blockFill int64
	index     int64
	// hashes computed before the bytes of the parent arrived
	pending map[int64][32]byte
	err     error
}

func (l *hashLayer) expected(index int64) ([]byte, bool) {
	if l.parent == nil {
		if index == 0 {
			return l.master, true
		}
		return nil, true
	}
	end := (index + 1) * 32
	if int64(len(l.parent.bytes)) < end {
		return nil, false
	}
	return l.parent.bytes[end-32 : end], true
}

func (l *hashLayer) finishBlock() {
	data := l.block[:l.blockFill]
	if l.padded {
		data = l.block
		clear(l.block[l.blockFill:])
	}
	sum := sha256.Sum256(data)
	if want, known := l.expected(l.index); known {
		if !bytes.Equal(want, sum[:]) && l.err == nil {
			l.err = errSectionDamaged
		}
	} else {
		l.pending[l.index] = sum
	}
	l.index++
	l.blockFill = 0
}

// feed gives the layer the plain bytes of the NCA at offset, in order.
func (l *hashLayer) feed(offset int64, data []byte) {
	from, to := offset, offset+int64(len(data))
	if from < l.start {
		from = l.start
	}
	if to > l.start+l.size {
		to = l.start + l.size
	}
	if from >= to {
		return
	}
	part := data[from-offset : to-offset]
	if l.keep {
		l.bytes = append(l.bytes, part...)
	}
	for len(part) > 0 {
		n := copy(l.block[l.blockFill:], part)
		l.blockFill += int64(n)
		part = part[n:]
		if l.blockFill == l.blockSize {
			l.finishBlock()
		}
	}
	if to == l.start+l.size && l.blockFill > 0 {
		l.finishBlock()
	}
}

func (l *hashLayer) check() error {
	if l.err != nil {
		return l.err
	}
	if blocks := (l.size + l.blockSize - 1) / l.blockSize; l.index != blocks {
		return fmt.Errorf("%d of %d blocks read", l.index, blocks)
	}
	for index, sum := range l.pending {
		want, known := l.expected(index)
		if !known || !bytes.Equal(want, sum[:]) {
			return errSectionDamaged
		}
	}
	return nil
}

// sectionLayers reads the hash tree of a section from its header. ok is false when the
// section cannot be checked on its own.
func sectionLayers(fsh *fsHeader, sectionStart int64, sectionSize int64) ([]*hashLayer, bool) {
	header := fsh.fsHeaderBytes
	// an update patch, a sparse section or a compressed one hashes data that is not in it
	if fsh.encType == 4 || !isZero(header[0x100:0x140]) || !isZero(header[0x148+0x8:0x148+0x10]) || !isZero(header[0x178+0x8:0x178+0x10]) {
		return nil, false
	}
	data := header[0x8:0x100]
	layer := func(offset, size, blockSize uint64, padded bool) (*hashLayer, bool) {
		if size == 0 || blockSize == 0 || blockSize > 64<<20 || offset+size > uint64(sectionSize) {
			return nil, false
		}
		return &hashLayer{start: sectionStart + int64(offset), size: int64(size), blockSize: int64(blockSize), padded: padded,
			block: make([]byte, blockSize), pending: map[int64][32]byte{}}, true
	}
	switch fsh.hashType {
	case 2:
		// HierarchicalSha256: a hash table, then the data
		blockSize := uint64(binary.LittleEndian.Uint32(data[0x20:0x24]))
		if binary.LittleEndian.Uint32(data[0x24:0x28]) != 2 {
			return nil, false
		}
		tableOffset, tableSize := binary.LittleEndian.Uint64(data[0x28:]), binary.LittleEndian.Uint64(data[0x30:])
		dataOffset, dataSize := binary.LittleEndian.Uint64(data[0x38:]), binary.LittleEndian.Uint64(data[0x40:])
		table, ok := layer(tableOffset, tableSize, tableSize, false)
		if !ok {
			return nil, false
		}
		table.master, table.keep = data[0:0x20], true
		content, ok := layer(dataOffset, dataSize, blockSize, false)
		if !ok {
			return nil, false
		}
		content.parent = table
		return []*hashLayer{table, content}, true
	case 3:
		// HierarchicalIntegrity (IVFC): six levels, each one the hashes of the next
		if string(data[0:4]) != "IVFC" {
			return nil, false
		}
		levels := int(binary.LittleEndian.Uint32(data[0xC:0x10])) - 1
		if levels < 1 || levels > 6 {
			return nil, false
		}
		layers := []*hashLayer{}
		for i := 0; i < levels; i++ {
			entry := data[0x10+i*0x18:]
			log2 := binary.LittleEndian.Uint32(entry[0x10:0x14])
			if log2 > 26 {
				return nil, false
			}
			current, ok := layer(binary.LittleEndian.Uint64(entry[0:]), binary.LittleEndian.Uint64(entry[8:]), 1<<log2, true)
			if !ok {
				return nil, false
			}
			if i == 0 {
				current.master = data[0xC0:0xE0]
			} else {
				current.parent = layers[i-1]
			}
			current.keep = i < levels-1
			layers = append(layers, current)
		}
		return layers, true
	}
	return nil, false
}

func isZero(data []byte) bool {
	for _, b := range data {
		if b != 0 {
			return false
		}
	}
	return true
}

// ncaIntegrity checks the hash trees of an NCA. plain gives the plain bytes of the NCA
// from offset 0x4000 to its end, in order, to the function it is called with: decrypted
// from an NSP, or straight from the stream of an NCZ.
type ncaIntegrity struct {
	sections []integritySection
	result   IntegrityResult
}

type integritySection struct {
	start, size int64
	encType     byte
	counter     []byte
	layers      []*hashLayer
}

// newNcaIntegrity reads the header of an NCA (its first 0xC00 bytes, encrypted).
func newNcaIntegrity(encryptedHeader []byte) (*ncaIntegrity, *ncaHeader, error) {
	keys, _ := settings.SwitchKeys()
	if keys == nil || keys.GetKey("header_key") == "" {
		return nil, nil, &MissingKeyError{KeyName: "header_key"}
	}
	header, err := DecryptNcaHeader(keys.GetKey("header_key"), encryptedHeader)
	if err != nil {
		return nil, nil, err
	}
	check := &ncaIntegrity{}
	for index := 0; index < 4; index++ {
		entry := getFsEntry(header, index)
		if entry.Size == 0 {
			continue
		}
		fsh, err := getFsHeader(header, index)
		if err != nil {
			// the header of a section does not match its hash in the NCA header
			return nil, nil, fmt.Errorf("section %d: %w", index, errSectionDamaged)
		}
		counter := make([]byte, 16)
		for i := 0; i < 8; i++ {
			counter[i] = fsh.fsHeaderBytes[0x147-i]
		}
		section := integritySection{start: int64(entry.StartOffset), size: int64(entry.Size), encType: fsh.encType, counter: counter}
		if layers, ok := sectionLayers(fsh, section.start, section.size); ok {
			section.layers = layers
		} else {
			check.result.Unchecked++
		}
		check.sections = append(check.sections, section)
	}
	return check, header, nil
}

func (c *ncaIntegrity) feed(offset int64, data []byte) {
	for _, section := range c.sections {
		for _, layer := range section.layers {
			layer.feed(offset, data)
		}
	}
}

func (c *ncaIntegrity) finish() (IntegrityResult, error) {
	for index, section := range c.sections {
		if section.layers == nil {
			continue
		}
		for _, layer := range section.layers {
			if err := layer.check(); err != nil {
				return c.result, fmt.Errorf("section %d: %w", index, err)
			}
		}
		c.result.Checked++
	}
	return c.result, nil
}

// checkNcaIntegrity checks the hash trees of the NCA at offset in an NSP or XCI: its
// sections are decrypted with the key of the NCA (titleKeys are the keys of the tickets
// of the package).
func checkNcaIntegrity(ctx context.Context, input io.ReaderAt, offset int64, size int64, titleKeys map[string][]byte) (IntegrityResult, error) {
	encryptedHeader := make([]byte, 0xC00)
	if err := readAtFull(input, encryptedHeader, offset); err != nil {
		return IntegrityResult{}, err
	}
	check, header, err := newNcaIntegrity(encryptedHeader)
	if err != nil {
		return IntegrityResult{}, err
	}
	var block cipher.Block
	needsKey := false
	for _, section := range check.sections {
		needsKey = needsKey || section.layers != nil && section.encType == 3
	}
	if needsKey {
		key, err := ncaSectionKey(header, titleKeys)
		if err != nil {
			// without the key nothing encrypted can be checked
			result := IntegrityResult{Unchecked: len(check.sections)}
			return result, nil
		}
		block, _ = aes.NewCipher(key)
	}
	buffer := make([]byte, nczChunkSize)
	for i := range check.sections {
		section := &check.sections[i]
		if section.layers == nil {
			continue
		}
		if section.encType != 1 && section.encType != 3 {
			section.layers = nil
			check.result.Unchecked++
			continue
		}
		if section.start+section.size > size {
			return check.result, fmt.Errorf("section %d is outside the NCA", i)
		}
		for position := section.start; position < section.start+section.size; {
			if err := ctx.Err(); err != nil {
				return check.result, err
			}
			length := section.start + section.size - position
			if length > int64(len(buffer)) {
				length = int64(len(buffer))
			}
			chunk := buffer[:length]
			if err := readAtFull(input, chunk, offset+position); err != nil {
				return check.result, err
			}
			if section.encType == 3 {
				cipher.NewCTR(block, nczCounter(section.counter, uint64(position))).XORKeyStream(chunk, chunk)
			}
			check.feed(position, chunk)
			position += int64(len(chunk))
		}
	}
	return check.finish()
}
