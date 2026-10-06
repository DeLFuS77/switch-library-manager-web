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
// XCI or XCZ. It needs the keys of the console, like reading the names of the game.
func ExtractIcon(path string) ([]byte, error) {
	file, err := OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	pkg, err := openGamePackage(file)
	if err != nil {
		return nil, err
	}
	return extractPackageIcon(file, pkg)
}

func extractPackageIcon(file io.ReaderAt, pkg *gamePackage) ([]byte, error) {
	controlIds := []string{}
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
		// DLC have no icon of their own; the game comes first, then its update
		if control, ok := cnmt.Contents["Control"]; ok && cnmt.Type != "DLC" {
			if cnmt.Type == "BASE" {
				controlIds = append([]string{control.ID}, controlIds...)
			} else {
				controlIds = append(controlIds, control.ID)
			}
		}
	}
	if len(controlIds) == 0 {
		return nil, errors.New("no control content in the file")
	}

	var lastErr error
	for _, id := range controlIds {
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
