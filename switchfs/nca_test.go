package switchfs

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs/_crypto"
)

const (
	testHeaderKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	testAreaKey   = "1f1e1d1c1b1a19181716151413121110"
	testNcaKey    = "00112233445566778899aabbccddeeff"
)

func TestNintendoTweak(t *testing.T) {
	if got := getNintendoTweak(0x010203); got != [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3} {
		t.Fatalf("unexpected Nintendo tweak: %x", got)
	}
}

func TestNcaHeaderAndFsValidation(t *testing.T) {
	if _, err := DecryptNcaHeader("not-hex", make([]byte, 0x400)); err == nil {
		t.Fatal("expected invalid key error")
	}
	if _, err := DecryptNcaHeader("000102030405060708090a0b0c0d0e0f", make([]byte, 0x3ff)); err == nil {
		t.Fatal("expected short header error")
	}
	if _, err := _decryptNcaHeader(nil, make([]byte, 0x10), 0x10, 0x10, 0); err == nil {
		t.Fatal("expected invalid decryption bounds error")
	}
	if _, err := _decryptNcaHeader(nil, make([]byte, 0x10), 0x11, 0x10, 0); err == nil {
		t.Fatal("expected unaligned decryption bounds error")
	}
	if got := getFsEntry(&ncaHeader{headerBytes: make([]byte, 0x250)}, 1); got.Size != 0 {
		t.Fatal("expected invalid FS entry to be empty")
	}
	if _, err := getFsHeader(&ncaHeader{headerBytes: make([]byte, 0x3ff)}, 0); err == nil {
		t.Fatal("expected truncated FS header error")
	}
	if _, err := (&fsHeader{fsHeaderBytes: make([]byte, 0x100), hashType: 1}).getHashInfo(); err == nil {
		t.Fatal("expected unsupported hash error")
	}
}

func TestNcaHeaderFieldsAndRights(t *testing.T) {
	for _, test := range []struct {
		name string
		a    byte
		b    byte
		want byte
	}{
		{name: "first", a: 4, b: 2, want: 4},
		{name: "second", a: 2, b: 4, want: 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := max(test.a, test.b); got != test.want {
				t.Fatalf("max(%d, %d) = %d", test.a, test.b, got)
			}
		})
	}
	if (&ncaHeader{rightsId: make([]byte, 0x10)}).HasRightsId() {
		t.Fatal("zero rights ID should be absent")
	}
	if !(&ncaHeader{rightsId: append([]byte{1}, make([]byte, 0xf)...)}).HasRightsId() {
		t.Fatal("non-zero rights ID should be present")
	}
	if (&ncaHeader{rightsId: []byte{}}).HasRightsId() || (*ncaHeader)(nil).HasRightsId() {
		t.Fatal("short rights ID should be absent")
	}

	plain := make([]byte, 0x400)
	copy(plain[0x200:], []byte("NCA2"))
	copy(plain[0x230:], bytes.Repeat([]byte{0x11}, 0x10))
	encrypted := encryptNcaHeader(plain, testHeaderKey)
	key, err := hex.DecodeString(testHeaderKey)
	if err != nil {
		t.Fatal(err)
	}
	c, err := newTestXTSCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := _decryptNcaHeader(c, encrypted, 0x400, 0x200, 0)
	if err != nil || !bytes.Equal(decoded[:0x400], plain) {
		t.Fatalf("synthetic header round trip failed: %v", err)
	}
	parsed, err := DecryptNcaHeader(testHeaderKey, encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.HasRightsId() || string(parsed.rightsId) != string(bytes.Repeat([]byte{0x11}, 0x10)) {
		t.Fatalf("unexpected rights ID: %x", parsed.rightsId)
	}
}

func TestDecryptNcaHeaderSuccessAndBranches(t *testing.T) {
	plain := make([]byte, 0xc00)
	copy(plain[0x200:], []byte("NCA2"))
	binary.LittleEndian.PutUint64(plain[0x210:], 0x0102030405060708)
	plain[0x206] = 3
	plain[0x220] = 1
	parsed, err := DecryptNcaHeader(testHeaderKey, encryptNcaHeader(plain, testHeaderKey))
	if err != nil {
		t.Fatal(err)
	}
	if string(parsed.titleId) != "102030405060708" || parsed.getKeyRevision() != 2 {
		t.Fatalf("unexpected parsed NCA header: %#v", parsed)
	}

	nca3 := make([]byte, 0x400)
	copy(nca3[0x200:], []byte("NCA3"))
	if _, err := DecryptNcaHeader(testHeaderKey, encryptNcaHeader(nca3, testHeaderKey)); err == nil {
		t.Fatal("expected truncated NCA3 error")
	}
	invalid := make([]byte, 0x400)
	if _, err := DecryptNcaHeader(testHeaderKey, encryptNcaHeader(invalid, testHeaderKey)); err == nil {
		t.Fatal("expected invalid NCA magic error")
	}

	key, err := hex.DecodeString(testHeaderKey)
	c, err := newTestXTSCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	plain = bytes.Repeat([]byte{0x3c}, 0x200)
	encrypted := encryptNcaHeader(plain, testHeaderKey)
	decoded, err := _decryptNcaHeader(c, encrypted, len(plain), 0x200, 0)
	if err != nil || !bytes.Equal(decoded[:len(plain)], plain) {
		t.Fatalf("header sector decryption failed: %v", err)
	}
}

// The sections of a big game lie past 4 GB: their offsets do not fit in 32 bits.
func TestFsEntryPast4GB(t *testing.T) {
	headerBytes := make([]byte, 0x600)
	binary.LittleEndian.PutUint32(headerBytes[0x250:], 0x20)
	binary.LittleEndian.PutUint32(headerBytes[0x254:], 0x1FB5BA0)
	got := getFsEntry(&ncaHeader{headerBytes: headerBytes}, 1)
	if got.StartOffset != 0x4000 || got.EndOffset != 0x3F6B74000 || got.Size != 0x3F6B70000 {
		t.Fatalf("a section that ends past 4 GB: %#v", got)
	}
}

func TestFsHeaderAndEntry(t *testing.T) {
	headerBytes := make([]byte, 0x600)
	binary.LittleEndian.PutUint32(headerBytes[0x240:], 2)
	binary.LittleEndian.PutUint32(headerBytes[0x244:], 5)
	if got := getFsEntry(&ncaHeader{headerBytes: headerBytes}, 0); got.StartOffset != 0x400 || got.EndOffset != 0xa00 || got.Size != 0x600 {
		t.Fatalf("unexpected FS entry: %#v", got)
	}
	fs := headerBytes[0x400:0x600]
	fs[2] = 0
	fs[3] = 2
	binary.LittleEndian.PutUint64(fs[0x40:], 0x1234)
	binary.LittleEndian.PutUint64(fs[0x48:], 0x5678)
	sum := sha256Sum(fs)
	copy(headerBytes[0x280:], sum[:])
	parsed, err := getFsHeader(&ncaHeader{headerBytes: headerBytes}, 0)
	if err != nil {
		t.Fatal(err)
	}
	info, err := parsed.getHashInfo()
	if err != nil || info.pfs0HeaderOffset != 0x1234 || info.pfs0size != 0x5678 {
		t.Fatalf("unexpected hash info: %#v %v", info, err)
	}
	fs[3] = 9
	if _, err := getFsHeader(&ncaHeader{headerBytes: headerBytes}, 0); err == nil {
		t.Fatal("expected FS header hash mismatch")
	}
}

func TestDecryptAesCtrErrorPaths(t *testing.T) {
	if _, err := decryptAesCtr(&ncaHeader{cryptoType: 1}, &fsHeader{}, 0, 16, make([]byte, 16)); err == nil {
		t.Fatal("expected unsupported crypto type error")
	}
	if _, err := decryptAesCtr(&ncaHeader{cryptoType: 0}, &fsHeader{}, 0, 16, make([]byte, 16)); err == nil {
		t.Fatal("expected missing key error")
	}
	if _, _, err := openMetaNcaDataSection(bytes.NewReader(make([]byte, 0x10)), 0); err == nil {
		t.Fatal("expected short NCA error")
	}
}

func TestOpenMetaNcaDataSectionSupportsUnencryptedSections(t *testing.T) {
	section := makePFS0(pfs0Magic, []string{"meta.cnmt"}, [][]byte{makeCNMT(1, 0, ContentMetaType_Application)})
	section = append(section, make([]byte, (-len(section))&0x1ff)...)
	nca := mutateSyntheticNCAHeader(t, makeSyntheticNCA(t, section, 0), func(header []byte) {
		header[0x404] = 1
	})
	copy(nca[0xc00:], section)
	_, decoded, err := openMetaNcaDataSection(bytes.NewReader(nca), 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded[:4]) != "PFS0" {
		t.Fatalf("unexpected unencrypted section: %q", decoded[:4])
	}
}

// The section of a metadata NCA is read whole: a damaged header made the scan, the icons and
// the facts of a file stop, with a start of its data past the section, or take gigabytes
// before the read failed, with a section past the end of the file.
func TestMetaSectionWithinItsFile(t *testing.T) {
	section := makePFS0(pfs0Magic, []string{"meta.cnmt"}, [][]byte{makeCNMT(1, 0, ContentMetaType_Application)})
	section = append(section, make([]byte, (-len(section))&0x1ff)...)
	nca := makeSyntheticNCA(t, section, 1)
	if _, decoded, err := openMetaNcaDataSection(bytes.NewReader(nca), 0); err != nil || string(decoded[:4]) != "PFS0" {
		t.Fatalf("the section: %v", err)
	}
	pastTheSection := mutateSyntheticNCAHeader(t, nca, func(header []byte) {
		binary.LittleEndian.PutUint64(header[0x440:], uint64(len(section))+1)
	})
	if _, _, err := openMetaNcaDataSection(bytes.NewReader(pastTheSection), 0); err == nil {
		t.Fatal("the data of the section starts past its end")
	}
	// a section of 1 GiB in a file of a few kB
	pastTheFile := mutateSyntheticNCAHeader(t, nca, func(header []byte) {
		binary.LittleEndian.PutUint32(header[0x244:], 6+(1<<30)/0x200)
	})
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, _, err := openMetaNcaDataSection(bytes.NewReader(pastTheFile), 0)
	runtime.ReadMemStats(&after)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("a section past the end of the file: %v", err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 64<<20 {
		t.Fatalf("%d bytes taken for a section that is not there", allocated)
	}
	// also from a reader that does not tell its size
	if _, _, err := openMetaNcaDataSection(unsizedReader{bytes.NewReader(pastTheFile)}, 0); err == nil {
		t.Fatal("a section past the end of an unsized reader")
	}
	if _, decoded, err := openMetaNcaDataSection(unsizedReader{bytes.NewReader(nca)}, 0); err != nil || string(decoded[:4]) != "PFS0" {
		t.Fatalf("the section from an unsized reader: %v", err)
	}
}

// unsizedReader hides the size of its reader.
type unsizedReader struct{ reader io.ReaderAt }

func (u unsizedReader) ReadAt(p []byte, offset int64) (int, error) { return u.reader.ReadAt(p, offset) }

// The counter of a section is its secure value and its generation, as the compression and the
// check of the hash trees build it: the metadata, read with the generation alone, was nothing
// readable in a section whose secure value is not zero, and its game had no name, NACP or icon
// of its own.
func TestMetaSectionWithASecureValue(t *testing.T) {
	initNszKeys(t, "")
	section := makePFS0(pfs0Magic, []string{"Application_0100000000010000.cnmt"}, [][]byte{makeCNMT(0x0100000000010000, 0, ContentMetaType_Application)})
	section = append(section, make([]byte, (-len(section))&0x1ff)...)
	// encrypted with the secure value 0x55667788
	nca := makeProgramNca(t, section, 3, "")
	_, decoded, err := openMetaNcaDataSection(bytes.NewReader(nca), 0)
	if err != nil {
		t.Fatal(err)
	}
	pfs0, err := readPfs0(bytes.NewReader(decoded), 0)
	if err != nil {
		t.Fatalf("the section is not decrypted: %v", err)
	}
	if attributes, err := readBinaryCnmt(pfs0, decoded); err != nil || attributes.TitleId != "0100000000010000" {
		t.Fatalf("the CNMT: %+v %v", attributes, err)
	}
}

func TestDecryptAesCtrSuccessAndTruncation(t *testing.T) {
	initTestKeys(t)
	plain := bytes.Repeat([]byte("ctr"), 32)
	nca := &ncaHeader{cryptoType: 0, keyGeneration1: 3, encryptedKeys: make([]byte, 0x40)}
	areaKey, _ := hex.DecodeString(testAreaKey)
	contentKey, _ := hex.DecodeString(testNcaKey)
	block, err := aes.NewCipher(areaKey)
	if err != nil {
		t.Fatal(err)
	}
	block.Encrypt(nca.encryptedKeys[0x20:0x30], contentKey)
	fs := &fsHeader{generation: 9}
	if got := nca.getKeyRevision(); got != 2 {
		t.Fatalf("unexpected key revision: %d", got)
	}
	encrypted := cryptCtr(t, contentKey, fs.generation, 0x30, plain)
	decoded, err := decryptAesCtr(nca, fs, 0x30, uint64(len(plain)), encrypted)
	if err != nil || !bytes.Equal(decoded, plain) {
		t.Fatalf("CTR decryption failed: %v", err)
	}
	if _, err := decryptAesCtr(nca, fs, 0, uint64(len(encrypted)+1), encrypted); err == nil {
		t.Fatal("expected truncated encoded section error")
	}
	shortKeys := *nca
	shortKeys.encryptedKeys = make([]byte, 0x2f)
	if _, err := decryptAesCtr(&shortKeys, fs, 0, 16, make([]byte, 16)); err == nil {
		t.Fatal("expected truncated encrypted keys error")
	}
	missingKeyDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(missingKeyDir, "prod.keys"), []byte("header_key = "+testHeaderKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.InitSwitchKeys(missingKeyDir); err != nil {
		t.Fatal(err)
	}
	if _, err := decryptAesCtr(&ncaHeader{cryptoType: 0, encryptedKeys: make([]byte, 0x40)}, fs, 0, 16, make([]byte, 16)); err == nil {
		t.Fatal("expected missing application key error")
	}
}

func initTestKeys(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	keys := []byte("header_key = " + testHeaderKey + "\nkey_area_key_application_00 = " + testAreaKey + "\nkey_area_key_application_02 = " + testAreaKey + "\n")
	if err := os.WriteFile(filepath.Join(dir, "prod.keys"), keys, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.InitSwitchKeys(dir); err != nil {
		t.Fatal(err)
	}
}

func makeSyntheticNCA(t *testing.T, section []byte, fsType byte) []byte {
	t.Helper()
	if len(section)%0x200 != 0 {
		t.Fatalf("synthetic NCA section is not sector aligned: %d", len(section))
	}
	initTestKeys(t)
	contentKey, _ := hex.DecodeString(testNcaKey)
	areaKey, _ := hex.DecodeString(testAreaKey)
	areaCipher, _ := aes.NewCipher(areaKey)
	header := make([]byte, 0xc00)
	copy(header[0x200:], []byte("NCA3"))
	binary.LittleEndian.PutUint32(header[0x240:], 6)
	binary.LittleEndian.PutUint32(header[0x244:], uint32(6+len(section)/0x200))
	header[0x402] = fsType
	header[0x403] = 2
	header[0x404] = 3
	binary.LittleEndian.PutUint32(header[0x540:], 1)
	binary.LittleEndian.PutUint64(header[0x440:], 0)
	binary.LittleEndian.PutUint64(header[0x448:], uint64(len(section)))
	fsHash := sha256.Sum256(header[0x400:0x600])
	copy(header[0x280:], fsHash[:])
	areaCipher.Encrypt(header[0x320:0x330], contentKey)
	encryptedHeader := encryptNcaHeader(header, testHeaderKey)
	encodedSection := cryptCtr(t, contentKey, 1, 0xc00, section)
	return append(encryptedHeader, encodedSection...)
}

func cryptCtr(t *testing.T, key []byte, generation uint32, offset uint32, data []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	counter := make([]byte, 0x10)
	binary.BigEndian.PutUint64(counter, uint64(generation))
	binary.BigEndian.PutUint64(counter[8:], uint64(offset/0x10))
	result := make([]byte, len(data))
	cipher.NewCTR(block, counter).XORKeyStream(result, data)
	return result
}

func encryptNcaHeader(plain []byte, keyString string) []byte {
	key, _ := hex.DecodeString(keyString)
	result := make([]byte, len(plain))
	for sector := 0; sector < len(plain)/0x200; sector++ {
		blockStart := sector * 0x200
		tweak := getNintendoTweak(sector)
		second, _ := aes.NewCipher(key[len(key)/2:])
		encryptedTweak := make([]byte, aes.BlockSize)
		second.Encrypt(encryptedTweak, tweak[:])
		for blockOffset := 0; blockOffset < 0x200; blockOffset += aes.BlockSize {
			block := encryptXTSBlock(key, plain[blockStart+blockOffset:blockStart+blockOffset+aes.BlockSize], encryptedTweak)
			copy(result[blockStart+blockOffset:blockStart+blockOffset+aes.BlockSize], block)
			testMul2Bytes(encryptedTweak)
		}
	}
	return result
}

func encryptXTSBlock(key, plain, tweak []byte) []byte {
	first, _ := aes.NewCipher(key[:len(key)/2])
	input := make([]byte, aes.BlockSize)
	for i := range input {
		input[i] = plain[i] ^ tweak[i]
	}
	first.Encrypt(input, input)
	for i := range input {
		input[i] ^= tweak[i]
	}
	return input
}

func newTestXTSCipher(key []byte) (*_crypto.Cipher, error) {
	return _crypto.NewCipher(aes.NewCipher, key)
}

func testMul2(tweak *[16]byte) {
	testMul2Bytes(tweak[:])
}

func testMul2Bytes(tweak []byte) {
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

func sha256Sum(data []byte) [32]byte {
	return sha256.Sum256(data)
}
