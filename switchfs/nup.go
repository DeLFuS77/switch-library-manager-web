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
	"regexp"
	"strings"
	"unicode/utf8"
)

// NUP is a container of decrypted Switch games (github.com/a55uka/nup, version 1): a game,
// its updates and its DLC in one file, as plain ExeFS and RomFS trees, each file compressed on
// its own. Nothing in it is encrypted, so it is read without the keys of the console. ReadNup
// reads its header, its table of contents and the metadata of its packages, never the files of
// the game; VerifyNup reads the data of every file to check it (nupverify.go).

const (
	nupMagic       = "NUP\x01"
	nupTocMagic    = "TOC\x01"
	nupHeaderSize  = 0x80
	nupTocHeadSize = 0x30
	nupPackageSize = 0x40
	nupDirSize     = 0x18
	nupFileSize    = 0x50
	nupTlvHeadSize = 8

	// a folder a package does not have (its ExeFS or its RomFS)
	nupNoDir = 0xFFFFFFFF
	// some builds of the packer of the format tag the files among the children of a folder with
	// the high bit, instead of counting them after the folders as the format says: both are read
	nupFileTag = 0x80000000
	// folders inside folders: a RomFS goes a few levels deep, a loop would go on for ever
	nupMaxDepth = 128

	// far above any real file: a damaged or hostile one is refused before it is read whole. The
	// largest known game has 372587 files, a table of 34 MB.
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

// codecs of the data of a file
const (
	nupCodecRaw  = 0
	nupCodecZstd = 1
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

// NupReaderVersion is raised when what the reader tells of a NUP changes (a name cleaned up, a
// record read), so the library reads its NUP files again instead of keeping what it cached.
const NupReaderVersion = 4

// NupPackage is one package of a NUP: the game, one of its updates or one of its DLC.
type NupPackage struct {
	// as stored: the ID of the program for the game and its updates, the ID of the DLC for a DLC
	TitleId uint64
	Kind    byte
	// bit 0: an update stored without its game
	Flags   byte
	Version uint32
	// the folder of the package in Dirs
	RootDir uint32
	// the metadata of the package by type, the first record of each type
	Meta map[uint16][]byte
}

// NupDir is a folder of the tree of a package.
type NupDir struct {
	Name       string
	FirstChild uint32
	ChildCount uint32
}

// NupFile is a file of the tree of a package, with where its data is stored.
type NupFile struct {
	Name string
	// its size uncompressed, and the place and the size of its data in the NUP
	Size         uint64
	ExtentOffset uint64
	ExtentSize   uint64
	// the SHA-256 of the file uncompressed
	Hash  [32]byte
	Codec byte
}

// NupContainer is what the header and the table of contents of a NUP tell.
type NupContainer struct {
	Packages []NupPackage
	Dirs     []NupDir
	Files    []NupFile
	// the children of the folders, each a node: a folder below len(Dirs), else the file at
	// node - len(Dirs)
	Children []uint32
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

	toc := make([]byte, tocSize)
	if _, err := r.ReadAt(toc, int64(tocOffset)); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, fmt.Errorf("NUP table of contents: %w", err)
	}
	if sum := sha256.Sum256(toc); !bytes.Equal(sum[:], tocHash) {
		return nil, errors.New("NUP table of contents is damaged (its hash does not match)")
	}
	if string(toc[:4]) != nupTocMagic {
		return nil, errors.New("NUP table of contents has an unknown format")
	}
	le := binary.LittleEndian
	if uint64(le.Uint32(toc[0x04:])) != packageCount {
		return nil, errors.New("NUP table of contents disagrees with the header")
	}
	dirCount := le.Uint64(toc[0x08:])
	fileCount := le.Uint64(toc[0x10:])
	poolOffset := le.Uint64(toc[0x18:])
	poolSize := le.Uint64(toc[0x20:])
	if dirCount > tocSize/nupDirSize || fileCount > tocSize/nupFileSize {
		return nil, errors.New("NUP folders and files do not fit in the table of contents")
	}
	records := nupTocHeadSize + packageCount*nupPackageSize + dirCount*nupDirSize + fileCount*nupFileSize
	switch {
	case records > tocSize:
		return nil, errors.New("NUP folders and files do not fit in the table of contents")
	case poolOffset < records || poolOffset > tocSize || poolSize > tocSize-poolOffset:
		return nil, errors.New("NUP names are outside the table of contents")
	case (poolOffset-records)%4 != 0:
		return nil, errors.New("NUP list of the children of the folders is malformed")
	}
	pool := toc[poolOffset : poolOffset+poolSize]
	name := func(record []byte) (string, error) {
		offset, length := uint64(le.Uint32(record)), uint64(le.Uint32(record[4:]))
		if offset+length > poolSize {
			return "", errors.New("NUP name is outside the table of contents")
		}
		return string(pool[offset : offset+length]), nil
	}
	children := (poolOffset - records) / 4

	result := &NupContainer{Packages: make([]NupPackage, 0, packageCount), Dirs: make([]NupDir, dirCount),
		Files: make([]NupFile, fileCount), Children: make([]uint32, children)}
	metaTotal := uint64(0)
	for i := uint64(0); i < packageCount; i++ {
		record := toc[nupTocHeadSize+i*nupPackageSize:][:nupPackageSize]
		pkg := NupPackage{
			TitleId: le.Uint64(record[0x00:]),
			RootDir: le.Uint32(record[0x08:]),
			Version: le.Uint32(record[0x14:]),
			Kind:    record[0x18],
			Flags:   record[0x19],
		}
		if pkg.Kind > nupKindLast {
			return nil, fmt.Errorf("NUP package %d has an unknown kind (%d)", i, pkg.Kind)
		}
		exefs, romfs := le.Uint32(record[0x0C:]), le.Uint32(record[0x10:])
		if uint64(pkg.RootDir) >= dirCount || exefs != nupNoDir && uint64(exefs) >= dirCount ||
			romfs != nupNoDir && uint64(romfs) >= dirCount {
			return nil, fmt.Errorf("NUP package %d has its folders outside the table of contents", i)
		}
		metaOffset := le.Uint64(record[0x20:])
		metaSize := uint64(le.Uint32(record[0x28:]))
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

	position := nupTocHeadSize + packageCount*nupPackageSize
	for i := range result.Dirs {
		record := toc[position:][:nupDirSize]
		position += nupDirSize
		dirName, err := name(record)
		if err != nil {
			return nil, err
		}
		dir := NupDir{Name: dirName, FirstChild: le.Uint32(record[0x08:]), ChildCount: le.Uint32(record[0x0C:])}
		if uint64(dir.FirstChild)+uint64(dir.ChildCount) > children {
			return nil, fmt.Errorf("NUP folder %q has its children outside the table of contents", dirName)
		}
		result.Dirs[i] = dir
	}
	for i := range result.Files {
		record := toc[position:][:nupFileSize]
		position += nupFileSize
		fileName, err := name(record)
		if err != nil {
			return nil, err
		}
		file := NupFile{Name: fileName, Size: le.Uint64(record[0x08:]), ExtentOffset: le.Uint64(record[0x10:]),
			ExtentSize: le.Uint64(record[0x18:]), Codec: record[0x40]}
		copy(file.Hash[:], record[0x20:0x40])
		if file.Codec > nupCodecZstd {
			return nil, fmt.Errorf("NUP file %q is stored with codec %d, which NUP version 1 does not have", fileName, file.Codec)
		}
		// a file of 0 bytes has no data; any other has its data in the data region, aligned
		if file.Size > 0 {
			switch {
			case file.ExtentOffset < blobOffset || file.ExtentOffset%16 != 0 || file.ExtentOffset > fileSize ||
				file.ExtentSize > fileSize-file.ExtentOffset:
				return nil, fmt.Errorf("NUP file %q has its data outside the file", fileName)
			case file.Codec == nupCodecRaw && file.ExtentSize != file.Size:
				return nil, fmt.Errorf("NUP file %q is stored raw with %d bytes instead of %d", fileName, file.ExtentSize, file.Size)
			case file.Codec == nupCodecZstd && file.ExtentSize == 0:
				return nil, fmt.Errorf("NUP file %q has no data", fileName)
			}
		}
		result.Files[i] = file
	}
	for i := range result.Children {
		node := le.Uint32(toc[position:])
		position += 4
		if node >= nupFileTag {
			node = uint32(dirCount) + (node - nupFileTag)
		}
		if uint64(node) >= dirCount+fileCount {
			return nil, errors.New("NUP folder holds a file or a folder that is not in the table of contents")
		}
		result.Children[i] = node
	}
	return result, nil
}

// Walk calls fn for every file of a package, with its path inside the package
// ("romfs/Data/stage.bin") and its index in Files, in the order of the tree. A folder held by
// two folders is walked once; a folder inside itself, or one too deep, is an error.
func (container *NupContainer) Walk(pkg int, fn func(path string, file int) error) error {
	const (
		unseen = iota
		open
		walked
	)
	state := make([]byte, len(container.Dirs))
	type frame struct {
		dir    uint32
		next   uint32
		prefix string
	}
	root := container.Packages[pkg].RootDir
	if int(root) >= len(container.Dirs) {
		return errors.New("NUP package has its folder outside the table of contents")
	}
	stack := []frame{{dir: root}}
	state[root] = open
	dirCount := uint32(len(container.Dirs))
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		dir := container.Dirs[top.dir]
		if top.next == dir.ChildCount {
			state[top.dir] = walked
			stack = stack[:len(stack)-1]
			continue
		}
		node := container.Children[dir.FirstChild+top.next]
		top.next++
		if node >= dirCount {
			if err := fn(top.prefix+container.Files[node-dirCount].Name, int(node-dirCount)); err != nil {
				return err
			}
			continue
		}
		switch state[node] {
		case open:
			return fmt.Errorf("NUP folder %q is inside itself", container.Dirs[node].Name)
		case walked:
			continue
		}
		if len(stack) >= nupMaxDepth {
			return errors.New("NUP folders go too deep")
		}
		state[node] = open
		stack = append(stack, frame{dir: node, prefix: top.prefix + container.Dirs[node].Name + "/"})
	}
	return nil
}

// nupLabel is how a package is told in the paths of its files: "game v65536", "update v131072",
// "DLC 0400245021D2D001 v0".
func nupLabel(pkg NupPackage) string {
	switch pkg.Kind {
	case NupKindBase:
		return fmt.Sprintf("game v%d", pkg.Version)
	case NupKindUpdate:
		return fmt.Sprintf("update v%d", pkg.Version)
	case NupKindAddon:
		return fmt.Sprintf("DLC %016X v%d", pkg.TitleId, pkg.Version)
	}
	return fmt.Sprintf("package of kind %d v%d", pkg.Kind, pkg.Version)
}

// parseNupTlvs splits a block of metadata into its records; the block ends where the header of
// the next record would not fit, and a record that runs past the block is an error.
//
// The packer of the format counted the types from 0 for a while (NPDM 0, NACP 1, the name 4,
// the publisher 5), one below the ones the format gives them: type 0 is no type of the format,
// so a block that has one is read with each of its types one up.
func parseNupTlvs(block []byte) (map[uint16][]byte, error) {
	result, err := splitNupTlvs(block)
	if _, shifted := result[0]; err != nil || !shifted {
		return result, err
	}
	moved := map[uint16][]byte{}
	for kind, value := range result {
		if kind < 0x00FF {
			kind++
		}
		moved[kind] = value
	}
	return moved, nil
}

func splitNupTlvs(block []byte) (map[uint16][]byte, error) {
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

// nupNameTags are the title ID and the version some packers leave in the name they store, as a
// file name has them: "Game [0400245021D2C000][v131072]"
var nupNameTags = regexp.MustCompile(`\s*\[(?:[0-9A-Fa-f]{16}|[vV]\d+)\]`)

// nupName is a name stored in a NUP as a name: as text, without the tags of a file name.
func nupName(raw []byte) string {
	return strings.TrimSpace(nupNameTags.ReplaceAllString(nupText(raw, 0x200), ""))
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
					if text := nupName(name); text != "" {
						names[language] = text
					}
				}
			}
		}
	}
	fallback := nupName(pkg.Meta[nupTlvTitleName])
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

// storedSizes are the bytes of the file the data of each package takes, by its index: a piece of
// data shared by several packages (an update stores most of the files of its game unchanged) is
// counted once, with the game first, then the updates, then the DLC, so the sizes add up to the
// data of the file. A package whose tree cannot be walked takes none.
func (container *NupContainer) storedSizes() []int64 {
	sizes := make([]int64, len(container.Packages))
	claimed := map[uint64]bool{}
	for _, kind := range []byte{NupKindBase, NupKindUpdate, NupKindAddon, 3, nupKindLast} {
		for i, pkg := range container.Packages {
			if pkg.Kind != kind {
				continue
			}
			size := int64(0)
			err := container.Walk(i, func(_ string, index int) error {
				file := container.Files[index]
				if file.Size > 0 && !claimed[file.ExtentOffset] {
					claimed[file.ExtentOffset] = true
					size += int64(file.ExtentSize)
				}
				return nil
			})
			if err == nil {
				sizes[i] = size
			}
		}
	}
	return sizes
}

// Metadata is what the packages of a NUP hold, by title ID (see ReadNupMetadata), each with the
// bytes of the file it takes: an update stored more than once takes the bytes of all its copies.
func (container *NupContainer) Metadata() map[string]*ContentMetaAttributes {
	result := map[string]*ContentMetaAttributes{}
	sizes := container.storedSizes()
	stored := map[string]int64{}
	var gameNacp *Nacp
	for _, pkg := range container.Packages {
		if pkg.Kind == NupKindBase && pkg.TitleId != 0 {
			gameNacp = pkg.nacp()
			break
		}
	}
	for i, pkg := range container.Packages {
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
		stored[meta.TitleId] += sizes[i]
		if older, ok := result[meta.TitleId]; ok && older.Version >= meta.Version {
			continue
		}
		result[meta.TitleId] = meta
	}
	for id, meta := range result {
		meta.StoredSize = stored[id]
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
