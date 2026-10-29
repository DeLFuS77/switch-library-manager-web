package db

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/testnsp"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// A file cut short is told by the error of its reader, never by "EOF" anywhere in a reason
// that has the path of the file: GAMEOFLIFE.nsp, which could not be opened, was offered to be
// deleted with the files cut short.
func TestCutShort(t *testing.T) {
	cases := []struct {
		err    error
		reason string
		want   bool
	}{
		{err: fmt.Errorf("reading the header: %w", io.ErrUnexpectedEOF), want: true},
		{err: errors.New("Invalid NSP file, reason - [failed to read PFS0 header: read 0 of 12 bytes: EOF]"), want: true},
		{err: errors.New("Invalid NSP file, reason - [open /roms/GAMEOFLIFE.nsp: permission denied]"), want: false},
		// reasons kept by an older version, without the flag
		{reason: "failed to read NSP [reason: unexpected EOF]", want: true},
		{reason: "unable to determine title-Id / version - failed to read NCA header EOF", want: true},
		{reason: "failed to read NSP [reason: open /roms/GAMEOFLIFE.nsp: permission denied]", want: false},
		{reason: "failed to read XCI [reason: open /roms/EOF.xci: input/output error]", want: false},
	}
	for _, c := range cases {
		if got := CutShort(c.err, c.reason); got != c.want {
			t.Errorf("%v %q: %v, want %v", c.err, c.reason, got, c.want)
		}
	}
}

// The scan flags a file cut short as such, as it reads it.
func TestScanFlagsAFileCutShort(t *testing.T) {
	// no keys are left loaded for the tests after this one, not even the ones of the computer
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	keysDir := t.TempDir()
	if err := testnsp.WriteKeys(keysDir); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.InitSwitchKeys(keysDir); err != nil {
		t.Fatal(err)
	}
	defer settings.InitSwitchKeys(t.TempDir())
	folder := t.TempDir()
	whole := filepath.Join(t.TempDir(), "whole.nsp")
	data, err := testnsp.WriteNsp(whole)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "GAMEOFLIFE [0100000000010000][v0].nsp"), data[:8], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "Other [0100000000020000][v0].nsp"), []byte("not a game, but long enough to be read"), 0o644); err != nil {
		t.Fatal(err)
	}
	localDB, err := scanLibrary(t, folder)
	if err != nil {
		t.Fatal(err)
	}
	for file, skipped := range localDB.Skipped {
		if cut := file.FileName == "GAMEOFLIFE [0100000000010000][v0].nsp"; skipped.CutShort != cut {
			t.Errorf("%s cut short %v: %+v", file.FileName, cut, skipped)
		}
	}
	if len(localDB.Skipped) != 2 {
		t.Fatalf("both are listed: %v", localDB.Skipped)
	}
}
