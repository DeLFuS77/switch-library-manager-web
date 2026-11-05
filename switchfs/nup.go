package switchfs

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// NUP is a container of decrypted Switch games (github.com/a55uka/nup, version 1): a game,
// its updates and its DLC in one file, as plain ExeFS and RomFS trees, each file compressed on
// its own. Nothing in it is encrypted, so it is read without the keys of the console. Only its
// header, its table of contents and the metadata of its packages are read here, never the
// files of the game.

const (
	nupMagic       = "NUP\x01"
	nupTocMagic    = "TOC\x01"
	nupHeaderSize  = 0x80
	nupTocHeadSize = 0x30
	nupPackageSize = 0x40
	nupTlvHeadSize = 8

	// far above any real file: a damaged or hostile one is refused before it is read whole
	nupMaxTocSize  = 256 << 20
	nupMaxPackages = 4096
	nupMaxMetaSize = 32 << 20
	// all the metadata of a file together: a few icons and names, not the gigabytes a file
	// that points every package at the same big block would make it read
	nupMaxMetaTotal = 64 << 20
)

// kinds of the packages of a NUP; 3 and 4 are reserved by the format and skipped
const (
	NupKindBase   = 0
	NupKindUpdate = 1
	NupKindAddon  = 2
	nupKindLast   = 4
)

// the types of the metadata of a package (TLV records)
const (
	nupTlvNacp            = 0x0002
	nupTlvIcon            = 0x0003
	nupTlvDisplayVersion  = 0x0004
	nupTlvTitleName       = 0x0005
	nupTlvPublisher       = 0x0006
	nupTlvLocalizedTitles = 0x0009
	nupTlvLocalizedIcons  = 0x000A
)

// NupPackage is one package of a NUP: the game, one of its updates or one of its DLC.
type NupPackage struct {
	// as stored: the ID of the program for the game and its updates, the ID of the DLC for a DLC
	TitleId uint64
	Kind    byte
	// bit 0: an update stored without its game
	Flags   byte
	Version uint32
	// the metadata of the package by type, the first record of each type
	Meta map[uint16][]byte
}

// NupContainer is what the header and the table of contents of a NUP tell.
type NupContainer struct {
	Packages []NupPackage
}

// IsNupFile tells a NUP by the extension of its name.
func IsNupFile(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".nup")
}

// ReadNup reads the header, the table of contents and the metadata of the packages of a NUP.
func ReadNup(path string) (*NupContainer, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	return readNup(file, info.Size())
}

// readNup checks a NUP as the format asks of a reader (the sizes and offsets inside the file,
// the hash of the table of contents, no unknown flags or kinds) before it trusts anything of it.
func readNup(r io.ReaderAt, size int64) (*NupContainer, error) {
	header := make([]byte, nupHeaderSize)
	if _, err := r.ReadAt(header, 0); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, fmt.Errorf("NUP header: %w", err)
	}
	if string(header[:4]) != nupMagic {
		if string(header[:3]) == "NUP" {
			return nil, fmt.Errorf("NUP version %d is not supported", header[3])
		}
		return nil, errors.New("not a NUP file")
	}
	headerSize := uint64(binary.LittleEndian.Uint16(header[0x04:]))
	tocOffset := binary.LittleEndian.Uint64(header[0x08:])
	tocSize := binary.LittleEndian.Uint64(header[0x10:])
	tocHash := header[0x18:0x38]
	blobOffset := binary.LittleEndian.Uint64(header[0x38:])
	fileSize := binary.LittleEndian.Uint64(header[0x40:])
	packageCount := uint64(binary.LittleEndian.Uint32(header[0x48:]))
	flags := binary.LittleEndian.Uint32(header[0x4C:])

	switch {
	case headerSize < nupHeaderSize:
		return nil, errors.New("NUP header is too small")
	case flags != 0:
		return nil, fmt.Errorf("NUP flags %#x are not supported", flags)
	case tocOffset != headerSize:
		return nil, errors.New("NUP table of contents is not after the header")
	case tocSize < nupTocHeadSize || tocSize > nupMaxTocSize:
		return nil, fmt.Errorf("NUP table of contents has an invalid size (%d bytes)", tocSize)
	case packageCount == 0 || packageCount > nupMaxPackages:
		return nil, fmt.Errorf("NUP has an invalid number of packages (%d)", packageCount)
	case nupTocHeadSize+packageCount*nupPackageSize > tocSize:
		return nil, errors.New("NUP packages do not fit in the table of contents")
	case blobOffset%16 != 0 || blobOffset < tocOffset+tocSize:
		return nil, errors.New("NUP data region overlaps the table of contents")
	}
	if uint64(size) < fileSize {
		// a copy or a download that stopped before the end
		return nil, fmt.Errorf("NUP file is cut short (%d of %d bytes): %w", size, fileSize, io.ErrUnexpectedEOF)
	}
	if uint64(size) != fileSize || blobOffset > fileSize {
		return nil, fmt.Errorf("NUP file size %d does not match its header (%d)", size, fileSize)
	}

	// the whole table goes through the hash; only its start, which has the packages, is kept
	kept := nupTocHeadSize + packageCount*nupPackageSize
	toc := make([]byte, kept)
	hash := sha256.New()
	section := io.NewSectionReader(r, int64(tocOffset), int64(tocSize))
	if _, err := io.ReadFull(section, toc); err != nil {
		return nil, fmt.Errorf("NUP table of contents: %w", err)
	}
	hash.Write(toc)
	if _, err := io.Copy(hash, section); err != nil {
		return nil, fmt.Errorf("NUP table of contents: %w", err)
	}
	if !bytes.Equal(hash.Sum(nil), tocHash) {
		return nil, errors.New("NUP table of contents is damaged (its hash does not match)")
	}
	if string(toc[:4]) != nupTocMagic {
		return nil, errors.New("NUP table of contents has an unknown format")
	}
	if uint64(binary.LittleEndian.Uint32(toc[0x04:])) != packageCount {
		return nil, errors.New("NUP table of contents disagrees with the header")
	}

	result := &NupContainer{}
	metaTotal := uint64(0)
	for i := uint64(0); i < packageCount; i++ {
		record := toc[nupTocHeadSize+i*nupPackageSize:][:nupPackageSize]
		pkg := NupPackage{
			TitleId: binary.LittleEndian.Uint64(record[0x00:]),
			Version: binary.LittleEndian.Uint32(record[0x14:]),
			Kind:    record[0x18],
			Flags:   record[0x19],
		}
		if pkg.Kind > nupKindLast {
			return nil, fmt.Errorf("NUP package %d has an unknown kind (%d)", i, pkg.Kind)
		}
		metaOffset := binary.LittleEndian.Uint64(record[0x20:])
		metaSize := uint64(binary.LittleEndian.Uint32(record[0x28:]))
		if metaSize > 0 {
			if metaSize > nupMaxMetaSize || metaOffset < tocOffset+tocSize || metaOffset > fileSize || metaSize > fileSize-metaOffset {
				return nil, fmt.Errorf("NUP package %d has its metadata outside the file", i)
			}
			if metaTotal += metaSize; metaTotal > nupMaxMetaTotal {
				return nil, errors.New("NUP metadata is too big")
			}
			block := make([]byte, metaSize)
			if _, err := r.ReadAt(block, int64(metaOffset)); err != nil {
				return nil, fmt.Errorf("NUP package %d metadata: %w", i, err)
			}
			meta, err := parseNupTlvs(block)
			if err != nil {
				return nil, fmt.Errorf("NUP package %d metadata: %w", i, err)
			}
			pkg.Meta = meta
		}
		result.Packages = append(result.Packages, pkg)
	}
	return result, nil
}

// parseNupTlvs splits a block of metadata into its records; the block ends where the header of
// the next record would not fit, and a record that runs past the block is an error.
func parseNupTlvs(block []byte) (map[uint16][]byte, error) {
	result := map[uint16][]byte{}
	for pos := 0; pos+nupTlvHeadSize <= len(block); {
		kind := binary.LittleEndian.Uint16(block[pos:])
		size := int(binary.LittleEndian.Uint32(block[pos+4:]))
		pos += nupTlvHeadSize
		if size < 0 || size > len(block)-pos {
			return nil, errors.New("a record runs past the end of the block")
		}
		if _, seen := result[kind]; !seen {
			result[kind] = block[pos : pos+size]
		}
		pos += size
	}
	return result, nil
}

// parseNupLocalized reads the records of names or icons by language: a length, a BCP-47 tag,
// then a length and the data.
func parseNupLocalized(payload []byte) (map[string][]byte, error) {
	result := map[string][]byte{}
	for pos := 0; pos < len(payload); {
		tagSize := int(payload[pos])
		pos++
		if tagSize == 0 || tagSize > len(payload)-pos-4 {
			return nil, errors.New("a localized record is malformed")
		}
		tag := strings.ToLower(string(payload[pos : pos+tagSize]))
		pos += tagSize
		size := int(binary.LittleEndian.Uint32(payload[pos:]))
		pos += 4
		if size < 0 || size > len(payload)-pos {
			return nil, errors.New("a localized record runs past the end of its block")
		}
		if _, seen := result[tag]; !seen {
			result[tag] = payload[pos : pos+size]
		}
		pos += size
	}
	return result, nil
}

// nupLanguages are the BCP-47 tags of the NUP format for each language of a NACP, in its order.
var nupLanguages = [...]string{"en-us", "en-gb", "ja", "fr", "de", "es-419", "es", "it", "nl", "fr-ca", "pt", "ru", "ko",
	"zh-hant", "zh-hans", "pt-br"}

// nupLanguage is the language of a NACP for a tag: the exact one, else the one of its first part
// ("en" is American English, "zh" Simplified Chinese).
func nupLanguage(tag string) (Language, bool) {
	for i, known := range nupLanguages {
		if tag == known {
			return Language(i), true
		}
	}
	switch primary, _, _ := strings.Cut(tag, "-"); primary {
	case "en":
		return AmericanEnglish, true
	case "zh":
		return Chinese, true
	default:
		for i, known := range nupLanguages {
			if primary == known {
				return Language(i), true
			}
		}
	}
	return 0, false
}

// nupText is a name or a publisher stored in a NUP, cut to the length a NACP allows, as text.
func nupText(raw []byte, limit int) string {
	text := strings.ToValidUTF8(string(raw), "")
	text = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F {
			return -1
		}
		return r
	}, text)
	text = strings.TrimSpace(text)
	for len(text) > limit {
		_, size := utf8.DecodeLastRuneInString(text)
		text = text[:len(text)-size]
	}
	return text
}

// nacp is what a package tells of the game: its NACP when the file has it, else a NACP made of
// the names, publisher and version it gives; nil when it gives none of them.
func (pkg *NupPackage) nacp() *Nacp {
	if raw := pkg.Meta[nupTlvNacp]; len(raw) >= 0x3080 {
		if nacp, err := readNacp(raw, RomfsHeader{}, RomfsFileEntry{size: uint64(len(raw))}); err == nil {
			return &nacp
		}
	}
	names := map[Language]string{}
	if raw, ok := pkg.Meta[nupTlvLocalizedTitles]; ok {
		if localized, err := parseNupLocalized(raw); err == nil {
			for tag, name := range localized {
				if language, ok := nupLanguage(tag); ok {
					if text := nupText(name, 0x200); text != "" {
						names[language] = text
					}
				}
			}
		}
	}
	fallback := nupText(pkg.Meta[nupTlvTitleName], 0x200)
	if fallback == "" && len(names) == 0 {
		return nil
	}
	publisher := nupText(pkg.Meta[nupTlvPublisher], 0x100)
	result := &Nacp{TitleName: map[string]NacpTitle{}, DisplayVersion: nupText(pkg.Meta[nupTlvDisplayVersion], 0x0F),
		AttributesRead: true, NamesRead: true, PublishersRead: true}
	for i := range nupLanguages {
		language := Language(i)
		name, ok := names[language]
		if !ok {
			// the name of the default language stands for the languages the file has no name in
			name = fallback
		}
		result.TitleName[language.String()] = NacpTitle{Language: language, Title: name, Publisher: publisher}
	}
	return result
}

// icon is the icon a package carries: the one of the default language, else American English,
// else the first of the others. Only JPEG and PNG images are returned.
func (pkg *NupPackage) icon() []byte {
	image := func(data []byte) bool {
		return bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}) || bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n"))
	}
	if data := pkg.Meta[nupTlvIcon]; image(data) {
		return data
	}
	localized, err := parseNupLocalized(pkg.Meta[nupTlvLocalizedIcons])
	if err != nil {
		return nil
	}
	if data := localized["en-us"]; image(data) {
		return data
	}
	for _, tag := range nupLanguages {
		if data := localized[tag]; image(data) {
			return data
		}
	}
	return nil
}

// NupTitleId is the title ID the library gives to a package, as text. The game and its updates
// are stored with the ID of the program (from its NPDM): an update takes the ID updates have in
// the eShop, the one of the game with 800 at the end, so the library tells it from the game.
func NupTitleId(pkg NupPackage) string {
	id := pkg.TitleId
	if pkg.Kind == NupKindUpdate && id&0x800 == 0 {
		id = id&^0xFFF | 0x800
	}
	return fmt.Sprintf("%016x", id)
}

// ReadNupMetadata reads what a NUP holds as the readers of NSP and XCI files tell it: the game,
// its newest update and its DLC, each by its title ID, with the names and version the file gives.
// An update stored more than once keeps its newest version only, like a cartridge holds one.
func ReadNupMetadata(path string) (map[string]*ContentMetaAttributes, error) {
	container, err := ReadNup(path)
	if err != nil {
		return nil, err
	}
	return container.Metadata(), nil
}

// Metadata is what the packages of a NUP hold, by title ID (see ReadNupMetadata).
func (container *NupContainer) Metadata() map[string]*ContentMetaAttributes {
	result := map[string]*ContentMetaAttributes{}
	var gameNacp *Nacp
	for _, pkg := range container.Packages {
		if pkg.Kind == NupKindBase && pkg.TitleId != 0 {
			gameNacp = pkg.nacp()
			break
		}
	}
	for _, pkg := range container.Packages {
		if pkg.TitleId == 0 {
			// a package without an ID (a dump without its NPDM) cannot be put anywhere
			continue
		}
		meta := &ContentMetaAttributes{TitleId: NupTitleId(pkg), Version: int(pkg.Version), Contents: map[string]Content{}}
		switch pkg.Kind {
		case NupKindBase:
			meta.Type = "BASE"
			meta.Ncap = gameNacp
		case NupKindUpdate:
			// only its own: the version shown of the game would be wrong for the update
			meta.Type = "UPD"
			meta.Ncap = pkg.nacp()
		case NupKindAddon:
			meta.Type = "DLC"
		default:
			continue
		}
		if older, ok := result[meta.TitleId]; ok && older.Version >= meta.Version {
			continue
		}
		result[meta.TitleId] = meta
	}
	return result
}

// Icon is the icon the NUP carries for its game: the one of the game, else the one of an update.
func (container *NupContainer) Icon() ([]byte, error) {
	for _, kind := range []byte{NupKindBase, NupKindUpdate} {
		for _, pkg := range container.Packages {
			if pkg.Kind == kind {
				if icon := pkg.icon(); icon != nil {
					return icon, nil
				}
			}
		}
	}
	return nil, errors.New("no icon in the NUP file")
}
