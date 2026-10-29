package switchfs

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The build ID of a game is the ID of the program of one version: cheats are made for it.
// It is in the header of the NSO "main", in the ExeFS (the first section) of the program NCA
// of the title: the game, or its update, which carries the whole ExeFS. The app reads it
// when the database of versions does not know the version, so its cheats can be found.

// nsoBuildIdOffset is where the module ID of an NSO starts; cheats use its first 8 bytes.
const nsoBuildIdOffset = 0x40

// exefsReadLimit is how much of the ExeFS is read at most to find the header of "main".
const exefsReadLimit = 16 << 20

// errNoBuildId is given when the package has no program for the title.
var errNoBuildId = errors.New("no program for the title")

// ReadBuildID gives the build ID of the program of a title (a game or an update) in an NSP,
// NSZ, XCI or XCZ, as 16 upper case hexadecimal digits. The keys are needed.
func ReadBuildID(path string, titleId string) (string, error) {
	reader, err := OpenFile(path)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	pkg, err := openGamePackage(reader)
	if err != nil {
		return "", err
	}
	program, err := programOf(reader, pkg, titleId)
	if err != nil {
		return "", err
	}
	keys, _ := settings.SwitchKeys()
	if keys == nil || keys.GetKey("header_key") == "" {
		return "", errors.New("prod.keys is needed to read the build ID")
	}
	encHeader := make([]byte, 0xC00)
	if err := readAtFull(reader, encHeader, program.offset); err != nil {
		return "", err
	}
	header, err := DecryptNcaHeader(keys.GetKey("header_key"), encHeader)
	if err != nil {
		return "", err
	}
	entry := getFsEntry(header, 0)
	fsh, err := getFsHeader(header, 0)
	if err != nil || entry.Size == 0 {
		return "", errors.New("the program has no ExeFS")
	}
	hashInfo, err := fsh.getHashInfo()
	if err != nil {
		return "", err
	}
	if hashInfo.pfs0HeaderOffset >= entry.Size {
		return "", errors.New("the program has no ExeFS")
	}
	start := uint64(entry.StartOffset) + hashInfo.pfs0HeaderOffset
	exefs, err := readPlain(reader, pkg, program, start, min(uint64(exefsReadLimit), uint64(entry.Size)-hashInfo.pfs0HeaderOffset))
	if err != nil {
		return "", err
	}
	return buildIdOfExefs(exefs)
}

// programOf is the program NCA (or NCZ) a CNMT of the title lists.
func programOf(reader io.ReaderAt, pkg *gamePackage, titleId string) (packedFile, error) {
	for _, file := range pkg.files {
		if !strings.HasSuffix(strings.ToLower(file.name), ".cnmt.nca") {
			continue
		}
		_, section, err := openMetaNcaDataSection(reader, file.offset)
		if err != nil {
			continue
		}
		pfs0, err := readPfs0(bytes.NewReader(section), 0)
		if err != nil {
			continue
		}
		attributes, err := readBinaryCnmt(pfs0, section)
		if err != nil || !strings.EqualFold(attributes.TitleId, titleId) {
			continue
		}
		for _, entry := range cnmtEntries(pfs0, section) {
			if entry.kind != 1 {
				continue
			}
			for _, candidate := range pkg.files {
				if contentId(candidate.name) == entry.id && !strings.Contains(strings.ToLower(candidate.name), ".cnmt.") {
					return candidate, nil
				}
			}
		}
	}
	return packedFile{}, errNoBuildId
}

// readPlain reads the decrypted bytes of an NCA (or NCZ) from position on, length at most.
func readPlain(reader io.ReaderAt, pkg *gamePackage, file packedFile, position uint64, length uint64) ([]byte, error) {
	if strings.HasSuffix(strings.ToLower(file.name), ".ncz") {
		return readNczPlain(reader, file, position, length)
	}
	titleKeys, err := readTickets(reader, pkg.files)
	if err != nil {
		return nil, err
	}
	plan, _, err := planNcz(reader, file.offset, uint64(file.size), titleKeys)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, errNoBuildId
	}
	data := make([]byte, min(length, uint64(file.size)-position))
	if err := (nczPlainReader{input: reader, offset: file.offset, sections: plan.sections}).read(data, position); err != nil {
		return nil, err
	}
	return data, nil
}

// readNczPlain decompresses an NCZ only as far as needed for the bytes from position on.
func readNczPlain(reader io.ReaderAt, file packedFile, position uint64, length uint64) ([]byte, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	data := make([]byte, 0, length)
	end := position + length
	err := decompressNczPlain(ctx, io.Discard, reader, file.offset, file.size, nil, func(offset int64, plain []byte) {
		from, to := uint64(offset), uint64(offset)+uint64(len(plain))
		if to <= position || from >= end || uint64(len(data)) >= length {
			if from >= end || uint64(len(data)) >= length {
				cancel()
			}
			return
		}
		lo := position + uint64(len(data))
		if from > lo {
			lo = from
		}
		hi := min(to, end)
		if lo < hi && lo == position+uint64(len(data)) {
			data = append(data, plain[lo-from:hi-from]...)
		}
		if uint64(len(data)) >= length {
			cancel()
		}
	})
	if len(data) > 0 && (err == nil || errors.Is(err, context.Canceled)) {
		return data, nil
	}
	if err == nil {
		err = errors.New("the ExeFS was not found in the NCZ")
	}
	return nil, err
}

// buildIdOfExefs reads the build ID in the header of "main" in an ExeFS (a PFS0).
func buildIdOfExefs(exefs []byte) (string, error) {
	pfs0, err := readPfs0(bytes.NewReader(exefs), 0)
	if err != nil {
		return "", err
	}
	for _, file := range pfs0.Files {
		if file.Name != "main" {
			continue
		}
		// told apart without adding to the offset: a damaged one near its largest overflowed
		start := file.StartOffset
		if start > uint64(len(exefs)) || uint64(len(exefs))-start < nsoBuildIdOffset+8 {
			return "", errors.New("the header of main is beyond what was read")
		}
		if string(exefs[start:start+4]) != "NSO0" {
			return "", errors.New("main is not an NSO")
		}
		return strings.ToUpper(hex.EncodeToString(exefs[start+nsoBuildIdOffset : start+nsoBuildIdOffset+8])), nil
	}
	return "", errors.New("the ExeFS has no main")
}
