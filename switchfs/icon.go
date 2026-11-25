package switchfs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"strings"
)

// the control NCA of a game holds its NACP and icons; it is small, so a compressed one is
// decompressed in memory
const maxControlNcaSize = 64 << 20

// icons in order of preference; the others are used when none of these is present
var preferredIcons = []string{"icon_AmericanEnglish.dat", "icon_BritishEnglish.dat", "icon_Spanish.dat", "icon_French.dat", "icon_German.dat", "icon_Italian.dat", "icon_Japanese.dat"}

// ExtractIcon returns the official icon (a JPEG image) stored in a game file: NSP, NSZ,
// XCI or XCZ. It needs the keys of the console, like reading the names of the game. A NUP
// needs no keys: its icon is the one it carries, when it was packed with one. The icon of the
// game comes first; the one of its update only when the file holds no game, or a game without
// one.
func ExtractIcon(path string) ([]byte, error) {
	return extractIcon(path, false)
}

// ExtractUpdateIcon is ExtractIcon with the icon of the newest update of the file first, the one
// the console shows once the update is installed: a file that holds a game and its update, as a
// pack of both, gives the icon of the update, and the one of the game when it holds no update or
// its update has none. A NUP gives the icon it carries, as with ExtractIcon.
func ExtractUpdateIcon(path string) ([]byte, error) {
	return extractIcon(path, true)
}

func extractIcon(path string, newestUpdate bool) ([]byte, error) {
	if IsNupFile(path) {
		container, err := ReadNup(path)
		if err != nil {
			return nil, err
		}
		return container.Icon()
	}
	if IsNxFile(path) {
		archive, err := ReadNx(path)
		if err != nil {
			return nil, err
		}
		return archive.Icon()
	}
	file, err := OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	pkg, err := openGamePackage(file)
	if err != nil {
		return nil, err
	}
	return extractPackageIcon(file, pkg, newestUpdate)
}

// controlContent is the control content of a game or an update of a file, which holds its icons.
type controlContent struct {
	id      string
	update  bool
	version int
}

// iconOrder is the order the control contents of a file are tried in for its icon: the games
// first (the last one first, in a file that holds several), then the updates as they come; with
// newestUpdate, the updates first, the newest first, then the games.
func iconOrder(controls []controlContent, newestUpdate bool) []string {
	games, updates := []string{}, []controlContent{}
	for _, control := range controls {
		if control.update {
			updates = append(updates, control)
		} else {
			games = append([]string{control.id}, games...)
		}
	}
	ids := []string{}
	if newestUpdate {
		sort.SliceStable(updates, func(i, j int) bool { return updates[i].version > updates[j].version })
	} else {
		ids = append(ids, games...)
	}
	for _, update := range updates {
		ids = append(ids, update.id)
	}
	if newestUpdate {
		ids = append(ids, games...)
	}
	return ids
}

func extractPackageIcon(file io.ReaderAt, pkg *gamePackage, newestUpdate bool) ([]byte, error) {
	controls := []controlContent{}
	for _, packed := range pkg.files {
		if !strings.HasSuffix(strings.ToLower(packed.name), ".cnmt.nca") {
			continue
		}
		_, section, err := openMetaNcaDataSection(file, packed.offset)
		if err != nil {
			return nil, err
		}
		cnmtPfs0, err := readPfs0(bytes.NewReader(section), 0)
		if err != nil {
			return nil, err
		}
		cnmt, err := readBinaryCnmt(cnmtPfs0, section)
		if err != nil {
			return nil, err
		}
		// DLC have no icon of their own
		if control, ok := cnmt.Contents["Control"]; ok && cnmt.Type != "DLC" {
			controls = append(controls, controlContent{id: control.ID, update: cnmt.Type != "BASE", version: cnmt.Version})
		}
	}
	if len(controls) == 0 {
		return nil, errors.New("no control content in the file")
	}

	var lastErr error
	for _, id := range iconOrder(controls, newestUpdate) {
		for _, packed := range pkg.files {
			if !strings.Contains(strings.ToLower(packed.name), strings.ToLower(id)) {
				continue
			}
			icon, err := readControlIcon(file, packed)
			if err == nil {
				return icon, nil
			}
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("control content not found in the file")
	}
	return nil, lastErr
}

// readControlIcon reads the icon from a control NCA, compressed (NCZ) or not.
func readControlIcon(file io.ReaderAt, packed packedFile) ([]byte, error) {
	var reader io.ReaderAt = file
	offset := packed.offset
	if strings.HasSuffix(strings.ToLower(packed.name), ".ncz") {
		if packed.size > maxControlNcaSize {
			return nil, errors.New("control content is too big")
		}
		var nca bytes.Buffer
		if err := decompressNcz(context.Background(), &nca, file, packed.offset, packed.size, nil); err != nil {
			return nil, err
		}
		reader, offset = bytes.NewReader(nca.Bytes()), 0
	}

	fsHeader, section, err := openMetaNcaDataSection(reader, offset)
	if err != nil {
		return nil, err
	}
	if fsHeader.fsType != 0 {
		return nil, errors.New("control content is not a RomFS")
	}
	header, err := readRomfsHeader(section)
	if err != nil {
		return nil, err
	}
	entries, err := readRomfsFileEntry(section, header)
	if err != nil {
		return nil, err
	}

	names := []string{}
	for _, name := range preferredIcons {
		if _, ok := entries[name]; ok {
			names = append(names, name)
		}
	}
	others := []string{}
	for name := range entries {
		if strings.HasPrefix(name, "icon_") && strings.HasSuffix(name, ".dat") {
			others = append(others, name)
		}
	}
	sort.Strings(others)
	names = append(names, others...)

	for _, name := range names {
		entry := entries[name]
		if header.DataOffset > uint64(len(section)) || entry.offset > uint64(len(section))-header.DataOffset {
			continue
		}
		start := header.DataOffset + entry.offset
		if entry.size == 0 || entry.size > uint64(len(section))-start {
			continue
		}
		icon := section[start : start+entry.size]
		// icons are JPEG images
		if len(icon) > 3 && icon[0] == 0xFF && icon[1] == 0xD8 {
			return append([]byte(nil), icon...), nil
		}
	}
	return nil, errors.New("no icon in the control content")
}
