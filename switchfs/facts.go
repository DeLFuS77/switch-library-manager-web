package switchfs

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"strings"
)

// FileFacts describes how a game file is stored.
type FileFacts struct {
	// "NSP", "NSZ", "XCI" or "XCZ", by what it holds rather than by its name
	Kind string
	Size int64
	// its NCZ files written as one stream, and in blocks
	Solid  int
	Blocks int
	// of them, the ones in the experimental format NCZSECT2 (an NCA in sparse storage), which
	// only NXPress reads so far: the file must be decompressed with it to be installed
	Sparse int
	// the ones in the experimental archive format NCZSECT3 (LZ4 blocks of Nintendo kept
	// decompressed), which only NXPress reads: decompressed with it to be installed
	Archive int
	// the size of the NSP or XCI it decompresses to; its own size when it holds no NCZ
	DecompressedSize int64
	// the padding after the data of an XCI, which trimming takes away
	Padding int64
	// the bytes of delta fragments of updates
	DeltaFragments int64
}

// how a game file keeps its NCA files (FileFacts.Storage), from what installers and emulators
// read best to what they read least: what the library notes of each file it reads
const (
	// none compressed: an NSP or an XCI
	StoragePlain = "plain"
	// NCZ files in blocks: every installer but Awoo takes it, and the emulators that read NSZ
	StorageBlocks = "blocks"
	// NCZ files written as one stream (solid): every installer takes it, emulators need it
	// decompressed
	StorageSolid = "solid"
	// some NCZ files in the experimental format NCZSECT2 or NCZSECT3, which only NXPress and
	// NXDock read
	StorageSparse  = "nczsect2"
	StorageArchive = "nczsect3"
)

// Storage is how the file keeps its NCA files, the one that limits it most when they differ.
func (facts FileFacts) Storage() string {
	switch {
	case facts.Archive > 0:
		return StorageArchive
	case facts.Sparse > 0:
		return StorageSparse
	case facts.Solid > 0:
		return StorageSolid
	case facts.Blocks > 0:
		return StorageBlocks
	}
	return StoragePlain
}

// ReadStorage tells how a game file keeps its NCA files (see FileFacts.Storage). It reads the
// table of the package and a few bytes of the header of each NCZ, without the keys: quick enough
// for every file of a scan.
func ReadStorage(path string) (string, error) {
	reader, err := OpenFile(path)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	pkg, err := openGamePackage(reader)
	if err != nil {
		return "", err
	}
	facts := FileFacts{}
	if err := countNczs(reader, pkg, &facts); err != nil {
		return "", err
	}
	return facts.Storage(), nil
}

// countNczs counts the NCZ files of a package by how they are compressed, from the table of
// sections of each and the magic of the blocks after it.
func countNczs(reader io.ReaderAt, pkg *gamePackage, facts *FileFacts) error {
	for _, file := range pkg.files {
		if !strings.HasSuffix(strings.ToLower(file.name), ".ncz") || file.size <= nczHeaderSize+0x10 {
			continue
		}
		// the block header follows the section table
		table := make([]byte, 0x10)
		if err := readAtFull(reader, table, file.offset+nczHeaderSize); err != nil {
			return err
		}
		count := int64(binary.LittleEndian.Uint64(table[8:16]))
		if string(table[:8]) == nczSectionMagicV2 {
			facts.Sparse++
		}
		if string(table[:8]) == nczSectionMagicV3 {
			facts.Archive++
		}
		magic := make([]byte, 8)
		if count > 0 && count <= nczMaxSections && readAtFull(reader, magic, file.offset+nczHeaderSize+0x10+count*0x40) == nil && bytes.Equal(magic, []byte(nczBlockMagic)) {
			facts.Blocks++
		} else {
			facts.Solid++
		}
	}
	return nil
}

// DescribeFile tells how a game file is stored: what it is, how its NCZ files are
// compressed, and what trimming would take away from it.
func DescribeFile(path string) (FileFacts, error) {
	facts := FileFacts{}
	reader, err := OpenFile(path)
	if err != nil {
		return facts, err
	}
	defer reader.Close()
	pkg, err := openGamePackage(reader)
	if err != nil {
		return facts, err
	}
	if info, err := os.Stat(path); err == nil {
		facts.Size = info.Size()
	}
	if err := countNczs(reader, pkg, &facts); err != nil {
		return facts, err
	}
	compressed := facts.Solid+facts.Blocks > 0
	switch {
	case pkg.xci && compressed:
		facts.Kind = "XCZ"
	case pkg.xci:
		facts.Kind = "XCI"
	case compressed:
		facts.Kind = "NSZ"
	default:
		facts.Kind = "NSP"
	}
	facts.DecompressedSize = facts.Size
	if compressed {
		if size, err := DecompressedSize(path); err == nil {
			facts.DecompressedSize = size
		}
	}
	if pkg.xci {
		if end, err := XciDataEnd(reader); err == nil && end < facts.Size {
			facts.Padding = facts.Size - end
		}
	} else {
		deltas := deltaFragmentNames(reader, pkg)
		for _, file := range pkg.files {
			if deltas[file.name] {
				facts.DeltaFragments += file.size
			}
		}
	}
	return facts, nil
}
