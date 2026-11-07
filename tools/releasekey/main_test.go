package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/selfupdate"
)

// The backup is checked before the secret is set: a folder that is not there, a file that is
// there already or a place inside the repository stop everything first.
func TestCheckBackup(t *testing.T) {
	repository := t.TempDir()
	outside := t.TempDir()
	taken := filepath.Join(outside, "taken.key")
	os.WriteFile(taken, []byte("x"), 0o600)
	for path, ok := range map[string]bool{
		filepath.Join(outside, "slm-signing.key"):    true,
		filepath.Join(outside, "missing", "slm.key"): false,
		taken:                                false,
		filepath.Join(repository, "slm.key"): false,
		filepath.Join(repository, "internal", "slm.key"): false,
	} {
		if err := checkBackup(path, repository); (err == nil) != ok {
			t.Errorf("%s: %v", path, err)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 1 {
		t.Errorf("left in the folder: %v", entries)
	}
}

// The private key goes to the secret and the backup only, never on the screen; the public key
// to release.pub, which a second run does not replace without -force.
func TestRun(t *testing.T) {
	repository := t.TempDir()
	os.MkdirAll(filepath.Join(repository, "internal", "selfupdate"), 0o755)
	keysFile := filepath.Join(repository, "internal", "selfupdate", "release.pub")
	os.WriteFile(keysFile, []byte("# no key yet\n"), 0o644)
	here, _ := os.Getwd()
	os.Chdir(repository)
	defer os.Chdir(here)
	var secret string
	saved := setSecret
	setSecret = func(repo string, text string) error { secret = text; return nil }
	defer func() { setSecret = saved }()

	backup := filepath.Join(t.TempDir(), "slm-signing.key")
	stdout := os.Stdout
	read, write, _ := os.Pipe()
	os.Stdout = write
	err := run("owner/repo", backup, false)
	write.Close()
	os.Stdout = stdout
	printed := make([]byte, 4096)
	n, _ := read.Read(printed)
	if err != nil {
		t.Fatal(err)
	}
	if secret == "" || strings.Contains(string(printed[:n]), secret) {
		t.Fatalf("the secret was not set, or was printed: %q", printed[:n])
	}
	if kept, _ := os.ReadFile(backup); strings.TrimSpace(string(kept)) != secret {
		t.Fatal("the backup is not the key of the secret")
	}
	private, id, err := selfupdate.ParsePrivateKey(secret)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(keysFile)
	keys, err := selfupdate.ParsePublicKeys(string(data))
	if err != nil || len(keys) != 1 || keys[0].ID != id {
		t.Fatalf("release.pub: %v %v", keys, err)
	}
	signature := selfupdate.Sign(private, id, []byte("sums"), selfupdate.Comment("2.0.3"))
	if _, err := selfupdate.Verify([]byte("sums"), signature, keys); err != nil {
		t.Fatalf("the public key is not the one of the secret: %v", err)
	}
	if err := run("owner/repo", "", false); err == nil {
		t.Fatal("a second key replaced the first without -force")
	}
}
