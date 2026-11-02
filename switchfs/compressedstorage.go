package switchfs

import (
	"encoding/binary"
	"errors"
	"io"
	"sort"
)

// The compressed storage of an NCA section (firmware 12 and later): the data of the section is
// cut into blocks, most of them of 64 KiB, each stored as it is, as zeros or compressed with LZ4,
// and a table at the end of the data tells where each one is. Its place is in the FS header at
// 0x178 (offset and size of the table after the start of the data level, and its BKTR header).
// Its entries are 0x18 bytes: the offset of the block in the decompressed data, its offset in the
// stored data, its kind (3 for LZ4), the level it was compressed at and its stored size.
// (Atmosphère: fssystem_compressed_storage.hpp, fssystem_compression_common.hpp.)
//
// In an update the data level is in the patched space of the section: each range of it comes
// from the update or from its base game, as the indirect table tells (FS header 0x100). Only the
// blocks the update holds are in its NCA.

// lz4Block is an LZ4 block of an NCA: where it is in the NCA, its size there, the size it
// decompresses to and the level of the compressor that made it.
type lz4Block struct {
	offset  uint64
	size    uint32
	rawSize uint32
	level   int8
}

const bucketNodeSize = 0x4000

// ncaRange is a range of bytes of an NCA.
type ncaRange struct {
	offset, size uint64
}

// bucketEntries reads the entries of a bucket tree of entrySize bytes each: the entry sets are the
// last nodes of the table. It also gives the end offset the tree covers.
func bucketEntries(table []byte, entrySize int, count int) ([][]byte, uint64, error) {
	perSet := (bucketNodeSize - 0x10) / entrySize
	sets := (count + perSet - 1) / perSet
	first := len(table) - sets*bucketNodeSize
	if count <= 0 || len(table) < bucketNodeSize || first < bucketNodeSize {
		return nil, 0, errors.New("invalid bucket tree")
	}
	end := binary.LittleEndian.Uint64(table[8:16])
	entries := make([][]byte, 0, count)
	for s := 0; s < sets; s++ {
		node := table[first+s*bucketNodeSize : first+(s+1)*bucketNodeSize]
		n := int(int32(binary.LittleEndian.Uint32(node[4:8])))
		if n < 0 || n > perSet {
			return nil, 0, errors.New("invalid bucket tree")
		}
		for k := 0; k < n; k++ {
			entries = append(entries, node[0x10+k*entrySize:0x10+(k+1)*entrySize])
		}
	}
	if len(entries) != count {
		return nil, 0, errors.New("invalid bucket tree")
	}
	return entries, end, nil
}

// findLz4Blocks finds the LZ4 blocks of the compressed storage of an NCA that the NCA itself
// holds, read decrypted through the sections of its plan. Sections in sparse storage are left
// out: their table is in the update.
func findLz4Blocks(input io.ReaderAt, offset int64, header *ncaHeader, plan *nczPlan) ([]lz4Block, error) {
	plain := nczPlainReader{input: input, offset: offset, sections: plan.sections}
	read := func(position uint64, size uint64) ([]byte, error) {
		if size > 1<<30 {
			return nil, errors.New("table too big")
		}
		data := make([]byte, size)
		return data, plain.read(data, position)
	}
	blocks := []lz4Block{}
	for index := 0; index < 4; index++ {
		entry := getFsEntry(header, index)
		if entry.Size == 0 {
			continue
		}
		fsh, err := getFsHeader(header, index)
		if err != nil {
			return nil, err
		}
		b := fsh.fsHeaderBytes
		tableOffset, tableSize := binary.LittleEndian.Uint64(b[0x178:]), binary.LittleEndian.Uint64(b[0x180:])
		tableCount := int(int32(binary.LittleEndian.Uint32(b[0x190:])))
		if tableSize == 0 || string(b[0x188:0x18C]) != "BKTR" || fsh.hashType != 3 {
			continue
		}
		if binary.LittleEndian.Uint16(b[0x170:]) != 0 || !isZero(b[0x148+0x8:0x148+0x10]) {
			continue
		}
		start := entry.StartOffset
		// the data level of the hash tree: the last of its 6 levels
		dataLevel := binary.LittleEndian.Uint64(b[0x8+0x10+5*0x18:])

		// where a range of the section is in the NCA, in pieces: the same place, or in an update
		// where the indirect table puts each part of it, when the update holds them all (ok false
		// when a part is in the base game)
		locate := func(virtual uint64, size uint64) ([]ncaRange, bool) { return []ncaRange{{start + virtual, size}}, true }
		patch := !isZero(b[0x100:0x140])
		if patch {
			indirectOffset, indirectSize := binary.LittleEndian.Uint64(b[0x100:]), binary.LittleEndian.Uint64(b[0x108:])
			indirectCount := int(int32(binary.LittleEndian.Uint32(b[0x118:])))
			if string(b[0x110:0x114]) != "BKTR" {
				continue
			}
			table, err := read(start+indirectOffset, indirectSize)
			if err != nil {
				return nil, err
			}
			raw, end, err := bucketEntries(table, 0x14, indirectCount)
			if err != nil {
				return nil, err
			}
			type indirect struct {
				virtual, physical uint64
				storage           int32
			}
			entries := make([]indirect, len(raw))
			for i, e := range raw {
				entries[i] = indirect{binary.LittleEndian.Uint64(e), binary.LittleEndian.Uint64(e[8:]), int32(binary.LittleEndian.Uint32(e[16:]))}
			}
			locate = func(virtual uint64, size uint64) ([]ncaRange, bool) {
				pieces := []ncaRange{}
				i := sort.Search(len(entries), func(i int) bool { return entries[i].virtual > virtual }) - 1
				for size > 0 {
					if i < 0 || i >= len(entries) || entries[i].storage != 1 {
						return nil, false
					}
					next := end
					if i+1 < len(entries) {
						next = entries[i+1].virtual
					}
					length := min(size, next-virtual)
					at := start + entries[i].physical + (virtual - entries[i].virtual)
					// a piece that follows the last one in the NCA joins it
					if n := len(pieces); n > 0 && pieces[n-1].offset+pieces[n-1].size == at {
						pieces[n-1].size += length
					} else {
						pieces = append(pieces, ncaRange{at, length})
					}
					virtual += length
					size -= length
					i++
				}
				return pieces, true
			}
		}

		pieces, ok := locate(dataLevel+tableOffset, tableSize)
		if !ok {
			continue
		}
		table := make([]byte, 0, tableSize)
		for _, piece := range pieces {
			part, err := read(piece.offset, piece.size)
			if err != nil {
				return nil, err
			}
			table = append(table, part...)
		}
		raw, end, err := bucketEntries(table, 0x18, tableCount)
		if err != nil {
			return nil, err
		}
		for i, e := range raw {
			if e[16] != 3 {
				continue
			}
			virtual := binary.LittleEndian.Uint64(e)
			next := end
			if i+1 < len(raw) {
				next = binary.LittleEndian.Uint64(raw[i+1])
			}
			size := binary.LittleEndian.Uint32(e[20:])
			if next <= virtual || next-virtual > 1<<24 || size == 0 {
				continue
			}
			pieces, ok := locate(dataLevel+binary.LittleEndian.Uint64(e[8:]), uint64(size))
			// a block made of pieces apart in the NCA is left as it is
			if !ok || len(pieces) != 1 {
				continue
			}
			blocks = append(blocks, lz4Block{offset: pieces[0].offset, size: size, rawSize: uint32(next - virtual), level: int8(e[17])})
		}
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].offset < blocks[j].offset })
	// blocks must not overlap, and lie after the header
	kept := blocks[:0]
	next := uint64(nczHeaderSize)
	for _, block := range blocks {
		if block.offset >= next {
			kept = append(kept, block)
			next = block.offset + uint64(block.size)
		}
	}
	return kept, nil
}
