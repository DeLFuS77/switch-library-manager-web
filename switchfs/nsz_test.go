package switchfs

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

const (
	testTitleKek = "a0a1a2a3a4a5a6a7a8a9aaabacadaeaf"
	testTitleKey = "b0b1b2b3b4b5b6b7b8b9babbbcbdbebf"
	testRightsId = "0100000000010000000000000000000a"
)

func initNszKeys(t *testing.T, titleKeys string) {
	t.Helper()
	dir := t.TempDir()
	keys := "header_key = " + testHeaderKey + "\nkey_area_key_application_00 = " + testAreaKey + "\ntitlekek_00 = " + testTitleKek + "\n"
	if err := os.WriteFile(filepath.Join(dir, "prod.keys"), []byte(keys), 0o600); err != nil {
		t.Fatal(err)
	}
	if titleKeys != "" {
		os.WriteFile(filepath.Join(dir, "title.keys"), []byte(titleKeys), 0o600)
	}
	if _, err := settings.InitSwitchKeys(dir); err != nil {
		t.Fatal(err)
	}
}

// gameData is compressible, like real game files
func gameData(size int) []byte {
	data := make([]byte, size)
	text := []byte("Switch Library Manager test data; the same words again and again. ")
	for i := range data {
		data[i] = text[i%len(text)] ^ byte(i/4096)
	}
	return data
}

// makeProgramNca builds an encrypted program NCA with one section at 0x4000, like real
// games. With rightsId, the section key is the title key, else it is in the key area.
func makeProgramNca(t *testing.T, plain []byte, encType byte, rightsId string) []byte {
	t.Helper()
	areaKey, _ := hex.DecodeString(testAreaKey)
	contentKey, _ := hex.DecodeString(testNcaKey)
	if rightsId != "" {
		contentKey, _ = hex.DecodeString(testTitleKey)
	}
	header := make([]byte, 0xC00)
	copy(header[0x200:], "NCA3")
	header[0x205] = NcaContentType_Program
	if rightsId != "" {
		id, _ := hex.DecodeString(rightsId)
		copy(header[0x230:], id)
	}
	binary.LittleEndian.PutUint32(header[0x240:], 0x20)
	binary.LittleEndian.PutUint32(header[0x244:], uint32(0x20+len(plain)/0x200))
	header[0x402] = 1
	header[0x403] = 2
	header[0x404] = encType
	// generation and secure value, both part of the counter
	binary.LittleEndian.PutUint32(header[0x540:], 1)
	binary.LittleEndian.PutUint32(header[0x544:], 0x55667788)
	fsHash := sha256.Sum256(header[0x400:0x600])
	copy(header[0x280:], fsHash[:])
	areaCipher, _ := aes.NewCipher(areaKey)
	areaCipher.Encrypt(header[0x320:0x330], contentKey)

	nca := make([]byte, 0x4000)
	copy(nca, encryptNcaHeader(header, testHeaderKey))
	section := append([]byte(nil), plain...)
	if encType == 3 {
		block, _ := aes.NewCipher(contentKey)
		counter := []byte{0x55, 0x66, 0x77, 0x88, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0}
		binary.BigEndian.PutUint64(counter[8:], 0x4000>>4)
		cipher.NewCTR(block, counter).XORKeyStream(section, section)
	}
	return append(nca, section...)
}

func makeTicket(rightsId string) []byte {
	ticket := make([]byte, 0x2C0)
	binary.LittleEndian.PutUint32(ticket, 0x010004)
	kek, _ := hex.DecodeString(testTitleKek)
	key, _ := hex.DecodeString(testTitleKey)
	block, _ := aes.NewCipher(kek)
	block.Encrypt(ticket[0x180:0x190], key)
	id, _ := hex.DecodeString(rightsId)
	copy(ticket[0x2A0:], id)
	return ticket
}

func contentName(nca []byte, suffix string) string {
	sum := sha256.Sum256(nca)
	return hex.EncodeToString(sum[:16]) + suffix
}

// writeTestNsp writes an NSP with two compressible NCA, a metadata NCA, a ticket and an xml.
func writeTestNsp(t *testing.T, folder string) (string, []byte) {
	t.Helper()
	game := makeProgramNca(t, gameData(0x80000), 3, "")
	licensed := makeProgramNca(t, gameData(0x40000), 3, testRightsId)
	meta := []byte(strings.Repeat("m", 0x200))
	names := []string{contentName(game, ".nca"), contentName(licensed, ".nca"), contentName(meta, ".cnmt.nca"), testRightsId + ".tik", "control.xml"}
	nsp := makePFS0(pfs0Magic, names, [][]byte{game, licensed, meta, makeTicket(testRightsId), []byte("<xml/>")})
	path := filepath.Join(folder, "Game [0100000000010000][v0].nsp")
	if err := os.WriteFile(path, nsp, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, nsp
}

func TestCompressVerifyAndDecompressNsp(t *testing.T) {
	initNszKeys(t, "")
	folder := t.TempDir()
	source, original := writeTestNsp(t, folder)
	target := NszPath(source)

	var lastDone, lastTotal int64
	result, err := CompressNsp(context.Background(), source, target, CompressOptions{Level: LevelBalanced, Workers: 2, Progress: func(done, total int64) {
		lastDone, lastTotal = done, total
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Compressed != 2 || result.Stored != 3 {
		t.Fatalf("expected 2 compressed and 3 stored files: %+v", result)
	}
	if result.OutputSize >= result.InputSize/2 {
		t.Fatalf("game data must compress well: %v -> %v", result.InputSize, result.OutputSize)
	}
	if lastTotal != int64(len(original)) || lastDone <= 0 {
		t.Fatalf("progress: %v of %v", lastDone, lastTotal)
	}

	pfs0, err := ReadPfs0File(target)
	if err != nil {
		t.Fatal(err)
	}
	nczCount := 0
	for _, file := range pfs0.Files {
		if strings.HasSuffix(file.Name, ".ncz") {
			nczCount++
		}
	}
	if nczCount != 2 {
		t.Fatalf("expected 2 NCZ files: %+v", pfs0.Files)
	}

	expected, err := HashNcas(context.Background(), source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyNsz(context.Background(), target, expected, nil); err != nil {
		t.Fatalf("verification: %v", err)
	}

	restored := filepath.Join(folder, "restored.nsp")
	if err := DecompressNsz(context.Background(), target, restored, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(restored)
	if !bytes.Equal(data, original) {
		t.Fatal("decompressing must give the original NSP back byte for byte")
	}

	// the library reads the metadata of NSZ files like NSP files
	if _, err := ReadPfs0File(target); err != nil {
		t.Fatal(err)
	}
}

func TestEveryLevelRoundTrips(t *testing.T) {
	initNszKeys(t, "")
	folder := t.TempDir()
	source, _ := writeTestNsp(t, folder)
	expected, _ := HashNcas(context.Background(), source, nil)
	for _, level := range []string{LevelFast, LevelBalanced, LevelMax} {
		target := filepath.Join(folder, level+".nsz")
		if _, err := CompressNsp(context.Background(), source, target, CompressOptions{Level: level}); err != nil {
			t.Fatalf("%s: %v", level, err)
		}
		if err := VerifyNsz(context.Background(), target, expected, nil); err != nil {
			t.Fatalf("%s: %v", level, err)
		}
	}
}

func TestVerifyDetectsDamagedNsz(t *testing.T) {
	initNszKeys(t, "")
	folder := t.TempDir()
	source, _ := writeTestNsp(t, folder)
	target := NszPath(source)
	if _, err := CompressNsp(context.Background(), source, target, CompressOptions{}); err != nil {
		t.Fatal(err)
	}
	pfs0, _ := ReadPfs0File(target)
	data, _ := os.ReadFile(target)
	for _, file := range pfs0.Files {
		if strings.HasSuffix(file.Name, ".ncz") {
			// a byte of the section key: decompression works, the content is wrong
			data[file.StartOffset+0x4000+0x10+0x20] ^= 0xFF
			break
		}
	}
	os.WriteFile(target, data, 0o644)
	if err := VerifyNsz(context.Background(), target, nil, nil); err == nil {
		t.Fatal("a damaged NSZ must fail the verification")
	}
}

func TestDamagedSourceIsReported(t *testing.T) {
	initNszKeys(t, "")
	source, original := writeTestNsp(t, t.TempDir())
	damaged := append([]byte(nil), original...)
	damaged[len(damaged)/3] ^= 0x01
	os.WriteFile(source, damaged, 0o644)
	if _, err := HashNcas(context.Background(), source, nil); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("a modified NCA must be reported: %v", err)
	}
}

func TestTitleKeyFromTitleKeysFile(t *testing.T) {
	kek, _ := hex.DecodeString(testTitleKek)
	key, _ := hex.DecodeString(testTitleKey)
	encrypted := make([]byte, 16)
	block, _ := aes.NewCipher(kek)
	block.Encrypt(encrypted, key)
	initNszKeys(t, testRightsId+" = "+hex.EncodeToString(encrypted)+"\n")

	// an NSP without ticket
	licensed := makeProgramNca(t, gameData(0x40000), 3, testRightsId)
	folder := t.TempDir()
	source := filepath.Join(folder, "noticket.nsp")
	os.WriteFile(source, makePFS0(pfs0Magic, []string{contentName(licensed, ".nca")}, [][]byte{licensed}), 0o644)
	target := NszPath(source)
	if _, err := CompressNsp(context.Background(), source, target, CompressOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := VerifyNsz(context.Background(), target, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestMissingTitleKeyIsAnError(t *testing.T) {
	initNszKeys(t, "")
	licensed := makeProgramNca(t, gameData(0x40000), 3, testRightsId)
	source := filepath.Join(t.TempDir(), "noticket.nsp")
	os.WriteFile(source, makePFS0(pfs0Magic, []string{contentName(licensed, ".nca")}, [][]byte{licensed}), 0o644)
	_, err := CompressNsp(context.Background(), source, NszPath(source), CompressOptions{})
	if err == nil || !strings.Contains(err.Error(), "title key") {
		t.Fatalf("a missing title key must be explained: %v", err)
	}
	if _, statErr := os.Stat(NszPath(source)); statErr == nil {
		t.Fatal("no NSZ may be left behind")
	}
}

func TestUpdatePatchSectionsAreStored(t *testing.T) {
	initNszKeys(t, "")
	patch := makeProgramNca(t, gameData(0x40000), 4, "")
	source := filepath.Join(t.TempDir(), "update.nsp")
	os.WriteFile(source, makePFS0(pfs0Magic, []string{contentName(patch, ".nca")}, [][]byte{patch}), 0o644)
	if _, err := CompressNsp(context.Background(), source, NszPath(source), CompressOptions{}); !errors.Is(err, ErrNotCompressible) {
		t.Fatalf("an NSP with nothing to compress: %v", err)
	}
}

func TestCompressionCanBeCancelled(t *testing.T) {
	initNszKeys(t, "")
	source, _ := writeTestNsp(t, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CompressNsp(ctx, source, NszPath(source), CompressOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
	if _, err := os.Stat(NszPath(source)); err == nil {
		t.Fatal("a cancelled compression must not leave a file")
	}
}

// Writes the NCZ files of a test NSZ to SLM_NSZ_EXPORT, to check them with other tools.
func TestExportNczForExternalCheck(t *testing.T) {
	folder := os.Getenv("SLM_NSZ_EXPORT")
	if folder == "" {
		t.Skip("set SLM_NSZ_EXPORT")
	}
	initNszKeys(t, "")
	source, _ := writeTestNsp(t, t.TempDir())
	target := NszPath(source)
	if _, err := CompressNsp(context.Background(), source, target, CompressOptions{Level: LevelMax, Workers: 4}); err != nil {
		t.Fatal(err)
	}
	pfs0, _ := ReadPfs0File(target)
	data, _ := os.ReadFile(target)
	for _, file := range pfs0.Files {
		if strings.HasSuffix(file.Name, ".ncz") {
			os.WriteFile(filepath.Join(folder, file.Name), data[file.StartOffset:file.StartOffset+file.Size], 0o644)
		}
	}
}

// Compresses a real game: SLM_NSZ_REAL is the NSP (only read), SLM_NSZ_OUT the folder of
// the NSZ and SLM_BENCH_DATA a data folder whose settings point to prod.keys.
func TestCompressRealGame(t *testing.T) {
	source, out, data := os.Getenv("SLM_NSZ_REAL"), os.Getenv("SLM_NSZ_OUT"), os.Getenv("SLM_BENCH_DATA")
	if source == "" || out == "" || data == "" {
		t.Skip("set SLM_NSZ_REAL, SLM_NSZ_OUT and SLM_BENCH_DATA")
	}
	if _, err := settings.InitSwitchKeys(data); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(out, strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))+".nsz")
	start := time.Now()
	result, err := CompressNsp(context.Background(), source, target, CompressOptions{Level: LevelBalanced, Workers: 4})
	if err != nil {
		t.Fatal(err)
	}
	compressTime := time.Since(start)
	start = time.Now()
	expected, err := HashNcas(context.Background(), source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyNsz(context.Background(), target, expected, nil); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d compressed, %d stored: %d -> %d bytes (%.1f%%) in %v, verified in %v", result.Compressed, result.Stored,
		result.InputSize, result.OutputSize, float64(result.OutputSize)*100/float64(result.InputSize), compressTime.Round(time.Millisecond), time.Since(start).Round(time.Millisecond))
}
