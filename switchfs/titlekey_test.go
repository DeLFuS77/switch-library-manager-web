package switchfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMissingTitleKey(t *testing.T) {
	initNszKeys(t, "")
	folder := t.TempDir()
	game := makeProgramNca(t, gameData(0x40000), 3, "")
	licensed := makeProgramNca(t, gameData(0x40000), 3, testRightsId)
	write := func(name string, names []string, files [][]byte) string {
		path := filepath.Join(folder, name)
		if err := os.WriteFile(path, makePFS0(pfs0Magic, names, files), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	withTicket, _ := writeTestNsp(t, folder)
	if missing, err := MissingTitleKey(withTicket); err != nil || missing != "" {
		t.Fatalf("its ticket has the key: %q %v", missing, err)
	}
	plain := write("Plain.nsp", []string{contentName(game, ".nca")}, [][]byte{game})
	if missing, err := MissingTitleKey(plain); err != nil || missing != "" {
		t.Fatalf("no rights ID, no key needed: %q %v", missing, err)
	}
	without := write("Without.nsp", []string{contentName(licensed, ".nca")}, [][]byte{licensed})
	if missing, err := MissingTitleKey(without); err != nil || missing != testRightsId {
		t.Fatalf("no ticket and no title.keys: %q %v", missing, err)
	}

	// the key in title.keys is enough
	initNszKeys(t, testRightsId+" = 00112233445566778899aabbccddeeff\n")
	if missing, err := MissingTitleKey(without); err != nil || missing != "" {
		t.Fatalf("title.keys has it: %q %v", missing, err)
	}
}
