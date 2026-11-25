package switchfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"
)

// The data of a .nx: the manifest (the trees of files of the versions), the index of the blocks
// and the table of checks, then the blocks themselves, each a piece of at most 1 MiB of one or
// more files, raw or one zstd frame, checked with a CRC-32C for each 16 KiB and the SHA-256 of
// each whole file. OpenNxData reads them for Verify, for unpacking a .nx and for packing it again.

const (
	nxManifestHeader = 96
	nxDirectorySize  = 16
	nxEntrySize      = 24
	nxFileSize       = 64
	nxBlockSize      = 24
	nxCheckSize      = 16384
	nxMaxDepth       = 256
	nxMaxBlock       = 1 << 20
	// the manifest and the tables read whole into memory: far more than a game needs
	nxMaxTables = 1 << 30
)

// NxFile is a file of the manifest: its size and the SHA-256 of its bytes.
type NxFile struct {
	Size      uint64
	Sha256    [32]byte
	firstRef  uint32
	blocks    uint32
	chunkLog2 uint8
}

type nxDirectory struct{ first, count uint32 }

type nxEntry struct {
	name   string
	target uint32
	kind   byte
}

type nxBlock struct {
	offset     uint64
	stored     uint32
	logical    uint32
	codec      byte
	firstCheck uint32
}

// NxData is a .nx open to read the files of its versions.
type NxData struct {
	*NxArchive
	file    *os.File
	dirs    []nxDirectory
	entries []nxEntry
	files   []NxFile
	refs    []uint32
	blocks  []nxBlock
	checks  []uint32
	decoder *zstd.Decoder
}

// OpenNxData opens a .nx to read the files of its versions. Close it after.
func OpenNxData(path string) (*NxData, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, err := openNxData(file)
	if err != nil {
		file.Close()
		return nil, err
	}
	return data, nil
}

func openNxData(file *os.File) (*NxData, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	archive, err := readNx(file, info.Size())
	if err != nil {
		return nil, err
	}
	h := archive.header
	if h.manifestSize > nxMaxTables || h.blockCount*nxBlockSize > nxMaxTables || h.integritySize > nxMaxTables {
		return nil, errors.New("the manifest of the .nx file is larger than this version reads")
	}
	d := &NxData{NxArchive: archive, file: file}
	manifest, err := nxReadAt(file, h.manifestOffset, h.manifestSize)
	if err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(manifest); !bytes.Equal(sum[:], h.manifestSha) {
		return nil, errors.New("the manifest is damaged: its SHA-256 does not match")
	}
	if err := d.readManifest(manifest, h.blockCount); err != nil {
		return nil, err
	}
	if err := d.readBlocks(h); err != nil {
		return nil, err
	}
	if d.decoder, err = zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(nxMaxBlock),
		zstd.WithDecoderMaxMemory(2*nxMaxBlock)); err != nil {
		return nil, err
	}
	return d, nil
}

// Close closes the file.
func (d *NxData) Close() error {
	if d.decoder != nil {
		d.decoder.Close()
	}
	return d.file.Close()
}

func (d *NxData) readManifest(m []byte, blockCount uint64) error {
	le := binary.LittleEndian
	bad := errors.New("the manifest of the .nx file is not valid")
	if !bytes.Equal(m[0:4], []byte("NXMF")) || le.Uint16(m[4:]) != 1 || le.Uint16(m[6:]) != nxManifestHeader ||
		le.Uint64(m[8:]) != uint64(len(m)) || !nxZero(m[80:96]) {
		return bad
	}
	dirCount, fileCount, entryCount, refCount := le.Uint32(m[16:]), le.Uint32(m[20:]), le.Uint32(m[24:]), le.Uint32(m[28:])
	if dirCount < 1 || dirCount > 16777216 || fileCount > 33554432 || entryCount > 50331648 {
		return bad
	}
	table := func(offset uint64, count uint32, size int, alignment uint64) ([]byte, error) {
		if count == 0 {
			if offset != 0 {
				return nil, bad
			}
			return nil, nil
		}
		length := uint64(count) * uint64(size)
		if offset%alignment != 0 || offset < nxManifestHeader || offset > uint64(len(m)) || length > uint64(len(m))-offset {
			return nil, bad
		}
		return m[offset : offset+length], nil
	}
	dirs, err := table(le.Uint64(m[32:]), dirCount, nxDirectorySize, 8)
	if err != nil {
		return err
	}
	files, err := table(le.Uint64(m[40:]), fileCount, nxFileSize, 8)
	if err != nil {
		return err
	}
	entries, err := table(le.Uint64(m[48:]), entryCount, nxEntrySize, 8)
	if err != nil {
		return err
	}
	refs, err := table(le.Uint64(m[56:]), refCount, 4, 4)
	if err != nil {
		return err
	}
	poolOffset, poolSize := le.Uint64(m[64:]), le.Uint64(m[72:])
	if poolOffset%8 != 0 || poolOffset > uint64(len(m)) || poolSize > uint64(len(m))-poolOffset || poolSize > 2<<30 {
		return bad
	}
	pool := m[poolOffset : poolOffset+poolSize]

	d.refs = make([]uint32, refCount)
	for i := range d.refs {
		d.refs[i] = le.Uint32(refs[i*4:])
		if uint64(d.refs[i]) >= blockCount {
			return bad
		}
	}
	d.files = make([]NxFile, fileCount)
	next := uint32(0)
	for i := range d.files {
		row := files[i*nxFileSize:]
		f := NxFile{Size: le.Uint64(row), firstRef: le.Uint32(row[8:]), blocks: le.Uint32(row[12:]), chunkLog2: row[16]}
		copy(f.Sha256[:], row[20:52])
		if f.chunkLog2 < 16 || f.chunkLog2 > 20 || row[17] != 0 || le.Uint16(row[18:]) != 0 || !nxZero(row[52:64]) || f.Size > 1<<63-1 {
			return bad
		}
		chunk := uint64(1) << f.chunkLog2
		if uint64(f.blocks) != (f.Size+chunk-1)/chunk || f.firstRef != next || uint64(f.firstRef)+uint64(f.blocks) > uint64(refCount) {
			return bad
		}
		next += f.blocks
		d.files[i] = f
	}
	if next != refCount {
		return bad
	}
	d.entries = make([]nxEntry, entryCount)
	for i := range d.entries {
		row := entries[i*nxEntrySize:]
		offset, length := le.Uint64(row), uint64(le.Uint32(row[8:]))
		e := nxEntry{target: le.Uint32(row[12:]), kind: row[16]}
		// the offset of a name is from the start of the manifest, within its pool
		if length < 1 || length > 4096 || offset < poolOffset || offset-poolOffset > uint64(len(pool)) ||
			length > uint64(len(pool))-(offset-poolOffset) || row[17] != 0 || le.Uint16(row[18:]) != 0 || le.Uint32(row[20:]) != 0 {
			return bad
		}
		e.name = string(pool[offset-poolOffset : offset-poolOffset+length])
		if !nxPathName(e.name) {
			return errors.New("the manifest of the .nx file has a name that is not safe: " + fmt.Sprintf("%q", e.name))
		}
		switch e.kind {
		case 1:
			if e.target >= dirCount {
				return bad
			}
		case 2:
			if e.target >= fileCount {
				return bad
			}
		default:
			return bad
		}
		d.entries[i] = e
	}
	d.dirs = make([]nxDirectory, dirCount)
	next = 0
	for i := range d.dirs {
		row := dirs[i*nxDirectorySize:]
		dir := nxDirectory{first: le.Uint32(row), count: le.Uint32(row[4:])}
		if dir.first != next || uint64(dir.first)+uint64(dir.count) > uint64(entryCount) || le.Uint32(row[8:]) != 0 || le.Uint32(row[12:]) != 0 {
			return bad
		}
		for e := dir.first + 1; e < dir.first+dir.count; e++ {
			if d.entries[e-1].name >= d.entries[e].name {
				return errors.New("the names of a folder of the .nx file are not in order or are repeated")
			}
		}
		next += dir.count
		d.dirs[i] = dir
	}
	if next != entryCount {
		return bad
	}
	return nil
}

// nxPathName tells a name of a file or folder that is safe to write on a disk.
func nxPathName(name string) bool {
	return utf8.ValidString(name) && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00")
}

func (d *NxData) readBlocks(h *nxHeader) error {
	if h.blockCount == 0 {
		return nil
	}
	le := binary.LittleEndian
	index, err := nxReadAt(d.file, h.blockOffset, h.blockCount*nxBlockSize)
	if err != nil {
		return err
	}
	integrity, err := nxReadAt(d.file, h.integrityOffset, h.integritySize)
	if err != nil {
		return err
	}
	hash := sha256.New()
	hash.Write(index)
	hash.Write(integrity)
	if !bytes.Equal(hash.Sum(nil), h.indexSha) {
		return errors.New("the index of the blocks is damaged: its SHA-256 does not match")
	}
	dataOffset, dataSize := le.Uint64(h.raw[56:]), le.Uint64(h.raw[64:])
	if dataOffset != 2*nxSlotB || dataSize > h.archiveSize-dataOffset {
		return errors.New("the data of the .nx file is out of bounds")
	}
	d.checks = make([]uint32, h.integritySize/4)
	for i := range d.checks {
		d.checks[i] = le.Uint32(integrity[i*4:])
	}
	d.blocks = make([]nxBlock, h.blockCount)
	end, nextCheck := dataOffset, uint64(0)
	for i := range d.blocks {
		row := index[i*nxBlockSize:]
		b := nxBlock{offset: le.Uint64(row), stored: le.Uint32(row[8:]), logical: le.Uint32(row[12:]), codec: row[16], firstCheck: le.Uint32(row[20:])}
		if b.offset%16 != 0 || b.offset < end || b.stored == 0 || b.stored > nxMaxBlock+64<<10 || b.logical == 0 || b.logical > nxMaxBlock ||
			b.codec > 1 || b.codec == 0 && b.stored != b.logical || row[17] != 0 || le.Uint16(row[18:]) != 0 ||
			uint64(b.firstCheck) != nextCheck || b.offset+uint64(b.stored) > dataOffset+dataSize {
			return errors.New("the index of the blocks of the .nx file is not valid")
		}
		end = b.offset + uint64(b.stored)
		nextCheck += (uint64(b.logical) + nxCheckSize - 1) / nxCheckSize
		d.blocks[i] = b
	}
	if nextCheck != uint64(len(d.checks)) {
		return errors.New("the table of checks of the .nx file does not match its blocks")
	}
	return nil
}

// Block reads a block, decompressed and checked with its CRC-32C.
func (d *NxData) block(id uint32, buffer []byte) ([]byte, error) {
	b := d.blocks[id]
	stored, err := nxReadAt(d.file, b.offset, uint64(b.stored))
	if err != nil {
		return nil, err
	}
	data := stored
	if b.codec == 1 {
		if data, err = d.decoder.DecodeAll(stored, buffer[:0]); err != nil {
			return nil, fmt.Errorf("a block of the .nx file cannot be decompressed: %w", err)
		}
	}
	if len(data) != int(b.logical) {
		return nil, errors.New("a block of the .nx file decompresses to another size than it says")
	}
	for k := 0; k*nxCheckSize < len(data); k++ {
		piece := data[k*nxCheckSize : min((k+1)*nxCheckSize, len(data))]
		if crc32.Checksum(piece, nxCastagnoli) != d.checks[int(b.firstCheck)+k] {
			return nil, errors.New("a block of the .nx file is damaged: its CRC-32C does not match")
		}
	}
	return data, nil
}

// Walk gives every file of a version of a game of the .nx, with its path (folders apart with
// "/") and its index in File, in the order of the manifest; folders come before what they hold
// (dir is called for each, the root first with ""). A folder or file reached twice is an error.
func (d *NxData) Walk(app int, snapshot int, dir func(path string) error, file func(path string, index int) error) error {
	root := d.Applications[app].Snapshots[snapshot].root
	if uint64(root) >= uint64(len(d.dirs)) {
		return errors.New("a version of the .nx file has a tree that is not in its manifest")
	}
	seenDirs, seenFiles := map[uint32]bool{}, map[uint32]bool{}
	var walk func(index uint32, path string, depth int) error
	walk = func(index uint32, path string, depth int) error {
		if depth > nxMaxDepth {
			return errors.New("the tree of a version of the .nx file is too deep")
		}
		if seenDirs[index] {
			return errors.New("the tree of a version of the .nx file reaches a folder twice")
		}
		seenDirs[index] = true
		if dir != nil {
			if err := dir(path); err != nil {
				return err
			}
		}
		node := d.dirs[index]
		for e := node.first; e < node.first+node.count; e++ {
			entry := d.entries[e]
			child := entry.name
			if path != "" {
				child = path + "/" + entry.name
			}
			if entry.kind == 1 {
				if err := walk(entry.target, child, depth+1); err != nil {
					return err
				}
				continue
			}
			if seenFiles[entry.target] {
				return errors.New("the tree of a version of the .nx file reaches a file twice")
			}
			seenFiles[entry.target] = true
			if file != nil {
				if err := file(child, int(entry.target)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(root, "", 0)
}

// File is a file of the manifest.
func (d *NxData) File(index int) NxFile {
	return d.files[index]
}

// WriteFile writes the bytes of a file of the manifest to w, each block checked, then the file
// with its SHA-256; done tells the bytes written so far.
func (d *NxData) WriteFile(ctx context.Context, index int, w io.Writer, done func(n int64)) error {
	f := d.files[index]
	hash := sha256.New()
	if err := d.copyFile(ctx, f, io.MultiWriter(w, hash), hash, done); err != nil {
		return err
	}
	return nil
}

func (d *NxData) copyFile(ctx context.Context, f NxFile, w io.Writer, hash hash.Hash, done func(n int64)) error {
	chunk := uint64(1) << f.chunkLog2
	buffer := make([]byte, 0, nxMaxBlock)
	left := f.Size
	for i := uint32(0); i < f.blocks; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := d.refs[f.firstRef+i]
		want := min(chunk, left)
		if uint64(d.blocks[id].logical) != want {
			return errors.New("a file of the .nx file has a block of the wrong size")
		}
		data, err := d.block(id, buffer)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		left -= want
		if done != nil {
			done(int64(want))
		}
	}
	if !bytes.Equal(hash.Sum(nil), f.Sha256[:]) {
		return errors.New("a file of the .nx file is damaged: its SHA-256 does not match")
	}
	return nil
}

// Verify checks a .nx whole: every file of every version against its SHA-256, and every block,
// those no file uses too, against its CRC-32C. progress tells the bytes checked of the total.
func (d *NxData) Verify(ctx context.Context, progress func(done int64, total int64)) error {
	used := map[int]bool{}
	for a := range d.Applications {
		for s := range d.Applications[a].Snapshots {
			if err := d.Walk(a, s, nil, func(_ string, index int) error {
				used[index] = true
				return nil
			}); err != nil {
				return err
			}
		}
	}
	total := int64(0)
	for index := range used {
		total += int64(d.files[index].Size)
	}
	done := int64(0)
	reached := make([]bool, len(d.blocks))
	for index := 0; index < len(d.files); index++ {
		if !used[index] {
			continue
		}
		f := d.files[index]
		for i := uint32(0); i < f.blocks; i++ {
			reached[d.refs[f.firstRef+i]] = true
		}
		hash := sha256.New()
		if err := d.copyFile(ctx, f, hash, hash, func(n int64) {
			done += n
			if progress != nil {
				progress(done, total)
			}
		}); err != nil {
			return err
		}
	}
	buffer := make([]byte, 0, nxMaxBlock)
	for id := range d.blocks {
		if !reached[id] {
			if _, err := d.block(uint32(id), buffer); err != nil {
				return err
			}
		}
	}
	return nil
}

// VerifyNx checks a .nx file whole: every file of each version against its SHA-256, and every
// block, those no version uses too, against the CRC-32C of the integrity table.
func VerifyNx(ctx context.Context, path string, progress func(done int64, total int64)) error {
	data, err := OpenNxData(path)
	if err != nil {
		return err
	}
	defer data.Close()
	return data.Verify(ctx, progress)
}
