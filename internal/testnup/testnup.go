// Package testnup builds small NUP files (containers of decrypted games) for tests: a header, a
// table of contents with one empty folder per package and no files, then the metadata of each
// package, as the format (github.com/a55uka/nup, version 1) describes them.
package testnup

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
)

const (
	headerSize  = 0x80
	tocHeadSize = 0x30
	packageSize = 0x40
	dirSize     = 0x18
)

// kinds of packages
const (
	Base   = 0
	Update = 1
	Addon  = 2
)

// types of metadata records
const (
	Nacp            = 0x0002
	Icon            = 0x0003
	DisplayVersion  = 0x0004
	TitleName       = 0x0005
	Publisher       = 0x0006
	LocalizedTitles = 0x0009
	LocalizedIcons  = 0x000A
)

// Record is one record of the metadata of a package.
type Record struct {
	Type    uint16
	Payload []byte
}

// Package is one package of a NUP.
type Package struct {
	TitleId uint64
	Kind    byte
	Flags   byte
	Version uint32
	Meta    []Record
}

// Localized is the payload of a record of names or icons by language: tag, data, tag, data...
func Localized(entries ...string) []byte {
	var out bytes.Buffer
	for i := 0; i+1 < len(entries); i += 2 {
		out.WriteByte(byte(len(entries[i])))
		out.WriteString(entries[i])
		binary.Write(&out, binary.LittleEndian, uint32(len(entries[i+1])))
		out.WriteString(entries[i+1])
	}
	return out.Bytes()
}

// Jpeg is a made-up icon that starts as a JPEG image does.
var Jpeg = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{7}, 60)...)

// Build writes a NUP with packages.
func Build(packages []Package) []byte {
	n := len(packages)
	tocSize := tocHeadSize + n*packageSize + n*dirSize
	blobOffset := (headerSize + tocSize + 15) &^ 15

	blocks := make([][]byte, n)
	for i, pkg := range packages {
		var block bytes.Buffer
		for _, record := range pkg.Meta {
			binary.Write(&block, binary.LittleEndian, record.Type)
			binary.Write(&block, binary.LittleEndian, uint16(0))
			binary.Write(&block, binary.LittleEndian, uint32(len(record.Payload)))
			block.Write(record.Payload)
		}
		blocks[i] = block.Bytes()
	}
	offsets := make([]int, n)
	end := blobOffset
	for i, block := range blocks {
		if len(block) > 0 {
			offsets[i] = end
			end = (end + len(block) + 15) &^ 15
		}
	}

	toc := make([]byte, tocSize)
	copy(toc, "TOC\x01")
	binary.LittleEndian.PutUint32(toc[0x04:], uint32(n))
	binary.LittleEndian.PutUint64(toc[0x08:], uint64(n))
	binary.LittleEndian.PutUint64(toc[0x18:], uint64(tocSize))
	for i, pkg := range packages {
		record := toc[tocHeadSize+i*packageSize:]
		binary.LittleEndian.PutUint64(record[0x00:], pkg.TitleId)
		binary.LittleEndian.PutUint32(record[0x08:], uint32(i))
		binary.LittleEndian.PutUint32(record[0x0C:], 0xFFFFFFFF)
		binary.LittleEndian.PutUint32(record[0x10:], 0xFFFFFFFF)
		binary.LittleEndian.PutUint32(record[0x14:], pkg.Version)
		record[0x18] = pkg.Kind
		record[0x19] = pkg.Flags
		if len(blocks[i]) > 0 {
			binary.LittleEndian.PutUint64(record[0x20:], uint64(offsets[i]))
			binary.LittleEndian.PutUint32(record[0x28:], uint32(len(blocks[i])))
		}
	}

	file := make([]byte, end)
	copy(file, "NUP\x01")
	binary.LittleEndian.PutUint16(file[0x04:], headerSize)
	binary.LittleEndian.PutUint16(file[0x06:], 1)
	binary.LittleEndian.PutUint64(file[0x08:], headerSize)
	binary.LittleEndian.PutUint64(file[0x10:], uint64(tocSize))
	binary.LittleEndian.PutUint64(file[0x38:], uint64(blobOffset))
	binary.LittleEndian.PutUint64(file[0x40:], uint64(end))
	binary.LittleEndian.PutUint32(file[0x48:], uint32(n))
	copy(file[headerSize:], toc)
	for i, block := range blocks {
		copy(file[offsets[i]:], block)
	}
	Rehash(file)
	return file
}

// Rehash writes again the hash of the table of contents of a NUP whose table was changed.
func Rehash(file []byte) {
	tocSize := binary.LittleEndian.Uint64(file[0x10:])
	hash := sha256.Sum256(file[headerSize : headerSize+tocSize])
	copy(file[0x18:], hash[:])
}
