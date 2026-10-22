package switchfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCardXci writes a game card dump with a game: its program NCA and its Meta NCA.
func writeCardXci(t *testing.T, folder string) string {
	t.Helper()
	programNca := makeProgramNca(t, gameData(0x20000), 3, "")
	programName := contentName(programNca, ".nca")
	var program [16]byte
	hex.Decode(program[:], []byte(strings.TrimSuffix(programName, ".nca")))
	cnmt := make([]byte, 0x20+0x38)
	binary.LittleEndian.PutUint64(cnmt[0:8], 0x0100000000010000)
	cnmt[0xC] = ContentMetaType_Application
	binary.LittleEndian.PutUint16(cnmt[0x10:0x12], 1)
	copy(cnmt[0x20+0x20:], program[:])
	cnmt[0x20+0x36] = 1
	meta := makeSyntheticNCA(t, pad512(makePFS0(pfs0Magic, []string{"Application.cnmt"}, [][]byte{cnmt})), 1)
	secure := makePFS0(hfs0Magic, []string{programName, contentName(meta, ".cnmt.nca")}, [][]byte{programNca, meta})
	update := makePFS0(hfs0Magic, []string{"firmware.nca"}, [][]byte{bytes.Repeat([]byte{7}, 0x1000)})
	root := makePFS0(hfs0Magic, []string{"update", "secure"}, [][]byte{update, secure})
	header := make([]byte, 0xF000)
	copy(header[0x100:], "HEAD")
	binary.LittleEndian.PutUint64(header[0x130:], 0xF000)
	path := filepath.Join(folder, "Game [0100000000010000][v0].xci")
	if err := os.WriteFile(path, append(header, root...), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPackIntoCard(t *testing.T) {
	initNszKeys(t, "")
	folder := t.TempDir()
	card := writeCardXci(t, folder)
	oldUpdate := writeTitleNsp(t, folder, 0x0100000000010800, 65536, ContentMetaType_Patch, 1)
	newUpdate := writeTitleNsp(t, folder, 0x0100000000010800, 131072, ContentMetaType_Patch, 2)
	dlc := writeTitleNsp(t, folder, 0x0100000000011001, 0, ContentMetaType_AddOnContent, 3)

	target := filepath.Join(folder, "complete.xci")
	replaced, check, err := PackIntoCard(context.Background(), card, []string{oldUpdate, dlc, newUpdate}, target, nil)
	if err != nil || len(check.Modified) != 0 || len(replaced) != 1 || replaced[0] != "0100000000010800 v65536" {
		t.Fatalf("pack: %v %+v %v", replaced, check, err)
	}
	titles, err := ReadXciMetadata(target)
	if err != nil || len(titles) != 3 || titles["0100000000010800"] == nil || titles["0100000000010800"].Version != 131072 || titles["0100000000011001"] == nil || titles["0100000000010000"] == nil {
		t.Fatalf("the game, its newest update and its DLC: %v %v", titles, err)
	}
	original, _ := os.ReadFile(card)
	packed, _ := os.ReadFile(target)
	if !bytes.Equal(original[:0xF000], packed[:0xF000]) {
		t.Fatal("the header of the card is kept")
	}
	if _, err := VerifyFile(context.Background(), target, nil); err != nil {
		t.Fatal(err)
	}

	// compressed, it is an XCZ that decompresses back to it
	xcz := filepath.Join(folder, "complete.xcz")
	if _, err := CompressGame(context.Background(), target, xcz, CompressOptions{}); err != nil {
		t.Fatal(err)
	}
	back := filepath.Join(folder, "back.xci")
	if _, err := DecompressAndCheck(context.Background(), xcz, back, nil); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(back); !bytes.Equal(again, packed) {
		t.Fatal("the XCZ decompresses to the packed card")
	}

	// a card first, NSP or NSZ after
	if _, _, err := PackIntoCard(context.Background(), dlc, []string{card}, filepath.Join(folder, "x.xci"), nil); err == nil {
		t.Fatal("the first file must be a card")
	}
	if _, _, err := PackIntoCard(context.Background(), card, []string{card}, filepath.Join(folder, "y.xci"), nil); err == nil {
		t.Fatal("one card only")
	}
}
