package switchfs

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"

	"github.com/disintegration/gift"

	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
)

// A NUP made from a dump folder without control data carries no icon, and the emulators show it
// with none. The cover of its game the library has (the one chosen in Edit, else the one of
// titledb) is made the size of the icons of the console, a JPEG of 256×256, and given to it, as
// NXPress does it (internal/nup/icon.go): its metadata of the game is written again after the end
// of the NUP with the icon, and its table of contents points to it; the data of the files, and
// the metadata as it was, stay where they are. The copy kept beside the NUP while it is written
// has the name and the format of the one of NXPress, so each puts back what the other left.

// NupIconSize is the side of the icons of the console, in pixels.
const NupIconSize = 256

const (
	// an image of more pixels than this is not an icon: it is not decoded
	maxNupIconPixels = 8192 * 8192
	// an image file bigger than this is not read
	MaxNupIconFile = 32 << 20
)

// MakeNupIcon makes an image (JPEG, PNG or GIF) the icon of a game: cut square at its middle,
// made 256×256 and written as a JPEG, as the icons of the console are.
func MakeNupIcon(data []byte) ([]byte, error) {
	if len(data) > MaxNupIconFile {
		return nil, errors.New("the image is too big to be an icon")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("the image cannot be read: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxNupIconPixels {
		return nil, fmt.Errorf("the image is %d×%d pixels, too many for an icon", config.Width, config.Height)
	}
	picture, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("the image cannot be read: %w", err)
	}
	bounds := picture.Bounds()
	side := min(bounds.Dx(), bounds.Dy())
	square := image.Rect(0, 0, side, side).Add(image.Pt(bounds.Min.X+(bounds.Dx()-side)/2, bounds.Min.Y+(bounds.Dy()-side)/2))
	filter := gift.New(gift.Crop(square), gift.Resize(NupIconSize, NupIconSize, gift.LanczosResampling))
	scaled := image.NewNRGBA(filter.Bounds(bounds))
	filter.Draw(scaled, picture)
	// a transparent image is shown on white, as JPEG keeps no transparency
	icon := image.NewRGBA(image.Rect(0, 0, NupIconSize, NupIconSize))
	draw.Draw(icon, icon.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(icon, icon.Bounds(), scaled, scaled.Bounds().Min, draw.Over)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, icon, &jpeg.Options{Quality: 92}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// HasIcon tells whether a NUP carries an icon in any of its packages.
func (container *NupContainer) HasIcon() bool {
	for i := range container.Packages {
		if container.Packages[i].icon() != nil {
			return true
		}
	}
	return false
}

// IconPackage is the package of a NUP that carries the icon of its game: the game, else the
// first package, as the format asks of a packer.
func (container *NupContainer) IconPackage() int {
	for i, pkg := range container.Packages {
		if pkg.Kind == NupKindBase {
			return i
		}
	}
	return 0
}

// NupIconBackup is the copy of the header and the table of contents of a NUP kept while an icon
// is given to it.
func NupIconBackup(path string) string {
	return path + ".nxpress-icon"
}

const nupIconBackupMagic = "NXPICON1"

// ErrNupIconOtherCopy is a copy beside a NUP that is not one of its table of contents: it is left
// where it is, and the NUP is not written.
var ErrNupIconOtherCopy = errors.New("is a copy of the table of contents of another NUP: remove it to go on")

// SetNupIcon gives a NUP that carries no icon the one given (made by MakeNupIcon), and tells
// whether it did: a NUP that carries one keeps it. The header and the table of contents as they
// were are kept beside the NUP while it is written, and put back when it does not read the same
// afterwards; a NUP left with such a copy by a work that stopped gets them back before anything.
func SetNupIcon(path string, icon []byte) (bool, error) {
	if len(icon) == 0 || !bytes.HasPrefix(icon, []byte{0xFF, 0xD8, 0xFF}) && !bytes.HasPrefix(icon, []byte("\x89PNG\r\n\x1a\n")) {
		return false, errors.New("the icon is not a JPEG or PNG image")
	}
	if len(icon) > nupMaxMetaSize/32 {
		return false, errors.New("the icon is too big")
	}
	if err := restoreNupIcon(path); err != nil {
		return false, err
	}
	if err := restoreNupMeta(path); err != nil {
		return false, err
	}
	before, err := ReadNup(path)
	if err != nil {
		return false, err
	}
	if before.HasIcon() {
		return false, nil
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	le := binary.LittleEndian
	header := make([]byte, nupHeaderSize)
	if _, err := file.ReadAt(header, 0); err != nil {
		return false, err
	}
	tocOffset, tocSize := le.Uint64(header[0x08:]), le.Uint64(header[0x10:])
	toc := make([]byte, tocSize)
	if _, err := file.ReadAt(toc, int64(tocOffset)); err != nil {
		return false, err
	}

	// the metadata of the package as it is, without a record of an icon that is no image, and the
	// icon after it, with its type counted as the block counts them
	index := before.IconPackage()
	pkg := before.Packages[index]
	old := make([]byte, pkg.metaSize)
	if _, err := file.ReadAt(old, int64(pkg.metaOffset)); err != nil {
		return false, err
	}
	kind := uint16(nupTlvIcon)
	if pkg.ZeroBased {
		kind--
	}
	var block bytes.Buffer
	for pos := 0; pos+nupTlvHeadSize <= len(old); {
		size := int(le.Uint32(old[pos+4:]))
		if size < 0 || size > len(old)-pos-nupTlvHeadSize {
			return false, errors.New("a record of its metadata runs past the end of its block")
		}
		if le.Uint16(old[pos:]) != kind {
			block.Write(old[pos : pos+nupTlvHeadSize+size])
		}
		pos += nupTlvHeadSize + size
	}
	var head [nupTlvHeadSize]byte
	le.PutUint16(head[0:], kind)
	le.PutUint32(head[4:], uint32(len(icon)))
	block.Write(head[:])
	block.Write(icon)
	if block.Len() > nupMaxMetaSize {
		return false, errors.New("its metadata would be too big with the icon")
	}

	oldSize := info.Size()
	at := (oldSize + 15) / 16 * 16
	end := at + int64(block.Len())
	newToc := bytes.Clone(toc)
	record := newToc[nupTocHeadSize+index*nupPackageSize:]
	le.PutUint64(record[0x20:], uint64(at))
	le.PutUint32(record[0x28:], uint32(block.Len()))
	newHeader := bytes.Clone(header)
	sum := sha256.Sum256(newToc)
	copy(newHeader[0x18:0x38], sum[:])
	le.PutUint64(newHeader[0x40:], uint64(end))

	// the header and the table of contents as they were, beside the NUP
	var backup bytes.Buffer
	backup.WriteString(nupIconBackupMagic)
	binary.Write(&backup, le, uint64(oldSize))
	binary.Write(&backup, le, uint64(end))
	backup.Write(newHeader[0x18:0x38])
	backup.Write(header)
	binary.Write(&backup, le, tocOffset)
	backup.Write(toc)
	if err := writeSyncedFile(NupIconBackup(path), backup.Bytes()); err != nil {
		return false, fmt.Errorf("the copy of its table of contents cannot be written beside it: %w", err)
	}
	undo := func(err error) (bool, error) {
		file.Close()
		return false, errors.Join(err, restoreNupIcon(path))
	}
	if _, err := file.WriteAt(make([]byte, at-oldSize), oldSize); err != nil {
		return undo(err)
	}
	if _, err := file.WriteAt(block.Bytes(), at); err != nil {
		return undo(err)
	}
	if err := file.Sync(); err != nil {
		return undo(err)
	}
	if _, err := file.WriteAt(newToc, int64(tocOffset)); err != nil {
		return undo(err)
	}
	if _, err := file.WriteAt(newHeader, 0); err != nil {
		return undo(err)
	}
	if err := file.Sync(); err != nil {
		return undo(err)
	}
	file.Close()

	after, err := ReadNup(path)
	if err == nil {
		err = sameNupWithIcon(before, after, index, icon)
	}
	if err != nil {
		return false, errors.Join(fmt.Errorf("the NUP does not read the same with the icon, so it is put back as it was: %w", err), restoreNupIcon(path))
	}
	if err := os.Remove(NupIconBackup(path)); err != nil {
		return false, err
	}
	durable.SyncFolder(filepath.Dir(path))
	return true, nil
}

// sameNupWithIcon tells whether a NUP read again after SetNupIcon is the one it was, with the icon.
func sameNupWithIcon(before *NupContainer, after *NupContainer, index int, icon []byte) error {
	if len(before.Packages) != len(after.Packages) || len(before.Files) != len(after.Files) || len(before.Dirs) != len(after.Dirs) {
		return errors.New("its packages, folders or files changed")
	}
	for i, file := range before.Files {
		if after.Files[i] != file {
			return fmt.Errorf("file %q changed", file.Name)
		}
	}
	for i, b := range before.Packages {
		a := after.Packages[i]
		if a.TitleId != b.TitleId || a.Kind != b.Kind || a.Version != b.Version || a.ZeroBased != b.ZeroBased {
			return fmt.Errorf("package %d changed", i)
		}
		for kind, value := range b.Meta {
			if i == index && kind == nupTlvIcon {
				continue
			}
			if !bytes.Equal(a.Meta[kind], value) {
				return fmt.Errorf("the metadata of package %d changed", i)
			}
		}
	}
	if !bytes.Equal(after.Packages[index].icon(), icon) {
		return errors.New("its icon does not read back")
	}
	return nil
}

// restoreNupIcon puts back the header and the table of contents a copy beside a NUP keeps, and
// the size the NUP had, and removes the copy; nothing when there is none.
func restoreNupIcon(path string) error {
	data, err := os.ReadFile(NupIconBackup(path))
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
	le := binary.LittleEndian
	other := fmt.Errorf("%s %w", NupIconBackup(path), ErrNupIconOtherCopy)
	const fixed = 8 + 8 + 8 + 32 + nupHeaderSize + 8
	if len(data) < fixed || string(data[:8]) != nupIconBackupMagic {
		return other
	}
	oldSize, newSize := int64(le.Uint64(data[8:])), int64(le.Uint64(data[16:]))
	newHash := data[24:56]
	header := data[56 : 56+nupHeaderSize]
	tocOffset := le.Uint64(data[56+nupHeaderSize:])
	toc := data[fixed:]
	if oldSize <= 0 || newSize < oldSize || info.Size() < oldSize || info.Size() > newSize ||
		tocOffset+uint64(len(toc)) > uint64(oldSize) || string(header[:4]) != nupMagic {
		return other
	}
	current := make([]byte, nupHeaderSize)
	if _, err := io.ReadFull(io.NewSectionReader(file, 0, nupHeaderSize), current); err != nil {
		return err
	}
	if !bytes.Equal(current[0x18:0x38], header[0x18:0x38]) && !bytes.Equal(current[0x18:0x38], newHash) {
		return other
	}
	if _, err := file.WriteAt(toc, int64(tocOffset)); err != nil {
		return err
	}
	if _, err := file.WriteAt(header, 0); err != nil {
		return err
	}
	if err := file.Truncate(oldSize); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := os.Remove(NupIconBackup(path)); err != nil {
		return err
	}
	durable.SyncFolder(filepath.Dir(path))
	return nil
}
