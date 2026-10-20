package switchfs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A "SuperNSP" packs the base game, its update and every DLC in one NSP, each content with its
// own NCA and ticket. This checks it compresses to NSZ, verifies, and decompresses back to the
// exact same NSP, byte for byte.
func TestSuperNspRoundTrips(t *testing.T) {
	initNszKeys(t, "")
	folder := t.TempDir()

	// the base has no rights ID (its key is in the key area); the update and the DLC each have
	// their own rights ID and ticket, like a real merged pack
	base := makeProgramNca(t, gameData(0x80000), 3, "")
	updateRights := "0100000000010800000000000000000b"
	update := makeProgramNca(t, gameData(0x60000), 3, updateRights)
	dlc1Rights := "0100000000011001000000000000000c"
	dlc1 := makeProgramNca(t, gameData(0x40000), 3, dlc1Rights)
	dlc2Rights := "0100000000011002000000000000000d"
	dlc2 := makeProgramNca(t, gameData(0x30000), 3, dlc2Rights)
	metaBase := []byte(strings.Repeat("b", 0x200))
	metaUpd := []byte(strings.Repeat("u", 0x200))
	metaDlc := []byte(strings.Repeat("d", 0x200))

	names := []string{
		contentName(base, ".nca"), contentName(metaBase, ".cnmt.nca"),
		contentName(update, ".nca"), contentName(metaUpd, ".cnmt.nca"), updateRights + ".tik", updateRights + ".cert",
		contentName(dlc1, ".nca"), contentName(metaDlc, ".cnmt.nca"), dlc1Rights + ".tik",
		contentName(dlc2, ".nca"), dlc2Rights + ".tik",
		"authoringtool.xml",
	}
	bodies := [][]byte{
		base, metaBase,
		update, metaUpd, makeTicket(updateRights), make([]byte, 0x200),
		dlc1, metaDlc, makeTicket(dlc1Rights),
		dlc2, makeTicket(dlc2Rights),
		[]byte("<xml/>"),
	}
	original := makePFS0(pfs0Magic, names, bodies)
	source := filepath.Join(folder, "Super [0100000000010000][v0].nsp")
	if err := os.WriteFile(source, original, 0o644); err != nil {
		t.Fatal(err)
	}

	target := NszPath(source)
	result, err := CompressNsp(context.Background(), source, target, CompressOptions{Level: LevelBalanced, Workers: 2})
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	// the four program NCA compress; the three small cnmt NCA are stored
	if result.Compressed != 4 {
		t.Fatalf("every content compressed: %+v", result)
	}

	expected, err := HashNcas(context.Background(), source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyNsz(context.Background(), target, expected, nil); err != nil {
		t.Fatalf("verify: %v", err)
	}

	restored := filepath.Join(folder, "restored.nsp")
	if err := DecompressNsz(context.Background(), target, restored, nil); err != nil {
		t.Fatalf("decompress: %v", err)
	}
	data, _ := os.ReadFile(restored)
	if !bytes.Equal(data, original) {
		t.Fatalf("the restored SuperNSP must equal the original byte for byte: %d vs %d bytes", len(data), len(original))
	}
}

// A ticketless SuperNSP (NSC Builder strips the .tik files): the keys of the contents are not
// inside the NSP but in title.keys. This is the case nsz historically failed on. It must
// compress, verify and decompress back byte for byte all the same.
func TestTicketlessSuperNspWithTitleKeys(t *testing.T) {
	updateRights := "0100000000010800000000000000000b"
	dlcRights := "0100000000011001000000000000000c"
	initNszKeys(t, updateRights+" = "+testTitleKey+"\n"+dlcRights+" = "+testTitleKey+"\n")
	folder := t.TempDir()

	base := makeProgramNca(t, gameData(0x80000), 3, "")
	update := makeProgramNca(t, gameData(0x60000), 3, updateRights)
	dlc := makeProgramNca(t, gameData(0x40000), 3, dlcRights)
	meta := []byte(strings.Repeat("m", 0x200))
	// no .tik files at all
	names := []string{contentName(base, ".nca"), contentName(update, ".nca"), contentName(dlc, ".nca"), contentName(meta, ".cnmt.nca"), "info.xml"}
	original := makePFS0(pfs0Magic, names, [][]byte{base, update, dlc, meta, []byte("<xml/>")})
	source := filepath.Join(folder, "SuperTicketless [0100000000010000][v0].nsp")
	if err := os.WriteFile(source, original, 0o644); err != nil {
		t.Fatal(err)
	}

	target := NszPath(source)
	if _, err := CompressNsp(context.Background(), source, target, CompressOptions{Level: LevelBalanced, Workers: 2}); err != nil {
		t.Fatalf("a ticketless pack with the keys in title.keys must compress: %v", err)
	}
	expected, _ := HashNcas(context.Background(), source, nil)
	if err := VerifyNsz(context.Background(), target, expected, nil); err != nil {
		t.Fatalf("verify: %v", err)
	}
	restored := filepath.Join(folder, "restored.nsp")
	if err := DecompressNsz(context.Background(), target, restored, nil); err != nil {
		t.Fatalf("decompress: %v", err)
	}
	data, _ := os.ReadFile(restored)
	if !bytes.Equal(data, original) {
		t.Fatal("the restored ticketless SuperNSP must equal the original byte for byte")
	}

	// without the keys and without tickets, it is refused clearly (ErrNoTitleKey), never corrupted
	initNszKeys(t, "")
	if _, err := CompressNsp(context.Background(), source, filepath.Join(folder, "x.nsz"), CompressOptions{Level: LevelBalanced}); err == nil {
		t.Fatal("without the keys the pack cannot be compressed, and that must be an error")
	}
}
