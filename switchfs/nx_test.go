package switchfs

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/testnx"
)

// nxTestFile writes a .nx file of a made-up game with the versions given; corrupt changes it
// first.
func nxTestFile(t *testing.T, snapshots []testnx.Snapshot, corrupt func([]byte)) string {
	t.Helper()
	data := testnx.Build(testnx.Game{Id: 0x0100000000010000, Title: "Test Game", Publisher: "Test Publisher",
		NacpName: "NACP Name", Snapshots: snapshots})
	if corrupt != nil {
		corrupt(data)
	}
	path := filepath.Join(t.TempDir(), "game.nx")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

var nxTestGame = []testnx.Snapshot{
	{Kind: testnx.Base, Version: 0, Display: "1.0.0"},
	{Kind: testnx.Update, Version: 131072, Display: "1.2.0"},
	{Kind: testnx.Update, Version: 65536, Display: "1.1.0"},
	{Kind: testnx.Dlc, Version: 0, Addon: 0x0100000000011001},
	{Kind: testnx.Dlc, Version: 0}, // from an NSP: its ID is in its CNMT
}

func TestReadNxMetadata(t *testing.T) {
	path := nxTestFile(t, nxTestGame, nil)
	if !IsNxFile(path) || !IsEmulatorFile(path) || IsNupFile(path) {
		t.Fatal("the file is not told as a .nx file")
	}
	metadata, err := ReadNxMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 3 {
		t.Fatalf("got %d titles, want the game, its newest update and the DLC with an ID", len(metadata))
	}
	game := metadata["0100000000010000"]
	if game == nil || game.Type != "BASE" || game.Version != 0 || game.Ncap == nil {
		t.Fatalf("game: %+v", game)
	}
	// the game of a file whose NACP belongs to its update gets the names of the catalog
	if name := game.Ncap.TitleName["AmericanEnglish"].Title; name != "Test Game" {
		t.Errorf("game name %q", name)
	}
	update := metadata["0100000000010800"]
	if update == nil || update.Type != "UPD" || update.Version != 131072 || update.Ncap == nil {
		t.Fatalf("update: %+v", update)
	}
	if name := update.Ncap.TitleName["AmericanEnglish"].Title; name != "NACP Name" || update.Ncap.DisplayVersion != "1.2.0" {
		t.Errorf("update NACP %q %q", name, update.Ncap.DisplayVersion)
	}
	dlc := metadata["0100000000011001"]
	if dlc == nil || dlc.Type != "DLC" {
		t.Fatalf("DLC: %+v", dlc)
	}
	archive, err := ReadNx(path)
	if err != nil {
		t.Fatal(err)
	}
	if icon, err := archive.Icon(); err != nil || !bytes.HasPrefix(icon, []byte{0xFF, 0xD8, 0xFF}) {
		t.Errorf("icon: %v", err)
	}
	if app := archive.Applications[0]; app.Platform != 1 || app.Publisher != "Test Publisher" || len(app.Snapshots) != 5 {
		t.Errorf("game of the archive: %+v", app)
	}
}

func TestReadNxDamaged(t *testing.T) {
	cases := []struct {
		name    string
		corrupt func([]byte)
		want    string
	}{
		{"not a .nx", func(b []byte) { copy(b[0:], "NOPE"); copy(b[nxSlotB:], "NOPE") }, "not a .nx file"},
		{"cut short", func(b []byte) {}, "cut short"},
		{"both headers damaged", func(b []byte) { b[20]++; b[nxSlotB+20]++ }, "damaged"},
		{"catalog damaged", func(b []byte) { b[len(b)-200] ^= 1 }, "SHA-256"},
		{"unknown required feature", func(b []byte) {
			for _, slot := range []int{0, nxSlotB} {
				h := b[slot : slot+nxHeaderSize]
				binary.LittleEndian.PutUint16(h[12:], 3)
				testnx.Seal(h)
			}
		}, "features"},
	}
	for _, c := range cases {
		path := nxTestFile(t, nxTestGame, c.corrupt)
		if c.name == "cut short" {
			data, _ := os.ReadFile(path)
			os.WriteFile(path, data[:len(data)-10], 0o644)
		}
		_, err := ReadNx(path)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error with %q", c.name, err, c.want)
		}
	}
	// one damaged header: the other one is used
	path := nxTestFile(t, nxTestGame, func(b []byte) { b[20]++ })
	if _, err := ReadNx(path); err != nil {
		t.Errorf("one damaged header: %v", err)
	}
}

// TestNxFixtures reads the files of the conformance tests of the format when NX_FIXTURES names
// the folder that holds them with their fixture_index.json. The reader only checks what the
// library needs (headers, catalog, media and the hashes of the manifest and the index), so the
// files that are only wrong deeper in the manifest or in their blocks are read.
func TestNxFixtures(t *testing.T) {
	dir := os.Getenv("NX_FIXTURES")
	if dir == "" {
		t.Skip("NX_FIXTURES is not set")
	}
	var index struct {
		Fixtures []struct {
			File     string  `json:"file"`
			Expected string  `json:"expected"`
			Error    *string `json:"expected_error"`
		} `json:"fixtures"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "fixture_index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &index); err != nil {
		t.Fatal(err)
	}
	deeper := map[string]bool{"block_hash": true, "path": true, "manifest": true}
	notChecked := map[string]bool{"invalid_shared_media_types.nx": true, "slot_generation_conflict.nx": true}
	for _, f := range index.Fixtures {
		path := filepath.Join(dir, f.File)
		_, err := ReadNx(path)
		switch {
		case f.Expected == "accept" && err != nil:
			t.Errorf("%s: %v", f.File, err)
		case f.Expected != "accept" && err == nil && f.Error != nil && !deeper[*f.Error] && !notChecked[f.File]:
			t.Errorf("%s: read, want it refused (%s)", f.File, *f.Error)
		}
		if err != nil {
			continue
		}
		// the data: a file wrong deeper is refused when opened or verified
		data, err := OpenNxData(path)
		if err == nil {
			err = data.Verify(t.Context(), nil)
			data.Close()
		}
		switch {
		case f.Expected == "accept" && err != nil:
			t.Errorf("%s: verify: %v", f.File, err)
		case f.Expected != "accept" && err == nil && !notChecked[f.File]:
			t.Errorf("%s: verified, want it refused (%v)", f.File, f.Error)
		}
	}
}
