package switchfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
)

// The metadata of a NUP whose types a build of the packer of the format counted from 0 is
// written again with them where the format puts them, for Eglantine and Lichii, which read them
// as they are written. It is the fix of NXPress (internal/nup/fixmeta.go). Only the type of each
// record changes, two bytes each, in the blocks of metadata of the packages: no hash covers them,
// the table of contents and the data of the files stay as they are. The blocks as they were are
// kept beside the NUP while they are written, and put back when the NUP does not read the same
// afterwards; a NUP left with such a copy by a work that stopped gets its blocks back before
// anything else. The copy has the name and the format of the one of NXPress, so each puts back
// what the other left.

// NupMetaBackup is the copy of the blocks of metadata of a NUP kept while they are written again.
func NupMetaBackup(path string) string {
	return path + ".nxpress-meta"
}

const nupMetaBackupMagic = "NXPMETA1"

// ErrNupMetaOtherCopy is a copy beside a NUP that is not one of its metadata (another NUP of the
// same name, or a file left by something else): it is left where it is, and the NUP is not written.
var ErrNupMetaOtherCopy = errors.New("is a copy of the metadata of another NUP: remove it to go on")

// FixNupZeroBased writes the types of the metadata of the packages of a NUP counted from 0 where
// the format puts them, and gives how many packages it wrote again: 0 when none had them so. The
// NUP is read again after, and put back as it was when it does not read the same.
func FixNupZeroBased(path string) (int, error) {
	if err := restoreNupMeta(path); err != nil {
		return 0, err
	}
	before, err := ReadNup(path)
	if err != nil {
		return 0, err
	}
	type block struct {
		offset uint64
		old    []byte
		fixed  []byte
	}
	blocks := []block{}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	for _, pkg := range before.Packages {
		if !pkg.ZeroBased {
			continue
		}
		old := make([]byte, pkg.metaSize)
		if _, err := file.ReadAt(old, int64(pkg.metaOffset)); err != nil {
			return 0, err
		}
		fixed, err := nupCountFromOne(old)
		if err != nil {
			return 0, err
		}
		blocks = append(blocks, block{pkg.metaOffset, old, fixed})
	}
	if len(blocks) == 0 {
		return 0, nil
	}

	// the blocks as they were, beside the NUP, with what tells the NUP they belong to: its size
	// and the hash of its table of contents
	header := make([]byte, nupHeaderSize)
	if _, err := file.ReadAt(header, 0); err != nil {
		return 0, err
	}
	var backup bytes.Buffer
	backup.WriteString(nupMetaBackupMagic)
	binary.Write(&backup, binary.LittleEndian, uint64(info.Size()))
	backup.Write(header[0x18:0x38])
	binary.Write(&backup, binary.LittleEndian, uint32(len(blocks)))
	for _, b := range blocks {
		binary.Write(&backup, binary.LittleEndian, b.offset)
		binary.Write(&backup, binary.LittleEndian, uint32(len(b.old)))
		backup.Write(b.old)
	}
	if err := writeSyncedFile(NupMetaBackup(path), backup.Bytes()); err != nil {
		os.Remove(NupMetaBackup(path))
		return 0, fmt.Errorf("the copy of its metadata cannot be written beside it: %w", err)
	}

	for _, b := range blocks {
		if _, err := file.WriteAt(b.fixed, int64(b.offset)); err != nil {
			file.Close()
			return 0, errors.Join(err, restoreNupMeta(path))
		}
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return 0, errors.Join(err, restoreNupMeta(path))
	}
	file.Close()

	// the NUP read again gives the same metadata, counted from 1 now
	after, err := ReadNup(path)
	if err == nil {
		err = sameNupMeta(before, after)
	}
	if err != nil {
		return 0, errors.Join(fmt.Errorf("the NUP does not read the same with its metadata written again, so it is put back as it was: %w", err), restoreNupMeta(path))
	}
	if err := os.Remove(NupMetaBackup(path)); err != nil {
		return 0, err
	}
	durable.SyncFolder(filepath.Dir(path))
	return len(blocks), nil
}

// nupCountFromOne is a block of metadata with each type one up, as parseNupTlvs reads it.
func nupCountFromOne(block []byte) ([]byte, error) {
	fixed := bytes.Clone(block)
	for pos := 0; pos+nupTlvHeadSize <= len(fixed); {
		kind := binary.LittleEndian.Uint16(fixed[pos:])
		size := int(binary.LittleEndian.Uint32(fixed[pos+4:]))
		if size < 0 || size > len(fixed)-pos-nupTlvHeadSize {
			return nil, errors.New("a record of its metadata runs past the end of its block")
		}
		if kind < 0x00FF {
			binary.LittleEndian.PutUint16(fixed[pos:], kind+1)
		}
		pos += nupTlvHeadSize + size
	}
	return fixed, nil
}

// sameNupMeta tells whether two readings of a NUP give the same packages with the same metadata,
// the second counted from 1. The names, the publisher, the version and the icon the library
// shows all come from that metadata.
func sameNupMeta(before *NupContainer, after *NupContainer) error {
	if len(before.Packages) != len(after.Packages) {
		return errors.New("its packages changed")
	}
	for i, a := range after.Packages {
		b := before.Packages[i]
		if a.ZeroBased {
			return fmt.Errorf("package %d is still counted from 0", i)
		}
		if a.TitleId != b.TitleId || a.Kind != b.Kind || a.Version != b.Version || len(a.Meta) != len(b.Meta) {
			return fmt.Errorf("package %d reads otherwise", i)
		}
		for kind, value := range b.Meta {
			if other, ok := a.Meta[kind]; !ok || !bytes.Equal(other, value) {
				return fmt.Errorf("package %d reads otherwise", i)
			}
		}
	}
	return nil
}

// restoreNupMeta puts back the blocks a copy beside a NUP keeps, and removes the copy; nothing
// when there is none. A copy of another NUP (of another size or table of contents) is left where
// it is, and the work stops.
func restoreNupMeta(path string) error {
	data, err := os.ReadFile(NupMetaBackup(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	header := make([]byte, nupHeaderSize)
	if _, err := io.ReadFull(io.NewSectionReader(file, 0, nupHeaderSize), header); err != nil {
		return err
	}
	le := binary.LittleEndian
	other := fmt.Errorf("%s %w", NupMetaBackup(path), ErrNupMetaOtherCopy)
	if len(data) < 8+8+32+4 || string(data[:8]) != nupMetaBackupMagic || int64(le.Uint64(data[8:])) != info.Size() || !bytes.Equal(data[16:48], header[0x18:0x38]) {
		return other
	}
	// every block is checked before one is written: a copy cut short puts back nothing
	type block struct {
		offset uint64
		data   []byte
	}
	count, pos := int(le.Uint32(data[48:])), 52
	blocks := []block{}
	for range count {
		if pos+12 > len(data) {
			return other
		}
		offset, size := le.Uint64(data[pos:]), int(le.Uint32(data[pos+8:]))
		pos += 12
		if size > len(data)-pos || offset > uint64(info.Size()) || uint64(size) > uint64(info.Size())-offset {
			return other
		}
		blocks = append(blocks, block{offset, data[pos : pos+size]})
		pos += size
	}
	for _, b := range blocks {
		if _, err := file.WriteAt(b.data, int64(b.offset)); err != nil {
			return err
		}
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := os.Remove(NupMetaBackup(path)); err != nil {
		return err
	}
	durable.SyncFolder(filepath.Dir(path))
	return nil
}

// writeSyncedFile writes a new file and makes sure it is on the disk, its name too.
func writeSyncedFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	durable.SyncFolder(filepath.Dir(path))
	return nil
}
