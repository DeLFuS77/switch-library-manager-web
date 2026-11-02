package switchfs

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// An NCA in sparse storage is a base game released with its update: it stores only the pieces
// of its sections the update does not replace, one after the other from the physical offset
// of the section, with a table at their end that tells where each piece goes in the whole
// section (a bucket tree, as the indirect storage of updates: storage 0 the pieces, storage 1
// zeros). Its FS entry tells the whole section, far past the end of the file.
//
// Each piece is still encrypted as it was in the whole NCA: AES-CTR with the counter of the
// section, at its place in the whole section (virtual), not where the NCA stores it. The table
// is encrypted with the counter of the section with its generation replaced by the sparse
// generation << 16, at its place in the file. (Atmosphère: fssystem_nca_file_system_driver.cpp,
// CreateSparseStorage; nxdumptool: bktr.h.)
//
// An NCZ rebuilds an encrypted section with the counter of the place of each byte in the file,
// so a piece cannot be stored decrypted in it: NCZSECT2 adds to each section the shift of its
// counter (docs/ncz-sparse.md of NXPress).

// the bucket tree of a sparse table: nodes of 16 KiB, entries of 0x14 bytes
const (
	sparseNodeSize  = 0x4000
	sparseEntrySize = 0x14
)

// sparseEntry is a piece of a sparse section: from virtual on in the whole section, at physical
// in the stored data when storage is 0, zeros when it is 1.
type sparseEntry struct {
	virtual  uint64
	physical uint64
	storage  uint32
}

// sparseSections are the NCZ sections of the stored bytes of a section in sparse storage: one
// per stored piece, shifted to the counter of its place in the whole section, and one for its
// table (and the hash data after it), with the sparse counter. ncaSize is the size of the NCA
// file, sectionStart the start of its FS entry, counter the upper counter of the section.
func sparseSections(input io.ReaderAt, ncaOffset int64, ncaSize uint64, sectionStart uint64, fsh *fsHeader, key, counter []byte) ([]nczSection, error) {
	b := fsh.fsHeaderBytes
	tableOffset := binary.LittleEndian.Uint64(b[0x148:])
	tableSize := binary.LittleEndian.Uint64(b[0x150:])
	entryCount := int32(binary.LittleEndian.Uint32(b[0x160:]))
	physical := binary.LittleEndian.Uint64(b[0x168:])
	generation := uint32(binary.LittleEndian.Uint16(b[0x170:]))
	if string(b[0x158:0x15C]) != "BKTR" && entryCount != 0 {
		return nil, errors.New("no table")
	}
	// an empty table: the section has no stored byte (the update holds it all)
	if entryCount == 0 || tableSize == 0 {
		return nil, nil
	}
	if entryCount < 0 {
		return nil, errors.New("invalid table")
	}
	var cryptoType uint64
	switch fsh.encType {
	case 1:
		cryptoType = 1
	case 3:
		cryptoType = 3
	default:
		return nil, fmt.Errorf("encryption %d", fsh.encType)
	}
	// the stored bytes: the pieces, then the table, then the hash data of the table if any
	physicalSize := tableOffset + tableSize
	if metaSize := binary.LittleEndian.Uint64(b[0x1A8:]); metaSize != 0 {
		metaOffset := binary.LittleEndian.Uint64(b[0x1A0:])
		if metaOffset < physicalSize {
			return nil, errors.New("hash data before the end of the table")
		}
		physicalSize = (metaOffset + metaSize + 15) &^ 15
	}
	if tableOffset > physicalSize || physical > ncaSize || physicalSize > ncaSize-physical {
		return nil, errors.New("stored bytes beyond the end of the NCA")
	}

	// the counter of the table: the generation of the counter of the section (its last 4
	// bytes, big endian) replaced by the sparse generation << 16
	sparseCounter := append([]byte(nil), counter...)
	binary.BigEndian.PutUint32(sparseCounter[4:8], generation<<16)
	table := make([]byte, tableSize)
	if err := readAtFull(input, table, ncaOffset+int64(physical+tableOffset)); err != nil {
		return nil, err
	}
	if cryptoType == 3 {
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		cipher.NewCTR(block, nczCounter(sparseCounter, physical+tableOffset)).XORKeyStream(table, table)
	}
	entries, virtualSize, err := readSparseTable(table, int(entryCount))
	if err != nil {
		return nil, err
	}

	sections := []nczSection{}
	for i, entry := range entries {
		end := virtualSize
		if i+1 < len(entries) {
			end = entries[i+1].virtual
		}
		if entry.storage != 0 {
			continue
		}
		length := end - entry.virtual
		if entry.physical > tableOffset || length > tableOffset-entry.physical {
			return nil, errors.New("a piece beyond the stored data")
		}
		at := physical + entry.physical
		section := nczSection{offset: at, size: length, cryptoType: cryptoType, key: key, counter: counter}
		if cryptoType == 3 {
			section.shift = int64(sectionStart+entry.virtual) - int64(at)
			if section.shift%16 != 0 {
				return nil, errors.New("a piece not aligned on 16 bytes")
			}
		}
		sections = append(sections, section)
	}
	sections = append(sections, nczSection{offset: physical + tableOffset, size: physicalSize - tableOffset, cryptoType: cryptoType, key: key, counter: sparseCounter})
	return sections, nil
}

// readSparseTable reads the decrypted bucket tree of a sparse section: a first node whose header
// tells the number of entry sets and the virtual size, then the entry sets, each a node of a
// header and up to 818 entries. The entries are checked: in order of their virtual offset from
// 0, storage 0 or 1, and as many as the FS header tells.
func readSparseTable(table []byte, entryCount int) ([]sparseEntry, uint64, error) {
	if len(table) < sparseNodeSize {
		return nil, 0, errors.New("table too small")
	}
	sets := int(binary.LittleEndian.Uint32(table[4:]))
	virtualSize := binary.LittleEndian.Uint64(table[8:])
	perSet := (sparseNodeSize - 0x10) / sparseEntrySize
	// past the offsets one node holds, the tree has a second level: no game needs it
	if sets <= 0 || sets > (sparseNodeSize-0x10)/8 || sets != (entryCount+perSet-1)/perSet || len(table) < sparseNodeSize*(1+sets) {
		return nil, 0, errors.New("invalid table")
	}
	entries := make([]sparseEntry, 0, entryCount)
	for set := 0; set < sets; set++ {
		node := table[sparseNodeSize*(1+set) : sparseNodeSize*(2+set)]
		count := int(binary.LittleEndian.Uint32(node[4:]))
		if count <= 0 || count > perSet {
			return nil, 0, errors.New("invalid table")
		}
		for i := 0; i < count; i++ {
			e := node[0x10+i*sparseEntrySize:]
			entry := sparseEntry{binary.LittleEndian.Uint64(e), binary.LittleEndian.Uint64(e[8:]), binary.LittleEndian.Uint32(e[16:])}
			if entry.storage > 1 || len(entries) == 0 && entry.virtual != 0 || len(entries) > 0 && entry.virtual <= entries[len(entries)-1].virtual || entry.virtual >= virtualSize {
				return nil, 0, errors.New("invalid table entry")
			}
			entries = append(entries, entry)
		}
	}
	if len(entries) != entryCount {
		return nil, 0, errors.New("invalid table")
	}
	return entries, virtualSize, nil
}
