package switchfs

import (
	"encoding/binary"
	"errors"
	"io"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// ncaCompressed tells whether an NCA stores a section with the compressed storage of firmware
// 12 and later: its FS header gives the table of the LZ4 blocks (its size at 0x180, a BKTR
// header at 0x188). Only the header of the NCA is read.
func ncaCompressed(reader io.ReaderAt, offset int64) (bool, error) {
	encrypted := make([]byte, 0xC00)
	if _, err := reader.ReadAt(encrypted, offset); err != nil {
		return false, err
	}
	keys, err := settings.SwitchKeys()
	if err != nil {
		return false, err
	}
	if keys == nil || keys.GetKey("header_key") == "" {
		return false, errors.New("missing key - header_key")
	}
	header, err := DecryptNcaHeader(keys.GetKey("header_key"), encrypted)
	if err != nil {
		return false, err
	}
	for index := 0; index < 4; index++ {
		if getFsEntry(header, index).Size == 0 {
			continue
		}
		fsh, err := getFsHeader(header, index)
		if err != nil {
			return false, err
		}
		b := fsh.fsHeaderBytes
		if binary.LittleEndian.Uint64(b[0x180:]) != 0 && string(b[0x188:0x18C]) == "BKTR" {
			return true, nil
		}
	}
	return false, nil
}

// noteNcaLz4 notes in the metadata of a game or an update whether its program is stored with
// the LZ4 of the compressed storage of its NCA (see ContentMetaAttributes.NcaLz4). An NCZ, an NCA
// compressed already, is not looked at. A program that cannot be read is noted as not compressed.
func noteNcaLz4(cnmt *ContentMetaAttributes, file io.ReaderAt, pfs0 *PFS0, pfs0Offset int64) {
	if cnmt == nil || cnmt.Type == "DLC" {
		return
	}
	cnmt.NcaLz4Read = true
	program, ok := cnmt.Contents["Program"]
	if !ok {
		return
	}
	nca := getNcaById(pfs0, program.ID)
	if nca == nil || nca.Name != program.ID+".nca" {
		return
	}
	cnmt.NcaLz4, _ = ncaCompressed(file, pfs0Offset+int64(nca.StartOffset))
}
