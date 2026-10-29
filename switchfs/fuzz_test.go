package switchfs

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
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

// fuzzNegativeTicket is a PFS0 with a ticket whose size has its top bit set: negative as an
// int64, it was allocated as it was.
func fuzzNegativeTicket() []byte {
	data := fuzzPfs0()
	copy(data[0x10+0x18:], "a.tik")
	binary.LittleEndian.PutUint64(data[0x10+8:], 1<<63|4)
	return data
}

func FuzzOpenGamePackage(f *testing.F) {
	f.Add(fuzzPfs0())
	f.Add(fuzzNegativeTicket())
	f.Add([]byte("HEAD"))
	f.Add(make([]byte, 0x200))
	f.Fuzz(func(t *testing.T, data []byte) {
		openGamePackage(bytes.NewReader(data))
	})
}

func FuzzReadPfs0(f *testing.F) {
	f.Add(fuzzPfs0())
	f.Add(fuzzNegativeTicket())
	f.Add([]byte("PFS0"))
	f.Fuzz(func(t *testing.T, data []byte) {
		readPfs0(bytes.NewReader(data), 0)
	})
}

// The checks the library runs on every file after a scan: the tickets, the title keys and
// what can be compressed, with the keys loaded so they read past the headers.
func FuzzCheckGameFile(f *testing.F) {
	initNszKeys(f, "")
	f.Add(fuzzPfs0())
	f.Add(fuzzNegativeTicket())
	f.Add(makePFS0(pfs0Magic, []string{testRightsId + ".tik"}, [][]byte{makeTicket(testRightsId)}))
	// one file, written again for every input: a folder each would make the fuzzing crawl
	path := filepath.Join(f.TempDir(), "fuzz.nsp")
	f.Fuzz(func(t *testing.T, data []byte) {
		if pkg, err := readGamePackage(bytes.NewReader(data)); err == nil {
			readTickets(bytes.NewReader(data), pkg.files)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		MissingTitleKey(path)
		NotCompressible(path)
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
