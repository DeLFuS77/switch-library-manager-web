package switchfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makePFS0(magic string, names []string, payloads [][]byte) []byte {
	entrySize := PfsfileEntryTableSize
	if magic == hfs0Magic {
		entrySize = HfsfileEntryTableSize
	}
	var stringTable []byte
	nameOffsets := make([]uint32, len(names))
	for i, name := range names {
		nameOffsets[i] = uint32(len(stringTable))
		stringTable = append(stringTable, []byte(name)...)
		stringTable = append(stringTable, 0)
	}
	headerLen := 0x10 + entrySize*len(names) + len(stringTable)
	result := make([]byte, headerLen)
	copy(result, magic)
	binary.LittleEndian.PutUint32(result[4:8], uint32(len(names)))
	binary.LittleEndian.PutUint32(result[8:12], uint32(len(stringTable)))
	offset := 0
	for i, payload := range payloads {
		entry := result[0x10+i*entrySize:]
		binary.LittleEndian.PutUint64(entry[0:8], uint64(offset))
		binary.LittleEndian.PutUint64(entry[8:16], uint64(len(payload)))
		binary.LittleEndian.PutUint32(entry[16:20], nameOffsets[i])
		offset += len(payload)
	}
	copy(result[0x10+entrySize*len(names):], stringTable)
	for _, payload := range payloads {
		result = append(result, payload...)
	}
	return result
}

func TestReadPfs0AndHfs0(t *testing.T) {
	for _, magic := range []string{pfs0Magic, hfs0Magic} {
		t.Run(magic, func(t *testing.T) {
			data := makePFS0(magic, []string{"a.nca", "b.nca"}, [][]byte{{1, 2}, {3, 4, 5}})
			parsed, err := readPfs0(bytes.NewReader(data), 0)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.HeaderLen != uint64(len(data)-5) || len(parsed.Files) != 2 {
				t.Fatalf("unexpected PFS0 metadata: %#v", parsed)
			}
			if parsed.Files[1].Name != "b.nca" || parsed.Files[1].StartOffset != uint64(len(data)-3) {
				t.Fatalf("unexpected second entry: %#v", parsed.Files[1])
			}
		})
	}
}

func TestReadPfs0RejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"short header", []byte("PFS")},
		{"bad magic", make([]byte, 0xC)},
		{"short entry", func() []byte {
			data := make([]byte, 0x10)
			copy(data, pfs0Magic)
			binary.LittleEndian.PutUint32(data[4:8], 1)
			return data
		}()},
		{"bad name offset", func() []byte {
			data := makePFS0(pfs0Magic, []string{"x"}, [][]byte{{}})
			binary.LittleEndian.PutUint32(data[0x10+16:0x10+20], 99)
			return data
		}()},
		{"unterminated name", func() []byte {
			data := makePFS0(pfs0Magic, []string{"x"}, [][]byte{{}})
			data[0x10+16] = 0
			data[0x10+16+1] = 0
			data[len(data)-2] = 'x'
			data = data[:len(data)-1]
			return data
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("parser panicked: %v", recovered)
				}
			}()
			if _, err := readPfs0(bytes.NewReader(test.data), 0); err == nil {
				t.Fatal("expected malformed input error")
			}
		})
	}
}

func TestReadPfs0OffsetAndReaderErrors(t *testing.T) {
	data := append([]byte("prefix"), makePFS0(pfs0Magic, []string{"x"}, [][]byte{{1}})...)
	if _, err := readPfs0(bytes.NewReader(data), int64(len("prefix"))); err != nil {
		t.Fatal(err)
	}
	if _, err := readPfs0(bytes.NewReader(data), -1); err == nil {
		t.Fatal("expected negative offset error")
	}
	if _, err := readPfs0(strings.NewReader(""), 0); err == nil {
		t.Fatal("expected reader error")
	}
}

func TestReadPfs0OverflowAndReadAtFull(t *testing.T) {
	data := makePFS0(pfs0Magic, []string{"x"}, [][]byte{{}})
	binary.LittleEndian.PutUint64(data[0x10:], ^uint64(0))
	if _, err := readPfs0(bytes.NewReader(data), 0); err == nil {
		t.Fatal("expected PFS0 entry offset overflow")
	}
	if err := readAtFull(bytes.NewReader([]byte{1}), make([]byte, 2), 0); err == nil {
		t.Fatal("expected short read error")
	}
	if err := readAtFull(bytes.NewReader([]byte{1}), make([]byte, 0), 0); err != nil {
		t.Fatal(err)
	}
	if err := readAtFull(fullReadErrorReader{}, make([]byte, 1), 0); err == nil {
		t.Fatal("expected error returned after a full read")
	}
}

type fullReadErrorReader struct{}

func (fullReadErrorReader) ReadAt(p []byte, _ int64) (int, error) {
	if len(p) > 0 {
		p[0] = 1
	}
	return len(p), io.ErrUnexpectedEOF
}

// A size with its top bit set, one bit flipped in the entry of a ticket, was negative as an
// int64: the ticket was allocated with it and the server stopped, and a file of that size was
// copied as nothing, its hash the one of nothing. Such a package, or one whose files end past
// its end, is refused wherever it is opened.
func TestSizesPastTheEndOfTheFileAreRefused(t *testing.T) {
	initNszKeys(t, "")
	_, nsp := writeTestNsp(t, t.TempDir())
	// the ticket is the fourth file, the program the first
	entrySize := func(data []byte, index int) []byte { return data[0x10+0x18*index+8:] }
	negative := append([]byte(nil), nsp...)
	binary.LittleEndian.PutUint64(entrySize(negative, 3), binary.LittleEndian.Uint64(entrySize(negative, 3))|1<<63)
	past := append([]byte(nil), nsp...)
	binary.LittleEndian.PutUint64(entrySize(past, 0), binary.LittleEndian.Uint64(entrySize(past, 0))+uint64(len(nsp)))
	for name, data := range map[string][]byte{"negative": negative, "past": past} {
		path := filepath.Join(t.TempDir(), "Game [0100000000010000][v0].nsp")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := openGamePackage(bytes.NewReader(data)); err == nil {
			t.Fatalf("%s: the package opens", name)
		}
		if _, err := MissingTitleKey(path); err == nil {
			t.Fatalf("%s: the title keys are checked", name)
		}
		if err := NotCompressible(path); err == nil || errors.Is(err, ErrNotCompressible) {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := VerifyFile(context.Background(), path, nil); err == nil {
			t.Fatalf("%s: the file is verified", name)
		}
		if _, err := CompressGame(context.Background(), path, CompressedPath(path), CompressOptions{}); err == nil {
			t.Fatalf("%s: the file is compressed", name)
		}
	}
	// the same in the secure partition of a game card
	xci, err := os.ReadFile(writeTestXci(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	root, err := readPfs0(bytes.NewReader(xci), 0xF000)
	if err != nil {
		t.Fatal(err)
	}
	secure := 0xF000 + int(root.Files[1].StartOffset)
	binary.LittleEndian.PutUint64(xci[secure+0x10+8:], binary.LittleEndian.Uint64(xci[secure+0x10+8:])|1<<63)
	if _, err := openGamePackage(bytes.NewReader(xci)); err == nil {
		t.Fatal("a game card with a negative size opens")
	}
	// a file cut short is told as one
	if _, err := openGamePackage(bytes.NewReader(nsp[:len(nsp)-1])); !errors.Is(err, io.ErrUnexpectedEOF) || !strings.Contains(err.Error(), "EOF") {
		t.Fatalf("a package cut short: %v", err)
	}
	if _, err := readTickets(bytes.NewReader(nsp), []packedFile{{name: "a.tik", offset: 0, size: -1}}); err == nil {
		t.Fatal("a ticket of a negative size is read")
	}
	if err := copyRange(context.Background(), io.Discard, bytes.NewReader(nsp), 0, -1, nil); err == nil {
		t.Fatal("a negative range is copied")
	}
}
