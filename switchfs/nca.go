package switchfs

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs/_crypto"
)

const (
	NcaSectionType_Code = iota
	NcaSectionType_Data
	NcaSectionType_Logo
)
const (
	NcaContentType_Program = iota
	NcaContentType_Meta
	NcaContentType_Control
	NcaContentType_Manual
	NcaContentType_Data
	NcaContentType_PublicData
)

func openMetaNcaDataSection(reader io.ReaderAt, ncaOffset int64) (*fsHeader, []byte, error) {
	//read the NCA headerBytes
	encNcaHeader := make([]byte, 0xC00)
	n, err := reader.ReadAt(encNcaHeader, ncaOffset)

	if err != nil {
		return nil, nil, errors.New("failed to read NCA header " + err.Error())
	}
	if n != 0xC00 {
		return nil, nil, errors.New("failed to read NCA header")
	}

	keys, err := settings.SwitchKeys()
	if err != nil {
		return nil, nil, err
	}
	if keys == nil {
		return nil, nil, errors.New("switch keys are not initialized")
	}
	headerKey := keys.GetKey("header_key")
	if headerKey == "" {
		return nil, nil, errors.New("missing key - header_key")
	}
	ncaHeader, err := DecryptNcaHeader(headerKey, encNcaHeader)
	if err != nil {
		return nil, nil, err
	}

	if ncaHeader.HasRightsId() {
		//fail - need title keys
		return nil, nil, errors.New("non standard encryption is not supported")
	}

	/*if ncaHeader.contentType != NcaContentType_Meta {
		return nil, errors.New("not a meta NCA")
	}*/

	dataSectionIndex := 0

	fsHeader, err := getFsHeader(ncaHeader, dataSectionIndex)
	if err != nil {
		return nil, nil, err
	}

	entry := getFsEntry(ncaHeader, dataSectionIndex)

	// the sections read whole here are those of the small control and meta NCAs
	if entry.Size == 0 || entry.Size > maxReadSectionSize {
		return nil, nil, errors.New("empty or invalid section")
	}
	if entry.StartOffset > uint64(^uint64(0)>>1) || ncaOffset > int64(^uint64(0)>>1)-int64(entry.StartOffset)-int64(entry.Size) {
		return nil, nil, errors.New("NCA section offset overflows")
	}

	entryOffset := ncaOffset + int64(entry.StartOffset)
	// a damaged size must not take gigabytes before the read fails: the section must be in
	// the file
	if size, ok := readerSize(reader); ok {
		if entryOffset > size || int64(entry.Size) > size-entryOffset {
			return nil, nil, fmt.Errorf("the NCA section ends past the end of the file: %w", io.ErrUnexpectedEOF)
		}
	} else if err := readAtFull(reader, make([]byte, 1), entryOffset+int64(entry.Size)-1); err != nil {
		return nil, nil, err
	}
	encodedEntryContent := make([]byte, entry.Size)
	_, err = reader.ReadAt(encodedEntryContent, entryOffset)
	if err != nil {
		return nil, nil, err
	}
	/*if fsHeader.hashType != 2 { //Sha256 (FS_TYPE_PFS0)
		return nil, errors.New("non FS_TYPE_PFS0")
	}*/
	var decoded []byte
	switch fsHeader.encType {
	case 1:
		decoded = encodedEntryContent
	case 3:
		decoded, err = decryptAesCtr(ncaHeader, fsHeader, entry.StartOffset, entry.Size, encodedEntryContent)
		if err != nil {
			return nil, nil, err
		}
	default:
		return nil, nil, fmt.Errorf("non supported encryption type [encryption type:%d]", fsHeader.encType)
	}
	hashInfo, err := fsHeader.getHashInfo()
	if err != nil {
		return nil, nil, err
	}
	if hashInfo.pfs0HeaderOffset > uint64(len(decoded)) {
		return nil, nil, errors.New("the data of the NCA section starts past its end")
	}

	return fsHeader, decoded[hashInfo.pfs0HeaderOffset:], nil
}

// MissingKeyError is returned when prod.keys lacks the key needed to decrypt a title,
// usually because the keys were dumped from an older firmware than the title requires.
type MissingKeyError struct {
	KeyName string
}

func (e *MissingKeyError) Error() string {
	return fmt.Sprintf("missing Key_area_key[%v]", e.KeyName)
}

// maxReadSectionSize is the size of the biggest section read into memory at once.
const maxReadSectionSize = 1 << 31

func decryptAesCtr(ncaHeader *ncaHeader, fsHeader *fsHeader, offset uint64, size uint64, encoded []byte) ([]byte, error) {
	keyRevision := fmt.Sprintf("%02x", ncaHeader.getKeyRevision())
	cryptoType := ncaHeader.cryptoType

	if cryptoType != 0 {
		return []byte{}, errors.New("unsupported crypto type")
	}

	keys, err := settings.SwitchKeys()
	if err != nil {
		return nil, err
	}
	if keys == nil {
		return nil, errors.New("switch keys are not initialized")
	}

	keyName := fmt.Sprintf("key_area_key_application_%s", keyRevision)
	KeyString := keys.GetKey(keyName)
	if KeyString == "" {
		return nil, &MissingKeyError{KeyName: keyName}
	}
	key, _ := hex.DecodeString(KeyString)

	if len(ncaHeader.encryptedKeys) < 0x30 {
		return nil, errors.New("truncated encrypted NCA keys")
	}
	decKey := _crypto.DecryptAes128Ecb(ncaHeader.encryptedKeys[0x20:0x30], key)
	if len(decKey) != 16 {
		return nil, errors.New("invalid key area key")
	}

	// the upper half of the counter is the secure value and the generation, as the
	// compression and the check of the hash trees read it
	counter := make([]byte, 0x10)
	binary.BigEndian.PutUint64(counter, uint64(fsHeader.secureValue)<<32|uint64(fsHeader.generation))
	binary.BigEndian.PutUint64(counter[8:], offset/0x10)

	c, err := aes.NewCipher(decKey)
	if err != nil {
		return nil, err
	}
	if size > uint64(len(encoded)) {
		return nil, errors.New("encoded NCA section is truncated")
	}

	// decrypted where it is: a section can be big, and a copy would take its size again
	s := cipher.NewCTR(c, counter)
	s.XORKeyStream(encoded[:size], encoded[:size])

	return encoded[:size], nil
}
