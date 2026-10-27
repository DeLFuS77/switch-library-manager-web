package switchfs_test

import (
	"path/filepath"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/testnsp"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// An NCA a tool wrote has no signature of Nintendo: its header is told unsigned, by name. The
// games of Nintendo pass the same check (tried with the games of the user, which cannot ship).
func TestCheckSignaturesTellsUnsignedHeaders(t *testing.T) {
	keysDir := t.TempDir()
	if err := testnsp.WriteKeys(keysDir); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.InitSwitchKeys(keysDir); err != nil {
		t.Fatal(err)
	}
	defer settings.InitSwitchKeys(t.TempDir())
	path := filepath.Join(t.TempDir(), "Game [0100000000010000][v0].nsp")
	if _, err := testnsp.WriteNsp(path); err != nil {
		t.Fatal(err)
	}
	check, err := switchfs.CheckSignatures(path)
	if err != nil {
		t.Fatal(err)
	}
	if check.Checked == 0 || len(check.Unsigned)+len(check.Unknown) != check.Checked {
		t.Fatalf("%+v", check)
	}
}
