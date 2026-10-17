package switchfs

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// The readers of game files take files of unknown origin: whatever the bytes, they return an
// error and never panic, loop or ask for huge amounts of memory.

// fuzzPfs0 is a small PFS0 with one file, as a seed.
func fuzzPfs0() []byte {
	names := []byte("a.nca\x00")
	header := make([]byte, 0x10)
	copy(header, "PFS0")
	binary.LittleEndian.PutUint32(header[4:], 1)
	binary.LittleEndian.PutUint32(header[8:], uint32(len(names)))
	entry := make([]byte, 0x18)
	binary.LittleEndian.PutUint64(entry[8:], 4)
	data := append(append(append(header, entry...), names...), []byte("data")...)
	return data
}

func FuzzOpenGamePackage(f *testing.F) {
	f.Add(fuzzPfs0())
	f.Add([]byte("HEAD"))
	f.Add(make([]byte, 0x200))
	f.Fuzz(func(t *testing.T, data []byte) {
		openGamePackage(bytes.NewReader(data))
	})
}

func FuzzReadPfs0(f *testing.F) {
	f.Add(fuzzPfs0())
	f.Add([]byte("PFS0"))
	f.Fuzz(func(t *testing.T, data []byte) {
		readPfs0(bytes.NewReader(data), 0)
	})
}

func FuzzReadRomfs(f *testing.F) {
	f.Add(make([]byte, 0x50))
	header := make([]byte, 0x200)
	binary.LittleEndian.PutUint64(header, 0x50)
	f.Add(header)
	f.Fuzz(func(t *testing.T, data []byte) {
		if header, err := readRomfsHeader(data); err == nil {
			readRomfsFileEntry(data, header)
		}
	})
}

func FuzzReadXmlCnmt(f *testing.F) {
	f.Add([]byte(`<?xml version="1.0"?><ContentMeta><Type>Application</Type><Id>0x0100000000010000</Id><Version>0</Version></ContentMeta>`))
	f.Fuzz(func(t *testing.T, data []byte) {
		readXmlCnmt(data)
	})
}
