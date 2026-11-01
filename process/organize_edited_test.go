package process

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// Organize names the files and folders of a game with the name the user gave it by hand, also
// one in Japanese the titles database would give way to a name in Latin letters; nothing is
// moved before Organize is asked to.
func TestOrganizeUsesTheNameGivenByHand(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	local, remote := fixtureLibrary(t, source)
	edited := *remote
	edited.Edited = map[string]db.EditedTitle{strings.ToUpper(baseID): {Name: "My Own Name"}}

	ops, err := OrganizeByFolders(target, local, &edited, organizeOptions(), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	moved := 0
	for _, op := range ops {
		if op.Kind != OP_MOVE {
			continue
		}
		moved++
		if !strings.HasPrefix(op.To, filepath.Join(target, "My Own Name")+string(filepath.Separator)) || !strings.HasPrefix(filepath.Base(op.To), "My Own Name") {
			t.Errorf("named with the name given: %s", op.To)
		}
	}
	if moved != 4 {
		t.Fatalf("the game, its update and its DLC: %+v", ops)
	}
	assertExists(t, filepath.Join(source, "base.nsp"), true)

	// the name of the titles database again, once the change is undone
	ops, err = OrganizeByFolders(target, local, remote, organizeOptions(), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Kind == OP_MOVE && !strings.HasPrefix(op.To, filepath.Join(target, gameTitle)) {
			t.Errorf("named as the titles database: %s", op.To)
		}
	}

	// a name in Japanese given by hand is kept when the names are not only for the Switch
	edited.Edited = map[string]db.EditedTitle{strings.ToUpper(baseID): {Name: "テストゲーム"}}
	options := organizeOptions()
	options.SwitchSafeFileNames = false
	ops, err = OrganizeByFolders(target, local, &edited, options, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Kind == OP_MOVE && !strings.HasPrefix(op.To, filepath.Join(target, "テストゲーム")) {
			t.Errorf("named in Japanese: %s", op.To)
		}
	}
}
