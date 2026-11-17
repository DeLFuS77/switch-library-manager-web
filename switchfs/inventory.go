package switchfs

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// PackageInventory is what an NSP, NSZ, XCI or XCZ holds, as it tells where it comes from: the
// partitions of a game card, the tickets and certificates, the header of each NCA (how it is
// distributed, its rights ID, whether Nintendo signed it) and the contents each CNMT lists.
type PackageInventory struct {
	// an XCI or XCZ, with the partitions of its root, in order
	Card       bool
	Partitions []string
	// the rights IDs of the tickets it carries, and how many certificates
	Tickets      []string
	Certificates int
	Ncas         []NcaFacts
	Titles       []TitleContents
}

// NcaFacts is what the header of an NCA (or NCZ) tells of where it comes from.
type NcaFacts struct {
	Name string
	// its content ID, from its name; "" when the name is not one
	ID string
	// 0 Program, 1 Meta, 2 Control, 3 Manual, 4 Data, 5 PublicData
	ContentType byte
	// distributed on a game card, not as a download
	GameCard bool
	// its rights ID in hexadecimal, "" when it has none (standard crypto)
	RightsID string
	// its header carries the signature of Nintendo; SignatureKnown is false when the key it
	// names is not one NXPress or SLMW knows
	Signed         bool
	SignatureKnown bool
}

// TitleContents is a title as its CNMT lists it: the contents it is made of.
type TitleContents struct {
	TitleID  string
	Type     string
	Version  int
	Contents []ListedContent
}

// ListedContent is a content a CNMT lists: its NCA ID and its type (1 Program, 2 Control, 6 a
// delta fragment, as in the CNMT).
type ListedContent struct {
	ID   string
	Type byte
}

// ReadInventory reads what a game file holds: only headers and the CNMT, never the data of the
// game. The keys are needed for the headers of the NCA files.
func ReadInventory(path string) (PackageInventory, error) {
	inventory := PackageInventory{}
	keys, _ := settings.SwitchKeys()
	if keys == nil || keys.GetKey("header_key") == "" {
		return inventory, &MissingKeyError{KeyName: "header_key"}
	}
	reader, err := OpenFile(path)
	if err != nil {
		return inventory, err
	}
	defer reader.Close()
	pkg, err := openGamePackage(reader)
	if err != nil {
		return inventory, err
	}
	inventory.Card = pkg.xci
	inventory.Partitions = append([]string(nil), pkg.partitions...)
	if rights, err := readTickets(reader, pkg.files); err == nil {
		for id := range rights {
			inventory.Tickets = append(inventory.Tickets, id)
		}
	}
	for _, file := range pkg.files {
		name := strings.ToLower(file.name)
		switch {
		case strings.HasSuffix(name, ".cert"):
			inventory.Certificates++
			continue
		case !strings.HasSuffix(name, ".nca") && !strings.HasSuffix(name, ".ncz"):
			continue
		case file.size < 0xC00:
			continue
		}
		encrypted := make([]byte, 0xC00)
		if err := readAtFull(reader, encrypted, file.offset); err != nil {
			return inventory, err
		}
		header, err := DecryptNcaHeader(keys.GetKey("header_key"), encrypted)
		if err != nil {
			return inventory, err
		}
		facts := NcaFacts{Name: file.name, ID: contentId(file.name), ContentType: header.contentType, GameCard: header.distribution == 1}
		if header.HasRightsId() {
			facts.RightsID = hex.EncodeToString(header.rightsId)
		}
		facts.Signed, facts.SignatureKnown = headerSigned(header.headerBytes)
		inventory.Ncas = append(inventory.Ncas, facts)
		if !strings.Contains(name, ".cnmt.") {
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
		if err != nil {
			continue
		}
		title := TitleContents{TitleID: attributes.TitleId, Type: attributes.Type, Version: attributes.Version}
		for _, entry := range cnmtEntries(pfs0, section) {
			title.Contents = append(title.Contents, ListedContent{ID: entry.id, Type: entry.kind})
		}
		inventory.Titles = append(inventory.Titles, title)
	}
	return inventory, nil
}

// npdmReadLimit is the most main.npdm is read of: a real one is a few KB.
const npdmReadLimit = 1 << 20

// errNoNpdm is given when the ExeFS of the program has no main.npdm.
var errNoNpdm = errors.New("the ExeFS has no main.npdm")

// ReadNpdm gives main.npdm, the permissions of the program of a title (a game or an update),
// from the ExeFS of its program NCA in an NSP, NSZ, XCI or XCZ. The keys are needed.
func ReadNpdm(path string, titleId string) ([]byte, error) {
	reader, err := OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	pkg, err := openGamePackage(reader)
	if err != nil {
		return nil, err
	}
	program, err := programOf(reader, pkg, titleId)
	if err != nil {
		return nil, err
	}
	keys, _ := settings.SwitchKeys()
	if keys == nil || keys.GetKey("header_key") == "" {
		return nil, &MissingKeyError{KeyName: "header_key"}
	}
	encHeader := make([]byte, 0xC00)
	if err := readAtFull(reader, encHeader, program.offset); err != nil {
		return nil, err
	}
	header, err := DecryptNcaHeader(keys.GetKey("header_key"), encHeader)
	if err != nil {
		return nil, err
	}
	entry := getFsEntry(header, 0)
	fsh, err := getFsHeader(header, 0)
	if err != nil || entry.Size == 0 {
		return nil, errors.New("the program has no ExeFS")
	}
	hashInfo, err := fsh.getHashInfo()
	if err != nil {
		return nil, err
	}
	if hashInfo.pfs0HeaderOffset >= entry.Size {
		return nil, errors.New("the program has no ExeFS")
	}
	start := uint64(entry.StartOffset) + hashInfo.pfs0HeaderOffset
	room := uint64(entry.Size) - hashInfo.pfs0HeaderOffset
	// the header of the ExeFS first, then main.npdm where it says, which may come after main
	head, err := readPlain(reader, pkg, program, start, min(uint64(0x4000), room))
	if err != nil {
		return nil, err
	}
	exefs, err := readPfs0(bytes.NewReader(head), 0)
	if err != nil {
		return nil, err
	}
	for _, file := range exefs.Files {
		if file.Name != "main.npdm" {
			continue
		}
		if file.Size == 0 || file.Size > npdmReadLimit || file.StartOffset > room || file.Size > room-file.StartOffset {
			return nil, errors.New("main.npdm is beyond the ExeFS")
		}
		data, err := readPlain(reader, pkg, program, start+file.StartOffset, file.Size)
		if err != nil {
			return nil, err
		}
		if uint64(len(data)) != file.Size {
			return nil, errors.New("main.npdm is cut short")
		}
		return data, nil
	}
	return nil, errNoNpdm
}
