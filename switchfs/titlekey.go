package switchfs

import (
	"encoding/hex"
	"os"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// MissingTitleKey tells whether the contents of an NSP or NSZ cannot be decrypted: a content
// with a rights ID whose title key is neither in a common ticket of the file nor in
// title.keys. Such a game cannot be compressed nor checked whole, and installs only with a
// ticket of the console. It reads the headers of the NCA files and the tickets alone, and
// returns the rights ID missing, or "". Without prod.keys nothing can be told: "".
func MissingTitleKey(path string) (string, error) {
	keys, _ := settings.SwitchKeys()
	if keys == nil || keys.GetKey("header_key") == "" {
		return "", nil
	}
	input, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer input.Close()
	pkg, err := openGamePackage(input)
	if err != nil {
		return "", err
	}
	titleKeys, err := readTickets(input, pkg.files)
	if err != nil {
		return "", err
	}
	encHeader := make([]byte, 0xC00)
	for _, file := range pkg.files {
		lower := strings.ToLower(file.name)
		if !strings.HasSuffix(lower, ".nca") && !strings.HasSuffix(lower, ".ncz") || file.size < int64(len(encHeader)) {
			continue
		}
		if err := readAtFull(input, encHeader, file.offset); err != nil {
			return "", err
		}
		header, err := DecryptNcaHeader(keys.GetKey("header_key"), encHeader)
		if err != nil || !header.HasRightsId() {
			continue
		}
		rightsId := hex.EncodeToString(header.rightsId)
		if _, ok := titleKeys[rightsId]; ok {
			continue
		}
		if _, found := keys.TitleKey(rightsId); found {
			continue
		}
		return rightsId, nil
	}
	return "", nil
}
