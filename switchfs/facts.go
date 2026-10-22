package switchfs

import (
	"bytes"
	"encoding/binary"
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
	// the size of the NSP or XCI it decompresses to; its own size when it holds no NCZ
	DecompressedSize int64
	// the padding after the data of an XCI, which trimming takes away
	Padding int64
	// the bytes of delta fragments of updates
	DeltaFragments int64
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
	for _, file := range pkg.files {
		if !strings.HasSuffix(strings.ToLower(file.name), ".ncz") || file.size <= nczHeaderSize+0x10 {
			continue
		}
		// the block header follows the section table
		table := make([]byte, 0x10)
		if err := readAtFull(reader, table, file.offset+nczHeaderSize); err != nil {
			return facts, err
		}
		count := int64(binary.LittleEndian.Uint64(table[8:16]))
		magic := make([]byte, 8)
		if count > 0 && count <= nczMaxSections && readAtFull(reader, magic, file.offset+nczHeaderSize+0x10+count*0x40) == nil && bytes.Equal(magic, []byte(nczBlockMagic)) {
			facts.Blocks++
		} else {
			facts.Solid++
		}
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
