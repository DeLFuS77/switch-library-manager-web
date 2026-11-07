package testnup

import (
	"crypto/sha256"
	"encoding/binary"
)

// Nso is a code module of an ExeFS (main, sdk, subsdk0...) made of three segments stored as
// they are, each with its SHA-256 in the header to be checked.
func Nso(text, ro, data []byte) []byte {
	const nsoHeaderSize = 0x100
	header := make([]byte, nsoHeaderSize)
	copy(header, "NSO0")
	le := binary.LittleEndian
	// the SHA-256 of the three segments is to be checked
	le.PutUint32(header[0x0C:], 0b111000)
	body := []byte{}
	for i, segment := range [][]byte{text, ro, data} {
		le.PutUint32(header[0x10+0x10*i:], uint32(nsoHeaderSize+len(body)))
		le.PutUint32(header[0x14+0x10*i:], uint32(len(body)))
		le.PutUint32(header[0x18+0x10*i:], uint32(len(segment)))
		le.PutUint32(header[0x60+4*i:], uint32(len(segment)))
		sum := sha256.Sum256(segment)
		copy(header[0xA0+0x20*i:], sum[:])
		body = append(body, segment...)
	}
	return append(header, body...)
}
