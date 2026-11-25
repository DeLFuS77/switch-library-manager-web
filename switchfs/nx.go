package switchfs

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// NX is a container of decrypted Switch games (github.com/fab918/nxformat, header versions 0.2
// and 0.3): games, their updates and their DLC in one file, the files cut in blocks stored once
// each, raw or with zstd. Like a NUP it is read without the keys of the console. ReadNx reads the
// header, the catalog at the end of the file and the NACP of each game, never the files of the
// games: that is enough for the library.

// NxReaderVersion is the version of what ReadNx tells of a file; the library reads its .nx files
// again when it changes.
const NxReaderVersion = 1

const (
	nxHeaderSize      = 256
	nxSlotB           = 4096
	nxCatalogHeader   = 128
	nxApplicationSize = 80
	nxProgramSize     = 64
	nxSnapshotSize    = 120
	nxLocalizedSize   = 24
	nxDependencySize  = 56
	nxExtensionSize   = 32
	nxMediaHeader     = 32
	nxMediaRecord     = 64
	nxAbsent          = 0xffffffff
	nxMaxCatalog      = 16 << 20
	nxMaxMedia        = 64 << 20
)

// the types of snapshot
const (
	NxSnapshotBase             = 0
	NxSnapshotUpdate           = 1
	NxSnapshotDlc              = 2
	NxSnapshotStandaloneUpdate = 3
)

// the extensions of the catalog this reader knows; an unknown one marked required refuses the file
const (
	nxExtHistory         = 0x00010004
	nxExtNativeMetadata  = 0x00010005
	nxExtSourceResources = 0x00010008
)

// the types of media
const (
	nxMediaPng  = 1
	nxMediaJpeg = 2
	nxMediaNacp = 4
)

// NxArchive is what the catalog of a .nx file tells.
type NxArchive struct {
	MinorVersion int
	Generation   uint64
	Applications []NxApplication
	// the header of the generation read, for the data of the files (see OpenNxData)
	header *nxHeader
}

// NxApplication is a game of a .nx file with all its versions and DLC.
type NxApplication struct {
	Id        uint64
	Platform  int // 0 unknown, 1 Switch, 2 Switch 2
	Title     string
	Publisher string
	// the names by language tag (lowercase BCP 47), as the file gives them
	Names map[string]string
	// the snapshot the title, the icon and the NACP belong to; -1 when the file gives none
	MetadataSnapshot int
	Snapshots        []NxSnapshot
	icon             []byte
	nacp             []byte
}

// NxSnapshot is one version of a game (the game, an update) or a DLC, complete in itself.
type NxSnapshot struct {
	Type           int
	Version        uint32
	DisplayVersion string
	ProgramId      uint64 // 0 for a DLC without a program
	// the title ID of a DLC converted from a NUP or an extracted dump; 0 for a DLC from an NSP,
	// which keeps the content ID of its CNMT instead
	AddonTitleId uint64
	ContentId    [16]byte
	LogicalSize  uint64
	nacp         []byte
	// the identity the format gives it: its kind, version and content ID with every path of its
	// tree and the size and SHA-256 of every file
	Identity [32]byte
	// the directory of the manifest its tree starts at
	root uint32
}

// IsNxFile tells a .nx file by the extension of its name.
func IsNxFile(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".nx")
}

// IsEmulatorFile tells a file of decrypted games, for emulators only (a NUP or a .nx), from an
// NSP or an XCI that installs.
func IsEmulatorFile(path string) bool {
	return IsNupFile(path) || IsNxFile(path)
}

var nxCastagnoli = crc32.MakeTable(crc32.Castagnoli)

// ReadNx reads the catalog of a .nx file.
func ReadNx(path string) (*NxArchive, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	return readNx(file, info.Size())
}

// nxHeader is the part of a header slot the reader uses.
type nxHeader struct {
	raw           []byte
	minor         int
	archiveSize   uint64
	catalogOffset uint64
	catalogSize   uint64
	mediaOffset   uint64
	mediaSize     uint64
	catalogSha    []byte
	generation    uint64
	// the manifest, the index of the blocks and the table of checks, for their SHA-256
	manifestOffset, manifestSize   uint64
	blockOffset, blockCount        uint64
	integrityOffset, integritySize uint64
	manifestSha, indexSha          []byte
}

func nxReadAt(r io.ReaderAt, offset uint64, size uint64) ([]byte, error) {
	if offset > 1<<62 || size > 1<<31 {
		return nil, errors.New("a range of the .nx file is out of bounds")
	}
	data := make([]byte, size)
	n, err := r.ReadAt(data, int64(offset))
	if uint64(n) == size {
		return data, nil
	}
	if err == nil || err == io.EOF {
		err = errors.New("the .nx file is cut short")
	}
	return nil, err
}

// parseNxHeader checks one header slot; the error tells why it cannot be used.
func parseNxHeader(page []byte, fileSize uint64) (*nxHeader, error) {
	raw := page[:nxHeaderSize]
	le := binary.LittleEndian
	if !bytes.Equal(raw[0:4], []byte("NXAR")) {
		return nil, errors.New("not a .nx file")
	}
	if crc32.Checksum(raw[:252], nxCastagnoli) != le.Uint32(raw[252:]) {
		return nil, errors.New("the header is damaged")
	}
	if !nxZero(page[nxHeaderSize:]) {
		return nil, errors.New("the page of the header is damaged")
	}
	major, minor := le.Uint16(raw[4:]), le.Uint16(raw[6:])
	if major != 0 || (minor != 2 && minor != 3) {
		return nil, fmt.Errorf("version %d.%d of the .nx format is not supported", major, minor)
	}
	if le.Uint32(raw[8:]) != nxHeaderSize {
		return nil, errors.New("the header has a size that is not 256")
	}
	if le.Uint16(raw[12:]) != 1 || le.Uint16(raw[14:]) != 0 {
		return nil, errors.New("the file needs features of the .nx format this version does not know")
	}
	if !nxZero(raw[228:252]) {
		return nil, errors.New("reserved bytes of the header are not zero")
	}
	h := &nxHeader{raw: raw, minor: int(minor), archiveSize: le.Uint64(raw[16:]), catalogOffset: le.Uint64(raw[24:]),
		catalogSize: le.Uint64(raw[32:]), mediaOffset: le.Uint64(raw[40:]), mediaSize: le.Uint64(raw[48:]),
		catalogSha: raw[120:152], generation: le.Uint64(raw[216:]),
		manifestOffset: le.Uint64(raw[72:]), manifestSize: le.Uint64(raw[80:]), blockOffset: le.Uint64(raw[88:]),
		blockCount: le.Uint64(raw[96:]), integrityOffset: le.Uint64(raw[104:]), integritySize: le.Uint64(raw[112:]),
		manifestSha: raw[152:184], indexSha: raw[184:216]}
	if h.manifestSize < 96 || h.manifestSize > 4<<30 || h.manifestOffset%16 != 0 || h.manifestOffset < 2*nxSlotB ||
		h.manifestOffset > h.archiveSize || h.manifestSize > h.archiveSize-h.manifestOffset {
		return nil, errors.New("the manifest is out of bounds")
	}
	if h.blockCount > 1<<32 || (h.blockCount == 0) != (h.blockOffset == 0) || (h.blockCount == 0) != (h.integrityOffset == 0) ||
		(h.blockCount == 0) != (h.integritySize == 0) || h.integritySize%4 != 0 ||
		h.blockCount != 0 && (h.blockOffset%16 != 0 || h.integrityOffset%16 != 0 || h.blockOffset > h.archiveSize ||
			h.blockCount*24 > h.archiveSize-h.blockOffset || h.integrityOffset > h.archiveSize ||
			h.integritySize > h.archiveSize-h.integrityOffset) {
		return nil, errors.New("the index of the blocks is out of bounds")
	}
	if h.archiveSize > fileSize || h.archiveSize > 1<<63-1 {
		return nil, errors.New("the file is shorter than its header says: it is cut short")
	}
	if h.catalogSize < nxCatalogHeader || h.catalogSize > nxMaxCatalog || h.catalogOffset%16 != 0 ||
		h.catalogOffset < 2*nxSlotB || h.catalogOffset > h.archiveSize || h.catalogSize > h.archiveSize-h.catalogOffset {
		return nil, errors.New("the catalog is out of bounds")
	}
	if (h.mediaOffset == 0) != (h.mediaSize == 0) || h.mediaOffset != 0 && (h.mediaOffset%16 != 0 ||
		h.mediaSize < nxMediaHeader || h.mediaOffset < 2*nxSlotB || h.mediaOffset > h.archiveSize ||
		h.mediaSize > h.archiveSize-h.mediaOffset) {
		return nil, errors.New("the media are out of bounds")
	}
	return h, nil
}

func nxZero(data []byte) bool {
	for _, b := range data {
		if b != 0 {
			return false
		}
	}
	return true
}

func readNx(r io.ReaderAt, fileSize int64) (*NxArchive, error) {
	if fileSize < 2*nxSlotB {
		return nil, errors.New("the .nx file is cut short")
	}
	start, err := nxReadAt(r, 0, 2*nxSlotB)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(start[0:4], []byte("NXAR")) && !bytes.Equal(start[nxSlotB:nxSlotB+4], []byte("NXAR")) {
		return nil, errors.New("not a .nx file")
	}
	a, errA := parseNxHeader(start[:nxSlotB], uint64(fileSize))
	b, errB := parseNxHeader(start[nxSlotB:], uint64(fileSize))
	// the newest valid slot first; the other when the newest one's catalog does not check
	slots := []*nxHeader{}
	switch {
	case a != nil && b != nil && a.generation == b.generation:
		if !bytes.Equal(a.raw, b.raw) {
			return nil, errors.New("the two headers disagree")
		}
		slots = append(slots, a)
	case a != nil && b != nil && a.generation > b.generation:
		slots = append(slots, a, b)
	case a != nil && b != nil:
		slots = append(slots, b, a)
	case a != nil:
		slots = append(slots, a)
	case b != nil:
		slots = append(slots, b)
	default:
		return nil, errA
	}
	_ = errB
	var firstErr error
	for _, h := range slots {
		archive, err := readNxCatalog(r, h)
		if err == nil {
			return archive, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

// nxCatalog walks the tables of the catalog with checked bounds.
type nxCatalog struct {
	data []byte
	pool []byte
}

func (c *nxCatalog) table(offset uint64, count uint32, size int) ([]byte, error) {
	if count == 0 {
		if offset != 0 {
			return nil, errors.New("an empty table of the catalog has an offset")
		}
		return nil, nil
	}
	length := uint64(count) * uint64(size)
	if offset%8 != 0 || offset < nxCatalogHeader || offset > uint64(len(c.data)) || length > uint64(len(c.data))-offset {
		return nil, errors.New("a table of the catalog is out of bounds")
	}
	return c.data[offset : offset+length], nil
}

// text is a string of the pool: "" when absent; an error when present and empty or not UTF-8.
func (c *nxCatalog) text(offset uint32, length uint32) (string, error) {
	if offset == nxAbsent {
		if length != 0 {
			return "", errors.New("an absent string of the catalog has a length")
		}
		return "", nil
	}
	if length == 0 {
		return "", errors.New("a string of the catalog is present but empty")
	}
	if uint64(offset)+uint64(length) > uint64(len(c.pool)) {
		return "", errors.New("a string of the catalog is out of bounds")
	}
	value := c.pool[offset : offset+length]
	if !utf8.Valid(value) || bytes.IndexByte(value, 0) >= 0 {
		return "", errors.New("a string of the catalog is not UTF-8 text")
	}
	return string(value), nil
}

func readNxCatalog(r io.ReaderAt, h *nxHeader) (*NxArchive, error) {
	le := binary.LittleEndian
	data, err := nxReadAt(r, h.catalogOffset, h.catalogSize)
	if err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(data); !bytes.Equal(sum[:], h.catalogSha) {
		return nil, errors.New("the catalog is damaged: its SHA-256 does not match")
	}
	if err := nxCheckSha(r, h.manifestSha, "the manifest", [2]uint64{h.manifestOffset, h.manifestSize}); err != nil {
		return nil, err
	}
	if err := nxCheckSha(r, h.indexSha, "the index of the blocks", [2]uint64{h.blockOffset, h.blockCount * 24},
		[2]uint64{h.integrityOffset, h.integritySize}); err != nil {
		return nil, err
	}
	if !bytes.Equal(data[0:4], []byte("NXCT")) || le.Uint16(data[4:]) != 1 || le.Uint16(data[6:]) != nxCatalogHeader ||
		le.Uint64(data[8:]) != h.catalogSize || le.Uint32(data[16:]) != 0 || le.Uint32(data[44:]) != 0 || !nxZero(data[112:128]) {
		return nil, errors.New("the header of the catalog is not valid")
	}
	appCount, programCount, snapshotCount := le.Uint32(data[20:]), le.Uint32(data[24:]), le.Uint32(data[28:])
	localizedCount, dependencyCount, extensionCount := le.Uint32(data[32:]), le.Uint32(data[36:]), le.Uint32(data[40:])
	if appCount < 1 || appCount > 4096 || programCount > 16384 || snapshotCount < 1 || snapshotCount > 65536 ||
		localizedCount > 16384 || dependencyCount > 32768 || extensionCount > 4096 {
		return nil, errors.New("the catalog has more entries than the format allows")
	}
	c := &nxCatalog{data: data}
	poolOffset, poolSize := le.Uint64(data[96:]), le.Uint64(data[104:])
	if poolOffset%8 != 0 || poolOffset > uint64(len(data)) || poolSize > uint64(len(data))-poolOffset {
		return nil, errors.New("the strings of the catalog are out of bounds")
	}
	c.pool = data[poolOffset : poolOffset+poolSize]
	apps, err := c.table(le.Uint64(data[48:]), appCount, nxApplicationSize)
	if err != nil {
		return nil, err
	}
	programs, err := c.table(le.Uint64(data[56:]), programCount, nxProgramSize)
	if err != nil {
		return nil, err
	}
	snapshots, err := c.table(le.Uint64(data[64:]), snapshotCount, nxSnapshotSize)
	if err != nil {
		return nil, err
	}
	localized, err := c.table(le.Uint64(data[72:]), localizedCount, nxLocalizedSize)
	if err != nil {
		return nil, err
	}
	if _, err := c.table(le.Uint64(data[80:]), dependencyCount, nxDependencySize); err != nil {
		return nil, err
	}
	extensions, err := c.table(le.Uint64(data[88:]), extensionCount, nxExtensionSize)
	if err != nil {
		return nil, err
	}

	media, err := readNxMedia(r, h)
	if err != nil {
		return nil, err
	}

	// the programs, checked against the applications they name
	programIds := make([]uint64, programCount)
	programApps := make([]uint32, programCount)
	programSnapshots := make([][2]uint32, programCount)
	for i := uint32(0); i < programCount; i++ {
		row := programs[i*nxProgramSize:]
		programIds[i] = le.Uint64(row)
		programApps[i] = le.Uint32(row[8:])
		programSnapshots[i] = [2]uint32{le.Uint32(row[32:]), le.Uint32(row[36:])}
		if programSnapshots[i][1] == 0 || uint64(programSnapshots[i][0])+uint64(programSnapshots[i][1]) > uint64(snapshotCount) {
			return nil, errors.New("a program of the catalog has a wrong range of versions")
		}
		if programIds[i] == 0 || programApps[i] >= appCount || row[12] > 2 || row[13]&^1 != 0 {
			return nil, errors.New("a program of the catalog is not valid")
		}
		if (row[13]&1 == 0) != nxZero(row[16:32]) {
			return nil, errors.New("a program of the catalog is not valid")
		}
	}

	archive := &NxArchive{MinorVersion: h.minor, Generation: h.generation, header: h}
	nextSnapshot, nextProgram, nextLocalized := uint32(0), uint32(0), uint32(0)
	type appKey struct {
		platform byte
		id       uint64
	}
	var previous *appKey
	identities := map[string]bool{}
	for i := uint32(0); i < appCount; i++ {
		row := apps[i*nxApplicationSize:]
		app := NxApplication{Id: le.Uint64(row), Platform: int(row[8]), MetadataSnapshot: -1, Names: map[string]string{}}
		key := appKey{row[8], app.Id}
		if app.Id == 0 || row[8] > 2 || row[9]&^3 != 0 || le.Uint16(row[10:]) != 0 || !nxZero(row[72:80]) {
			return nil, errors.New("a game of the catalog is not valid")
		}
		if previous != nil && (key.platform < previous.platform || key.platform == previous.platform && key.id <= previous.id) {
			return nil, errors.New("the games of the catalog are not in order or are repeated")
		}
		previous = &key
		firstProgram, programs := le.Uint32(row[12:]), le.Uint32(row[16:])
		if programs == 0 {
			if firstProgram != nxAbsent {
				return nil, errors.New("a game of the catalog has a wrong range of programs")
			}
		} else if firstProgram != nextProgram || uint64(firstProgram)+uint64(programs) > uint64(programCount) {
			return nil, errors.New("a game of the catalog has a wrong range of programs")
		} else {
			for p := firstProgram; p < firstProgram+programs; p++ {
				if programApps[p] != i {
					return nil, errors.New("a program of the catalog names another game")
				}
			}
			nextProgram += programs
		}
		firstSnapshot, count := le.Uint32(row[20:]), le.Uint32(row[24:])
		if count == 0 || firstSnapshot != nextSnapshot || uint64(firstSnapshot)+uint64(count) > uint64(snapshotCount) {
			return nil, errors.New("a game of the catalog has a wrong range of versions")
		}
		nextSnapshot += count
		if app.Title, err = c.text(le.Uint32(row[28:]), le.Uint32(row[32:])); err != nil {
			return nil, err
		}
		if app.Publisher, err = c.text(le.Uint32(row[36:]), le.Uint32(row[40:])); err != nil {
			return nil, err
		}
		if app.icon, err = media.get(le.Uint32(row[44:]), nxMediaPng, nxMediaJpeg); err != nil {
			return nil, err
		}
		if app.nacp, err = media.get(le.Uint32(row[56:]), nxMediaNacp); err != nil {
			return nil, err
		}
		firstLocalized, localizedEntries := le.Uint32(row[48:]), le.Uint32(row[52:])
		if localizedEntries > 0 && (firstLocalized != nextLocalized || uint64(firstLocalized)+uint64(localizedEntries) > uint64(localizedCount)) {
			return nil, errors.New("a game of the catalog has a wrong range of names")
		}
		nextLocalized += localizedEntries
		previousTag := ""
		for l := uint32(0); l < localizedEntries; l++ {
			entry := localized[(firstLocalized+l)*nxLocalizedSize:]
			tag, err := c.text(le.Uint32(entry), le.Uint32(entry[4:]))
			if err != nil || tag == "" || len(tag) > 63 || !nxLanguageTag(tag) || l > 0 && tag <= previousTag {
				return nil, errors.New("a language of the catalog is not valid")
			}
			previousTag = tag
			name, err := c.text(le.Uint32(entry[8:]), le.Uint32(entry[12:]))
			if err != nil {
				return nil, err
			}
			if le.Uint32(entry[16:]) == nxAbsent && name == "" || le.Uint32(entry[20:]) != 0 {
				return nil, errors.New("a name of the catalog is not valid")
			}
			if _, err := media.get(le.Uint32(entry[16:]), nxMediaPng, nxMediaJpeg); err != nil {
				return nil, err
			}
			if name != "" {
				app.Names[tag] = name
			}
		}
		hasMetadata := app.Title != "" || app.Publisher != "" || app.icon != nil || app.nacp != nil || localizedEntries > 0 || le.Uint32(row[64:]) > 0
		metadataSnapshot := le.Uint32(row[68:])
		if metadataSnapshot != nxAbsent {
			if metadataSnapshot < firstSnapshot || metadataSnapshot >= firstSnapshot+count || !hasMetadata {
				return nil, errors.New("the metadata of a game of the catalog belong to no version of it")
			}
			app.MetadataSnapshot = int(metadataSnapshot - firstSnapshot)
		} else if hasMetadata {
			return nil, errors.New("the metadata of a game of the catalog belong to no version of it")
		}
		hasBase, hasDlc := false, false
		for s := firstSnapshot; s < firstSnapshot+count; s++ {
			row := snapshots[s*nxSnapshotSize:]
			snapshot := NxSnapshot{Type: int(row[4]), Version: le.Uint32(row[8:]), LogicalSize: le.Uint64(row[16:]), root: le.Uint32(row[12:])}
			copy(snapshot.ContentId[:], row[32:48])
			copy(snapshot.Identity[:], row[56:88])
			flags := row[5]
			if snapshot.Type > 4 || flags&^3 != 0 || le.Uint16(row[6:]) != 0 {
				return nil, errors.New("a version of the catalog is not valid")
			}
			if (flags&1 != 0) == nxZero(row[88:120]) {
				return nil, errors.New("a version of the catalog says it has a build ID and does not, or the other way round")
			}
			program := le.Uint32(row[0:])
			if program == nxAbsent {
				if snapshot.Type != NxSnapshotDlc {
					return nil, errors.New("a version of the catalog that is not a DLC has no program")
				}
			} else if program >= programCount || programApps[program] != i {
				return nil, errors.New("a version of the catalog names a program of another game")
			} else if rg := programSnapshots[program]; s < rg[0] || s >= rg[0]+rg[1] {
				return nil, errors.New("a version of the catalog is out of the range of its program")
			} else {
				snapshot.ProgramId = programIds[program]
			}
			identity := string(row[56:88])
			if identities[identity] {
				return nil, errors.New("two versions of the catalog have the same identity")
			}
			identities[identity] = true
			if flags&2 != 0 {
				if snapshot.Type != NxSnapshotDlc || program != nxAbsent || le.Uint64(row[32:]) == 0 || !nxZero(row[40:48]) {
					return nil, errors.New("a DLC of the catalog has a title ID that is not valid")
				}
				snapshot.AddonTitleId = le.Uint64(row[32:])
			}
			if snapshot.DisplayVersion, err = c.text(le.Uint32(row[24:]), le.Uint32(row[28:])); err != nil {
				return nil, err
			}
			if s == metadataSnapshot && snapshot.Type == NxSnapshotDlc {
				return nil, errors.New("the metadata of a game of the catalog belong to a DLC")
			}
			hasBase = hasBase || snapshot.Type == NxSnapshotBase
			hasDlc = hasDlc || snapshot.Type == NxSnapshotDlc
			app.Snapshots = append(app.Snapshots, snapshot)
		}
		if hasBase != (row[9]&1 != 0) || hasDlc != (row[9]&2 != 0) {
			return nil, errors.New("a game of the catalog does not say right what it holds")
		}
		if app.MetadataSnapshot >= 0 {
			app.Snapshots[app.MetadataSnapshot].nacp = app.nacp
		}
		archive.Applications = append(archive.Applications, app)
	}
	if nextSnapshot != snapshotCount || nextProgram != programCount || nextLocalized != localizedCount {
		return nil, errors.New("the tables of the catalog do not match its games")
	}

	// the extensions: the history gives the NACP of the other versions; an unknown required one
	// cannot be read
	seen := map[[3]uint32]bool{}
	for e := uint32(0); e < extensionCount; e++ {
		row := extensions[e*nxExtensionSize:]
		kind, flags, owner, index := le.Uint32(row), le.Uint32(row[4:]), le.Uint16(row[8:]), le.Uint32(row[12:])
		offset, size := le.Uint64(row[16:]), le.Uint64(row[24:])
		if flags&^1 != 0 || owner > 3 || le.Uint16(row[10:]) != 0 || offset > uint64(len(data)) || size > uint64(len(data))-offset {
			return nil, errors.New("an extension of the catalog is not valid")
		}
		key := [3]uint32{kind, uint32(owner), index}
		if seen[key] {
			return nil, errors.New("an extension of the catalog is repeated")
		}
		seen[key] = true
		switch kind {
		case nxExtHistory:
			if owner != 1 || index >= appCount {
				return nil, errors.New("an extension of the catalog is not valid")
			}
			if err := readNxHistory(data[offset:offset+size], &archive.Applications[index], snapshots, apps[index*nxApplicationSize:], media, c); err != nil {
				return nil, err
			}
		case nxExtNativeMetadata, nxExtSourceResources:
		default:
			if flags&1 != 0 {
				return nil, fmt.Errorf("the file needs the extension %#x of the .nx format, which this version does not know", kind)
			}
		}
	}
	if (h.minor == 3) != seenKind(seen, nxExtSourceResources) {
		return nil, errors.New("the version of the .nx file does not match its source resources")
	}
	return archive, nil
}

// nxCheckSha checks the SHA-256 of ranges of the file read one after the other, in pieces.
func nxCheckSha(r io.ReaderAt, want []byte, what string, ranges ...[2]uint64) error {
	hash := sha256.New()
	buffer := make([]byte, 1<<20)
	for _, rg := range ranges {
		offset, size := rg[0], rg[1]
		for size > 0 {
			n := uint64(len(buffer))
			if size < n {
				n = size
			}
			piece, err := nxReadAt(r, offset, n)
			if err != nil {
				return err
			}
			hash.Write(piece)
			offset, size = offset+n, size-n
		}
	}
	if !bytes.Equal(hash.Sum(nil), want) {
		return errors.New(what + " is damaged: its SHA-256 does not match")
	}
	return nil
}

func seenKind(seen map[[3]uint32]bool, kind uint32) bool {
	for key := range seen {
		if key[0] == kind {
			return true
		}
	}
	return false
}

// readNxHistory gives each version of a game the NACP of its own the history keeps.
func readNxHistory(payload []byte, app *NxApplication, snapshots []byte, appRow []byte, media *nxMedia, c *nxCatalog) error {
	le := binary.LittleEndian
	if len(payload) < 24 || !bytes.Equal(payload[0:4], []byte("NXSM")) || le.Uint16(payload[4:]) != 1 || le.Uint16(payload[6:]) != 24 {
		return errors.New("the history of a game of the catalog is not valid")
	}
	records, localized, opaque := le.Uint32(payload[8:]), le.Uint32(payload[12:]), le.Uint32(payload[16:])
	if records < 1 || records > 65536 || localized > 16384 ||
		uint64(len(payload)) != 24+uint64(records)*48+uint64(localized)*24+uint64(opaque)*4 {
		return errors.New("the history of a game of the catalog is not valid")
	}
	first := le.Uint32(appRow[20:])
	for i := uint32(0); i < records; i++ {
		row := payload[24+i*48:]
		index := le.Uint32(row)
		if index < first || index >= first+uint32(len(app.Snapshots)) || int(index-first) == app.MetadataSnapshot {
			return errors.New("the history of a game of the catalog names a version of another game")
		}
		if _, err := c.text(le.Uint32(row[4:]), le.Uint32(row[8:])); err != nil {
			return err
		}
		if _, err := media.get(le.Uint32(row[20:]), nxMediaPng, nxMediaJpeg); err != nil {
			return err
		}
		nacp, err := media.get(le.Uint32(row[24:]), nxMediaNacp)
		if err != nil {
			return err
		}
		if snapshots[index*nxSnapshotSize+4] == NxSnapshotDlc {
			return errors.New("the history of a game of the catalog gives metadata to a DLC")
		}
		app.Snapshots[index-first].nacp = nacp
	}
	return nil
}

// nxMedia reads the icons and the NACP of the media region when asked for them.
type nxMedia struct {
	r       io.ReaderAt
	offset  uint64
	size    uint64
	records []byte
}

func readNxMedia(r io.ReaderAt, h *nxHeader) (*nxMedia, error) {
	m := &nxMedia{r: r}
	if h.mediaOffset == 0 {
		return m, nil
	}
	le := binary.LittleEndian
	header, err := nxReadAt(r, h.mediaOffset, nxMediaHeader)
	if err != nil {
		return nil, err
	}
	count := le.Uint32(header[16:])
	if !bytes.Equal(header[0:4], []byte("NXMD")) || le.Uint16(header[4:]) != 1 || le.Uint16(header[6:]) != nxMediaHeader ||
		le.Uint64(header[8:]) != h.mediaSize || count < 1 || count > 65535 || le.Uint16(header[20:]) != nxMediaRecord ||
		le.Uint32(header[24:]) != nxMediaHeader || uint64(nxMediaHeader)+uint64(count)*nxMediaRecord > h.mediaSize {
		return nil, errors.New("the media of the .nx file are not valid")
	}
	m.offset, m.size = h.mediaOffset, h.mediaSize
	if m.records, err = nxReadAt(r, h.mediaOffset+nxMediaHeader, uint64(count)*nxMediaRecord); err != nil {
		return nil, err
	}
	return m, nil
}

// get reads a media record of one of the types given, checked with its SHA-256; nil when absent.
func (m *nxMedia) get(index uint32, types ...uint16) ([]byte, error) {
	if index == nxAbsent {
		return nil, nil
	}
	if uint64(index)*nxMediaRecord >= uint64(len(m.records)) {
		return nil, errors.New("the catalog names media the file does not have")
	}
	le := binary.LittleEndian
	row := m.records[index*nxMediaRecord:]
	kind := le.Uint16(row)
	known := false
	for _, t := range types {
		known = known || kind == t
	}
	stored, logical, offset := le.Uint64(row[4:]), le.Uint64(row[12:]), le.Uint64(row[20:])
	if !known {
		return nil, errors.New("the catalog names media of the wrong type")
	}
	if row[2] != 0 || row[3] != 0 || stored == 0 || stored > nxMaxMedia || logical != stored || offset%16 != 0 ||
		offset > m.size || stored > m.size-offset {
		return nil, errors.New("the media of the .nx file are not valid")
	}
	data, err := nxReadAt(m.r, m.offset+offset, stored)
	if err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(data); !bytes.Equal(sum[:], row[28:60]) {
		return nil, errors.New("the media of the .nx file are damaged: a SHA-256 does not match")
	}
	return data, nil
}

// nxLanguageTag tells a lowercase ASCII BCP 47 tag: subtags of letters and digits, none empty.
func nxLanguageTag(tag string) bool {
	for _, part := range strings.Split(tag, "-") {
		if part == "" || len(part) > 8 {
			return false
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
				return false
			}
		}
	}
	return true
}

// TitleId is the title ID of a version as the library knows it: the game for the game (a game
// of several programs is one title), with the update bit for an update, the ID of the DLC for a
// DLC; "" when the file does not give it (a DLC from an NSP, whose ID is in its CNMT).
func (app *NxApplication) TitleId(snapshot NxSnapshot) string {
	switch snapshot.Type {
	case NxSnapshotBase:
		return fmt.Sprintf("%016x", app.Id)
	case NxSnapshotUpdate, NxSnapshotStandaloneUpdate:
		id := app.Id
		if id&0x800 == 0 {
			id = id&^0xFFF | 0x800
		}
		return fmt.Sprintf("%016x", id)
	case NxSnapshotDlc:
		if snapshot.AddonTitleId != 0 {
			return fmt.Sprintf("%016x", snapshot.AddonTitleId)
		}
	}
	return ""
}

// Nacp is the NACP of a version: its own when the file keeps it; else, for the game, one made
// of the names and the publisher of the catalog with its own display version (the NACP of the
// catalog may be the one of an update, whose version would be wrong for the game).
func (app *NxApplication) Nacp(snapshot NxSnapshot) *Nacp {
	if raw := snapshot.nacp; len(raw) >= 0x3080 {
		if nacp, err := readNacp(raw, RomfsHeader{}, RomfsFileEntry{size: uint64(len(raw))}); err == nil {
			return &nacp
		}
	}
	if snapshot.Type != NxSnapshotBase || app.Title == "" && len(app.Names) == 0 {
		return nil
	}
	names := map[Language]string{}
	for tag, name := range app.Names {
		if language, ok := nupLanguage(tag); ok {
			names[language] = nupName([]byte(name))
		}
	}
	fallback := nupName([]byte(app.Title))
	result := &Nacp{TitleName: map[string]NacpTitle{}, DisplayVersion: nupText([]byte(snapshot.DisplayVersion), 0x0F),
		AttributesRead: true, NamesRead: true, PublishersRead: true, CompressedNamesRead: true}
	publisher := nupText([]byte(app.Publisher), 0x100)
	for i := range nupLanguages {
		language := Language(i)
		name, ok := names[language]
		if !ok || name == "" {
			name = fallback
		}
		result.TitleName[language.String()] = NacpTitle{Language: language, Title: name, Publisher: publisher}
	}
	return result
}

// Metadata is what a .nx file holds as the readers of NSP and XCI files tell it: each game, its
// newest update and its DLC by their title IDs. A DLC whose ID the file does not give is left out.
func (archive *NxArchive) Metadata() map[string]*ContentMetaAttributes {
	result := map[string]*ContentMetaAttributes{}
	for a := range archive.Applications {
		app := &archive.Applications[a]
		for _, snapshot := range app.Snapshots {
			id := app.TitleId(snapshot)
			if id == "" {
				continue
			}
			meta := &ContentMetaAttributes{TitleId: id, Version: int(snapshot.Version), Contents: map[string]Content{},
				Ncap: app.Nacp(snapshot)}
			switch snapshot.Type {
			case NxSnapshotBase:
				meta.Type = "BASE"
			case NxSnapshotUpdate, NxSnapshotStandaloneUpdate:
				meta.Type = "UPD"
			case NxSnapshotDlc:
				meta.Type = "DLC"
			default:
				continue
			}
			if older, ok := result[id]; ok && older.Version >= meta.Version {
				continue
			}
			result[id] = meta
		}
	}
	return result
}

// ReadNxMetadata reads what a .nx file holds (see NxArchive.Metadata).
func ReadNxMetadata(path string) (map[string]*ContentMetaAttributes, error) {
	archive, err := ReadNx(path)
	if err != nil {
		return nil, err
	}
	return archive.Metadata(), nil
}

// Icon is the icon of the first game of the file that carries one.
func (archive *NxArchive) Icon() ([]byte, error) {
	for _, app := range archive.Applications {
		if len(app.icon) > 0 {
			return app.icon, nil
		}
	}
	return nil, errors.New("no icon in the .nx file")
}
