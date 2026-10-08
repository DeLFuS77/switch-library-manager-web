// Package testnsp builds small encrypted NSP files with made-up keys, for tests.
package testnsp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
)

const (
	HeaderKey  = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	AreaKey    = "1f1e1d1c1b1a19181716151413121110"
	ContentKey = "00112233445566778899aabbccddeeff"
)

// WriteKeys writes a prod.keys with the made-up keys to folder.
func WriteKeys(folder string) error {
	keys := "header_key = " + HeaderKey + "\nkey_area_key_application_00 = " + AreaKey + "\n"
	return os.WriteFile(filepath.Join(folder, "prod.keys"), []byte(keys), 0o600)
}

// WriteNsp writes an NSP with one compressible program NCA to path and returns its bytes.
func WriteNsp(path string) ([]byte, error) {
	return WriteNspOf(path, "compressible test game data ")
}

// WriteNspOf is WriteNsp with an NCA made of text, so that NSPs of different texts hold
// different NCAs (a game, its update, its DLC).
func WriteNspOf(path string, content string) ([]byte, error) {
	plain := make([]byte, 0x40000)
	text := []byte(content)
	for i := range plain {
		plain[i] = text[i%len(text)]
	}
	nca := programNca(plain)
	sum := sha256.Sum256(nca)
	nsp := pfs0([]string{hex.EncodeToString(sum[:16]) + ".nca"}, [][]byte{nca})
	return nsp, os.WriteFile(path, nsp, 0o644)
}

func programNca(plain []byte) []byte {
	areaKey, _ := hex.DecodeString(AreaKey)
	contentKey, _ := hex.DecodeString(ContentKey)
	header := make([]byte, 0xC00)
	copy(header[0x200:], "NCA3")
	binary.LittleEndian.PutUint32(header[0x240:], 0x20)
	binary.LittleEndian.PutUint32(header[0x244:], uint32(0x20+len(plain)/0x200))
	header[0x402] = 1
	header[0x403] = 2
	header[0x404] = 3
	binary.LittleEndian.PutUint32(header[0x540:], 1)
	fsHash := sha256.Sum256(header[0x400:0x600])
	copy(header[0x280:], fsHash[:])
	areaCipher, _ := aes.NewCipher(areaKey)
	areaCipher.Encrypt(header[0x320:0x330], contentKey)

	nca := make([]byte, 0x4000)
	copy(nca, encryptHeader(header))
	section := append([]byte(nil), plain...)
	block, _ := aes.NewCipher(contentKey)
	counter := make([]byte, 16)
	counter[7] = 1
	binary.BigEndian.PutUint64(counter[8:], 0x4000>>4)
	cipher.NewCTR(block, counter).XORKeyStream(section, section)
	return append(nca, section...)
}

// encryptHeader encrypts an NCA header with AES-XTS and the Nintendo tweak.
func encryptHeader(plain []byte) []byte {
	key, _ := hex.DecodeString(HeaderKey)
	first, _ := aes.NewCipher(key[:16])
	second, _ := aes.NewCipher(key[16:])
	result := make([]byte, len(plain))
	for sector := 0; sector < len(plain)/0x200; sector++ {
		tweak := make([]byte, 16)
		binary.BigEndian.PutUint64(tweak[8:], uint64(sector))
		second.Encrypt(tweak, tweak)
		for offset := sector * 0x200; offset < (sector+1)*0x200; offset += 16 {
			block := make([]byte, 16)
			for i := range block {
				block[i] = plain[offset+i] ^ tweak[i]
			}
			first.Encrypt(block, block)
			for i := range block {
				result[offset+i] = block[i] ^ tweak[i]
			}
			var carry byte
			for i := range tweak {
				next := tweak[i] >> 7
				tweak[i] = tweak[i]<<1 | carry
				carry = next
			}
			if carry != 0 {
				tweak[0] ^= 0x87
			}
		}
	}
	return result
}

func pfs0(names []string, payloads [][]byte) []byte {
	var table []byte
	offsets := make([]uint32, len(names))
	for i, name := range names {
		offsets[i] = uint32(len(table))
		table = append(append(table, name...), 0)
	}
	header := make([]byte, 0x10+0x18*len(names)+len(table))
	copy(header, "PFS0")
	binary.LittleEndian.PutUint32(header[4:], uint32(len(names)))
	binary.LittleEndian.PutUint32(header[8:], uint32(len(table)))
	position := 0
	for i, payload := range payloads {
		entry := header[0x10+0x18*i:]
		binary.LittleEndian.PutUint64(entry[0:], uint64(position))
		binary.LittleEndian.PutUint64(entry[8:], uint64(len(payload)))
		binary.LittleEndian.PutUint32(entry[16:], offsets[i])
		position += len(payload)
	}
	copy(header[0x10+0x18*len(names):], table)
	for _, payload := range payloads {
		header = append(header, payload...)
	}
	return header
}

// WriteXci writes an XCI (a game card) with the same program NCA as WriteNsp in its secure
// partition to path and returns its bytes.
func WriteXci(path string) ([]byte, error) {
	plain := make([]byte, 0x40000)
	text := []byte("compressible test game data ")
	for i := range plain {
		plain[i] = text[i%len(text)]
	}
	nca := programNca(plain)
	sum := sha256.Sum256(nca)
	secure := hfs0([]string{hex.EncodeToString(sum[:16]) + ".nca"}, [][]byte{nca})
	root := hfs0([]string{"secure"}, [][]byte{secure})
	header := make([]byte, 0xF000)
	copy(header[0x100:], "HEAD")
	binary.LittleEndian.PutUint64(header[0x130:], 0xF000)
	xci := append(header, root...)
	return xci, os.WriteFile(path, xci, 0o644)
}

// hfs0 builds a partition of a game card: like a PFS0 with bigger entries (with hashes,
// left empty here).
func hfs0(names []string, payloads [][]byte) []byte {
	var table []byte
	offsets := make([]uint32, len(names))
	for i, name := range names {
		offsets[i] = uint32(len(table))
		table = append(append(table, name...), 0)
	}
	header := make([]byte, 0x10+0x40*len(names)+len(table))
	copy(header, "HFS0")
	binary.LittleEndian.PutUint32(header[4:], uint32(len(names)))
	binary.LittleEndian.PutUint32(header[8:], uint32(len(table)))
	position := 0
	for i, payload := range payloads {
		entry := header[0x10+0x40*i:]
		binary.LittleEndian.PutUint64(entry[0:], uint64(position))
		binary.LittleEndian.PutUint64(entry[8:], uint64(len(payload)))
		binary.LittleEndian.PutUint32(entry[16:], offsets[i])
		position += len(payload)
	}
	copy(header[0x10+0x40*len(names):], table)
	for _, payload := range payloads {
		header = append(header, payload...)
	}
	return header
}
