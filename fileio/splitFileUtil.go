package fileio

import (
	"errors"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
	"os"
)

func ReadSplitFileMetadata(filePath string) (map[string]*switchfs.ContentMetaAttributes, error) {
	// a NUP or a .nx is told by the name of the folder of its parts
	if switchfs.IsNupFile(filePath) {
		return switchfs.ReadNupMetadata(filePath)
	}
	if switchfs.IsNxFile(filePath) {
		return switchfs.ReadNxMetadata(filePath)
	}
	//check if this is a NS* or XC* file
	_, err := switchfs.ReadPfs0File(filePath)
	isXCI := false
	if err != nil {
		_, err = readXciHeader(filePath)
		if err != nil {
			return nil, errors.New("split file is not an XCI/XCZ, NSP/NSZ, NUP or .nx")
		}
		isXCI = true
	}

	if isXCI {
		return switchfs.ReadXciMetadata(filePath)
	} else {
		return switchfs.ReadNspMetadata(filePath)
	}
}

func readXciHeader(filePath string) ([]byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}

	defer file.Close()

	header := make([]byte, 0x200)
	_, err = file.Read(header)
	if err != nil {
		return nil, err
	}

	if string(header[0x100:0x104]) != "HEAD" {
		return nil, errors.New("not an XCI/XCZ file")
	}
	return header, nil
}
