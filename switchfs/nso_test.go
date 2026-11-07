package switchfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/testnup"
)

// madeUpNso is an NSO module made up of three segments of text, each compressed with LZ4 when
// compressed is set, with its SHA-256 in the header to be checked.
func madeUpNso(compressed bool) []byte {
	segments := [3][]byte{
		append(bytes.Repeat([]byte("text of the module, made up "), 900), nupNoise(4000, 8)...),
		append(bytes.Repeat([]byte("read-only data "), 300), nupNoise(700, 9)...),
		bytes.Repeat([]byte{0, 0, 0, 1}, 2000),
	}
	header := make([]byte, nsoHeaderSize)
	copy(header, nsoMagic)
	le := binary.LittleEndian
	flags := uint32(0b111000)
	if compressed {
		flags |= 0b111
	}
	le.PutUint32(header[0x0C:], flags)
	body := []byte{}
	memory := uint32(0)
	for i, segment := range segments {
		stored := segment
		if compressed {
			stored = lz4CompressFast(segment, true)
		}
		le.PutUint32(header[0x10+0x10*i:], uint32(nsoHeaderSize+len(body)))
		le.PutUint32(header[0x14+0x10*i:], memory)
		le.PutUint32(header[0x18+0x10*i:], uint32(len(segment)))
		le.PutUint32(header[0x60+4*i:], uint32(len(stored)))
		sum := sha256.Sum256(segment)
		copy(header[0xA0+0x20*i:], sum[:])
		body = append(body, stored...)
		memory += uint32(len(segment)+0xFFF) &^ 0xFFF
	}
	return append(header, body...)
}

// checkNso writes a module into a check in pieces of the size given, as the reader of a NUP
// hands them, and gives what the check tells.
func checkNso(module []byte, size int64, piece int) string {
	check := newNsoCheck(size)
	for at := 0; at < len(module); at += piece {
		check.Write(module[at:min(at+piece, len(module))])
	}
	return check.result()
}

func TestNsoCheckGoodModules(t *testing.T) {
	for _, compressed := range []bool{true, false} {
		module := madeUpNso(compressed)
		for _, piece := range []int{1, 7, 0x100, 4096, len(module)} {
			if problem := checkNso(module, int64(len(module)), piece); problem != "" {
				t.Errorf("compressed %v, pieces of %d: %s", compressed, piece, problem)
			}
		}
	}
}

func TestNsoCheckTellsAModuleCutShort(t *testing.T) {
	module := madeUpNso(true)
	// bytes lost inside the text segment, as in a dump that lost them: the rest comes earlier
	cut := append(append([]byte{}, module[:0x300]...), module[0x300+980:]...)
	problem := checkNso(cut, int64(len(cut)), 4096)
	if problem != "it is 980 bytes shorter than its header says" {
		t.Errorf("problem %q", problem)
	}
	// bytes lost at the end
	if problem := checkNso(module[:len(module)-31], int64(len(module)-31), 512); !strings.HasPrefix(problem, "it is 31 bytes shorter") {
		t.Errorf("cut at the end: %q", problem)
	}
	// a module that ends in its header
	if problem := checkNso(module[:0x80], 0x80, 512); problem != "it ends in its header (128 of 256 bytes)" {
		t.Errorf("cut in the header: %q", problem)
	}
}

func TestNsoCheckTellsADamagedSegment(t *testing.T) {
	module := madeUpNso(true)
	le := binary.LittleEndian
	// a byte of the data segment changed: it decompresses, but not to what was hashed
	damaged := append([]byte{}, module...)
	data := int(le.Uint32(module[0x30:]))
	damaged[data+int(le.Uint32(module[0x68:]))-1] ^= 0x01
	if problem := checkNso(damaged, int64(len(damaged)), 1000); problem != "the SHA-256 of its data segment does not match its header" &&
		problem != "its data segment does not decompress to the 8000 bytes its header says (LZ4)" {
		t.Errorf("damaged data: %q", problem)
	}
	// a hash of the header that does not match
	wrongHash := append([]byte{}, module...)
	wrongHash[0xC0] ^= 0xFF
	if problem := checkNso(wrongHash, int64(len(wrongHash)), 1000); problem != "the SHA-256 of its ro segment does not match its header" {
		t.Errorf("wrong hash: %q", problem)
	}
	// an uncompressed module checked by its hashes alone
	plain := madeUpNso(false)
	plain[0x100+5] ^= 0x01
	if problem := checkNso(plain, int64(len(plain)), 1000); problem != "the SHA-256 of its text segment does not match its header" {
		t.Errorf("plain: %q", problem)
	}
	// LZ4 that runs past what the header says
	broken := append([]byte{}, module...)
	le.PutUint32(broken[0x18:], le.Uint32(broken[0x18:])-10)
	if problem := checkNso(broken, int64(len(broken)), 1000); !strings.HasPrefix(problem, "its text segment does not decompress to the") {
		t.Errorf("broken: %q", problem)
	}
	// a size no LZ4 data of its length can decompress to
	huge := append([]byte{}, module...)
	le.PutUint32(huge[0x38:], 1<<29)
	if problem := checkNso(huge, int64(len(huge)), 1000); problem != "its data segment cannot decompress to the 536870912 bytes its header says" {
		t.Errorf("huge: %q", problem)
	}
}

func TestNsoCheckLeavesOtherFilesAlone(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("NSO"), nupNoise(3000, 1), []byte("NPDM made up")} {
		if problem := checkNso(data, int64(len(data)), 100); problem != "" {
			t.Errorf("%q: %s", data[:min(8, len(data))], problem)
		}
	}
	// segments compressed in another way are checked by the size only
	module := madeUpNso(true)
	other := append([]byte{}, module...)
	other[0x0C] |= nsoFlagOther
	other[0x100+3] ^= 0xFF
	if problem := checkNso(other, int64(len(other)), 1000); problem != "" {
		t.Errorf("other compression: %s", problem)
	}
	if problem := checkNso(other[:len(other)-1], int64(len(other)-1), 1000); !strings.HasPrefix(problem, "it is 1 bytes shorter") {
		t.Errorf("other compression cut short: %q", problem)
	}
}

func TestIsCodeModule(t *testing.T) {
	for path, want := range map[string]bool{
		"exefs/main": true, "exefs/rtld": true, "exefs/sdk": true, "exefs/subsdk0": true, "exefs/subsdk9": true,
		"ExeFS/MAIN": true, "base/exefs/main": true, "exefs/main.npdm": false, "romfs/main": false, "main": false,
		"exefs/subsdk": false, "exefs/subsdk10": false, "exefs/maindata": false,
	} {
		if IsCodeModule(path) != want {
			t.Errorf("%s: %v", path, !want)
		}
	}
}

// nupWithCode is a game and its update, each with its code modules, the main of the update the
// one given.
func nupWithCode(main []byte) []testnup.Package {
	good := madeUpNso(true)
	return []testnup.Package{
		{TitleId: 0x0400A3A01EE4C000, Kind: testnup.Base, Version: 0,
			Files: []testnup.File{
				{Path: "exefs/main", Data: good, Zstd: true},
				{Path: "exefs/rtld", Data: good},
				{Path: "exefs/main.npdm", Data: []byte("NPDM made up")},
				{Path: "romfs/Data/a.bin", Data: nupNoise(5000, 3), Zstd: true},
			}},
		{TitleId: 0x0400A3A01EE4C000, Kind: testnup.Update, Version: 655360,
			Files: []testnup.File{
				{Path: "exefs/main", Data: main, Zstd: true},
				{Path: "exefs/sdk", Data: good, Zstd: true},
				{Path: "exefs/subsdk0", Data: madeUpNso(false)},
			}},
	}
}

func TestVerifyNupChecksItsCode(t *testing.T) {
	good := madeUpNso(true)
	path := writeNup(t, testnup.BuildWith(nupWithCode(good), testnup.Options{Dedup: true}))
	if _, err := VerifyFile(WithThreads(context.Background(), 3), path, nil); err != nil {
		t.Fatalf("good code: %v", err)
	}

	// a dump that lost bytes of its main: the NUP holds it as it was packed, its hashes match
	cut := append(append([]byte{}, good[:0x400]...), good[0x400+980:]...)
	for _, threads := range []int{1, 4} {
		_, err := VerifyFile(WithThreads(context.Background(), threads), writeNup(t, testnup.Build(nupWithCode(cut))), nil)
		var damaged *NupDamagedError
		if !errors.As(err, &damaged) || !damaged.CodeOnly() || len(damaged.Damaged) != 1 {
			t.Fatalf("cut main: %v", err)
		}
		d := damaged.Damaged[0]
		if d.Package != "update v655360" || d.Path != "exefs/main" || !d.Code || d.Reason != "it is 980 bytes shorter than its header says" {
			t.Errorf("damaged %+v", d)
		}
		if message := err.Error(); message != NupCodeDamaged+" (update v655360/exefs/main: it is 980 bytes shorter than its header says)" {
			t.Errorf("message %q", message)
		}
	}

	// a module whose hash in its header does not match its data, besides a file damaged in the NUP
	wrong := append([]byte{}, good...)
	wrong[0xA0] ^= 0xFF
	packages := nupWithCode(wrong)
	nup := testnup.Build(packages)
	damageNup(nup, packages, 0, "romfs/Data/a.bin", 10)
	_, err := VerifyFile(context.Background(), writeNup(t, nup), nil)
	found := damagedNup(t, err)
	if len(found) != 2 || found["update v655360/exefs/main"] != "the SHA-256 of its text segment does not match its header" ||
		found["game v0/romfs/Data/a.bin"] == "" {
		t.Errorf("damaged %v", found)
	}
	var damaged *NupDamagedError
	if errors.As(err, &damaged); damaged.CodeOnly() || !strings.HasPrefix(err.Error(), "game v0/romfs/Data/a.bin: ") {
		t.Errorf("mixed: %v", err)
	}

	// a module damaged in the NUP is damaged by its data, not by the dump
	packages = nupWithCode(good)
	nup = testnup.Build(packages)
	damageNup(nup, packages, 1, "exefs/subsdk0", 0x300)
	_, err = VerifyFile(context.Background(), writeNup(t, nup), nil)
	if found := damagedNup(t, err); len(found) != 1 || found["update v655360/exefs/subsdk0"] != "its SHA-256 does not match" {
		t.Errorf("damaged in the NUP: %v", found)
	}
}
