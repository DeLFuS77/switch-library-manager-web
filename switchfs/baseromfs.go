package switchfs

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// A base game as its updates read it, for NXDock: a game it merged with its update keeps only
// what that update read of the base, and a newer update may read a little more. NXDock asks the
// library for those few bytes, and the library reads them from the base game it holds: the RomFS
// section of its program NCA, decrypted, at the places of the whole section (the pieces of a
// sparse base at their places, zeros where the base stores nothing). NXDock checks every block
// with the hashes of the new update, so a wrong byte is caught on the console.
//
// An NCZ is decompressed once to a temporary file (a solid one cannot be read from the middle);
// an NSP or XCI is read where it is.

// BaseRomfs is the RomFS section of the program NCA of a base game, opened.
type BaseRomfs struct {
	file    ReadAtCloser
	nca     io.ReaderAt
	temp    *os.File
	start   uint64 // where the section starts in the NCA
	size    uint64 // its size, as the update sees it
	enc     byte
	block   cipher.Block
	counter []byte // the upper half of the counter of the section
	// sparse: the pieces of the section the NCA stores, from physical on
	sparse   []sparseEntry
	physical uint64
}

// ErrNoBaseProgram is told when the file holds no program NCA of the title.
var ErrNoBaseProgram = errors.New("no program of this title in the file")

// OpenBaseRomfs opens the RomFS section of the program NCA of titleId in the base game at path
// (NSP, NSZ, XCI or XCZ).
func OpenBaseRomfs(ctx context.Context, path string, titleId uint64) (*BaseRomfs, error) {
	keys, _ := settings.SwitchKeys()
	if keys == nil || keys.GetKey("header_key") == "" {
		return nil, errors.New("prod.keys is needed")
	}
	file, err := OpenFile(path)
	if err != nil {
		return nil, err
	}
	b := &BaseRomfs{file: file}
	ok := false
	defer func() {
		if !ok {
			b.Close()
		}
	}()
	pkg, err := openGamePackage(file)
	if err != nil {
		return nil, err
	}
	titleKeys, _ := readTickets(file, pkg.files)
	for _, f := range pkg.files {
		lower := strings.ToLower(f.name)
		compressed := strings.HasSuffix(lower, ".ncz")
		if !compressed && !strings.HasSuffix(lower, ".nca") || f.size < 0xC00 {
			continue
		}
		encHeader := make([]byte, 0xC00)
		if err := readAtFull(file, encHeader, f.offset); err != nil {
			return nil, err
		}
		header, err := DecryptNcaHeader(keys.GetKey("header_key"), encHeader)
		if err != nil || header.contentType != NcaContentType_Program {
			continue
		}
		if id, err := strconv.ParseUint(string(header.titleId), 16, 64); err != nil || id != titleId {
			continue
		}
		key, err := ncaSectionKey(keys, header, titleKeys)
		if err != nil {
			return nil, err
		}
		if compressed {
			// the NCA as it was, once, in a temporary file
			temp, err := os.CreateTemp("", "slm-base-*.nca")
			if err != nil {
				return nil, err
			}
			b.temp = temp
			if err := decompressNcz(ctx, temp, file, f.offset, f.size, nil); err != nil {
				return nil, fmt.Errorf("decompressing the base: %w", err)
			}
			b.nca = temp
		} else {
			b.nca = io.NewSectionReader(file, f.offset, f.size)
		}
		if err := b.openSection(header, key); err != nil {
			return nil, err
		}
		ok = true
		return b, nil
	}
	return nil, ErrNoBaseProgram
}

func (b *BaseRomfs) openSection(header *ncaHeader, key []byte) error {
	for index := 0; index < 4; index++ {
		entry := getFsEntry(header, index)
		if entry.Size == 0 {
			continue
		}
		fsh, err := getFsHeader(header, index)
		if err != nil {
			return err
		}
		// the RomFS: a RomFS hashed by IVFC
		if fsh.fsType != 0 || fsh.hashType != 3 {
			continue
		}
		b.start, b.size, b.enc = entry.StartOffset, entry.Size, fsh.encType
		if b.enc == 5 {
			b.enc = 3
		}
		if b.enc != 1 && b.enc != 3 {
			return fmt.Errorf("a RomFS of encryption %d", fsh.encType)
		}
		b.counter = make([]byte, 16)
		for i := 0; i < 8; i++ {
			b.counter[i] = fsh.fsHeaderBytes[0x147-i]
		}
		if b.block, err = aes.NewCipher(key); err != nil {
			return err
		}
		raw := fsh.fsHeaderBytes
		if binary.LittleEndian.Uint16(raw[0x170:]) != 0 || !isZero(raw[0x148+0x8:0x148+0x10]) {
			return b.openSparse(fsh)
		}
		return nil
	}
	return errors.New("no RomFS in the program")
}

// openSparse reads the table of a section in sparse storage (as sparseSections does).
func (b *BaseRomfs) openSparse(fsh *fsHeader) error {
	raw := fsh.fsHeaderBytes
	tableOffset := binary.LittleEndian.Uint64(raw[0x148:])
	tableSize := binary.LittleEndian.Uint64(raw[0x150:])
	count := int32(binary.LittleEndian.Uint32(raw[0x160:]))
	b.physical = binary.LittleEndian.Uint64(raw[0x168:])
	generation := uint32(binary.LittleEndian.Uint16(raw[0x170:]))
	if count <= 0 || tableSize == 0 || tableSize > 64<<20 {
		// nothing stored: all of it in the update
		b.sparse = []sparseEntry{{virtual: 0, storage: 1}}
		return nil
	}
	table := make([]byte, tableSize)
	if err := readAtFull(b.nca, table, int64(b.physical+tableOffset)); err != nil {
		return err
	}
	if b.enc == 3 {
		counter := append([]byte(nil), b.counter...)
		binary.BigEndian.PutUint32(counter[4:8], generation<<16)
		cipher.NewCTR(b.block, nczCounter(counter, b.physical+tableOffset)).XORKeyStream(table, table)
	}
	entries, _, err := readSparseTable(table, int(count))
	if err != nil {
		return err
	}
	b.sparse = entries
	return nil
}

// Size is the size of the section, as the update sees it.
func (b *BaseRomfs) Size() int64 { return int64(b.size) }

// ReadAt reads the section, decrypted, at the places an update reads it.
func (b *BaseRomfs) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || uint64(off) > b.size || uint64(len(p)) > b.size-uint64(off) {
		return 0, io.EOF
	}
	at := uint64(off)
	done := 0
	for done < len(p) {
		n := len(p) - done
		physical := b.start + at // where it is in the NCA
		zero := false
		if b.sparse != nil {
			i := len(b.sparse) - 1
			for i > 0 && b.sparse[i].virtual > at {
				i--
			}
			e := b.sparse[i]
			if i+1 < len(b.sparse) && b.sparse[i+1].virtual-at < uint64(n) {
				n = int(b.sparse[i+1].virtual - at)
			}
			if e.storage != 0 {
				zero = true
			} else {
				physical = b.physical + e.physical + (at - e.virtual)
			}
		}
		chunk := p[done : done+n]
		if zero {
			for i := range chunk {
				chunk[i] = 0
			}
		} else if err := b.readDecrypted(chunk, physical, b.start+at); err != nil {
			return done, err
		}
		done += n
		at += uint64(n)
	}
	return done, nil
}

// readDecrypted reads len(p) bytes stored at physical in the NCA, decrypted with the counter of
// the place virtual (the same for a section stored whole).
func (b *BaseRomfs) readDecrypted(p []byte, physical, virtual uint64) error {
	skip := virtual % 16
	buf := make([]byte, uint64(len(p))+skip)
	if err := readAtFull(b.nca, buf, int64(physical-skip)); err != nil {
		return err
	}
	if b.enc == 3 {
		cipher.NewCTR(b.block, nczCounter(b.counter, virtual-skip)).XORKeyStream(buf, buf)
	}
	copy(p, buf[skip:])
	return nil
}

// Close closes the file and removes the temporary NCA.
func (b *BaseRomfs) Close() error {
	if b.temp != nil {
		name := b.temp.Name()
		b.temp.Close()
		os.Remove(name)
		b.temp = nil
	}
	if b.file != nil {
		b.file.Close()
		b.file = nil
	}
	return nil
}
