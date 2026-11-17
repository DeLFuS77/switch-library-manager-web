package switchfs_test

import (
	"bytes"
	"encoding/binary"
	"path/filepath"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/testnsp"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// exefs is an ExeFS (a PFS0) with main and main.npdm, main.npdm after main as in the games,
// padded to 0x1000 so that it repeats whole in the program of a test NSP.
func exefs(npdm []byte) []byte {
	names := []string{"main", "main.npdm"}
	payloads := [][]byte{bytes.Repeat([]byte{0xAA}, 0x300), npdm}
	var table []byte
	offsets := []uint32{}
	for _, name := range names {
		offsets = append(offsets, uint32(len(table)))
		table = append(append(table, name...), 0)
	}
	out := make([]byte, 0x10+0x18*len(names)+len(table))
	copy(out, "PFS0")
	binary.LittleEndian.PutUint32(out[4:], uint32(len(names)))
	binary.LittleEndian.PutUint32(out[8:], uint32(len(table)))
	position := 0
	for i, payload := range payloads {
		entry := out[0x10+0x18*i:]
		binary.LittleEndian.PutUint64(entry[0:], uint64(position))
		binary.LittleEndian.PutUint64(entry[8:], uint64(len(payload)))
		binary.LittleEndian.PutUint32(entry[16:], offsets[i])
		position += len(payload)
	}
	copy(out[0x10+0x18*len(names):], table)
	for _, payload := range payloads {
		out = append(out, payload...)
	}
	return append(out, make([]byte, 0x1000-len(out))...)
}

func inventoryKeys(t *testing.T) {
	t.Helper()
	keysDir := t.TempDir()
	if err := testnsp.WriteKeys(keysDir); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.InitSwitchKeys(keysDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { settings.InitSwitchKeys(t.TempDir()) })
}

// The inventory tells the NCA files with their headers, the certificates and what each CNMT
// lists; main.npdm is read from the ExeFS of the program of the title asked for.
func TestReadInventoryAndNpdm(t *testing.T) {
	inventoryKeys(t)
	npdm := append([]byte("META"), bytes.Repeat([]byte{0x5C}, 0x7C)...)
	path := filepath.Join(t.TempDir(), "Game.nsp")
	titles := []testnsp.Title{
		{Id: 0x0100000000010000, Type: 0x80, Version: 0, Content: string(exefs(npdm))},
		{Id: 0x0100000000010800, Type: 0x81, Version: 65536, Content: "update data "},
	}
	if _, err := testnsp.WriteMultiTitleNsp(path, titles); err != nil {
		t.Fatal(err)
	}
	inventory, err := switchfs.ReadInventory(path)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Card || len(inventory.Ncas) != 4 || inventory.Certificates != 2 || len(inventory.Titles) != 2 {
		t.Fatalf("%+v", inventory)
	}
	for _, nca := range inventory.Ncas {
		if nca.ID == "" || nca.GameCard || nca.RightsID != "" || nca.Signed || !nca.SignatureKnown {
			t.Fatalf("%+v", nca)
		}
	}
	for _, title := range inventory.Titles {
		if len(title.Contents) != 1 || title.Contents[0].Type != 1 {
			t.Fatalf("%+v", title)
		}
	}
	read, err := switchfs.ReadNpdm(path, "0100000000010000")
	if err != nil || !bytes.Equal(read, npdm) {
		t.Fatalf("%q, %v", read, err)
	}
	// the program of the update has no ExeFS that is a PFS0
	if _, err := switchfs.ReadNpdm(path, "0100000000010800"); err == nil {
		t.Fatal("main.npdm of a program without ExeFS")
	}
	if _, err := switchfs.ReadNpdm(path, "0100000000099000"); err == nil {
		t.Fatal("main.npdm of a title not in the file")
	}
}

// A game card tells its partitions; its NCA files are those of the secure one.
func TestReadInventoryOfACard(t *testing.T) {
	inventoryKeys(t)
	path := filepath.Join(t.TempDir(), "Game.xci")
	if _, err := testnsp.WriteXci(path); err != nil {
		t.Fatal(err)
	}
	inventory, err := switchfs.ReadInventory(path)
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.Card || len(inventory.Partitions) != 1 || inventory.Partitions[0] != "secure" || len(inventory.Ncas) != 1 {
		t.Fatalf("%+v", inventory)
	}
}
