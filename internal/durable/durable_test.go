package durable

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCommitFileKeepsTheBytes(t *testing.T) {
	folder := t.TempDir()
	temporary := filepath.Join(folder, ".game.nsz.tmp")
	target := filepath.Join(folder, "game.nsz")
	data := bytes.Repeat([]byte("switch"), 1000)
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CommitFile(temporary, target); err != nil {
		t.Fatal(err)
	}
	if read, err := os.ReadFile(target); err != nil || !bytes.Equal(read, data) {
		t.Fatalf("the file must keep its bytes: %v", err)
	}
	if _, err := os.Stat(temporary); !os.IsNotExist(err) {
		t.Fatal("the temporary name must be gone")
	}
}

func TestCommitFileOfAMissingFileFails(t *testing.T) {
	folder := t.TempDir()
	target := filepath.Join(folder, "game.nsz")
	if err := CommitFile(filepath.Join(folder, ".gone.tmp"), target); err == nil {
		t.Fatal("a temporary file that is not there must fail")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("nothing must take the name")
	}
}
