// Command releasekey makes the key the releases of Switch Library Manager Web are signed with,
// which the app checks before it installs a new version by itself. The maintainer runs it once,
// on their own computer, with gh signed in:
//
//	go run ./tools/releasekey -backup FILE
//
// The private key goes straight into the secret SLM_SIGNING_KEY of the repository, through gh,
// and is never shown nor kept; only the public key is written, to internal/selfupdate/release.pub,
// which the app is built with (commit it). With -backup FILE the private key is written to FILE
// too, outside the repository, for the maintainer to keep somewhere safe (a password manager) and
// then delete: with it a lost secret can be set again; without it, a new key is made, and the
// copies of the app built with the old public key only tell of the next versions.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/internal/selfupdate"
)

const secretName = "SLM_SIGNING_KEY"

func main() {
	repo := flag.String("repo", "DeLFuS77/switch-library-manager-web", "the repository whose secret takes the key")
	backup := flag.String("backup", "", "also write the private key to this file, outside the repository")
	force := flag.Bool("force", false, "make a new key though the app has one")
	flag.Parse()
	if err := run(*repo, *backup, *force); err != nil {
		fmt.Fprintln(os.Stderr, "releasekey:", err)
		os.Exit(1)
	}
}

// checkBackup tells whether the backup can be written where it is asked, before anything is
// made: outside the repository, in a folder that is there and takes files, and not over a file.
// Checked before the secret is set, so nothing changes when it cannot be written.
func checkBackup(backup string, repository string) error {
	whole, err := filepath.Abs(backup)
	if err != nil {
		return err
	}
	if strings.HasPrefix(strings.ToLower(whole), strings.ToLower(repository)+string(filepath.Separator)) {
		return fmt.Errorf("keep the backup outside the repository, not in %s", backup)
	}
	folder := filepath.Dir(whole)
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		return fmt.Errorf("the folder of the backup is not there: %s", folder)
	}
	if _, err := os.Lstat(whole); err == nil {
		return fmt.Errorf("%s is there already: give another name", backup)
	}
	probe, err := os.CreateTemp(folder, ".releasekey-*")
	if err != nil {
		return fmt.Errorf("the backup cannot be written in %s: %w", folder, err)
	}
	probe.Close()
	os.Remove(probe.Name())
	return nil
}

// setSecret puts the private key in the secret of the repository; a variable for the tests.
var setSecret = func(repo string, secret string) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh is needed to put the key in the secret: %w", err)
	}
	// the private key goes to gh by its input, never on the command line nor on the screen
	command := exec.Command("gh", "secret", "set", secretName, "--repo", repo)
	command.Stdin = strings.NewReader(secret)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	return command.Run()
}

func run(repo, backup string, force bool) error {
	keysFile := filepath.Join("internal", "selfupdate", "release.pub")
	current, err := os.ReadFile(keysFile)
	if err != nil {
		return fmt.Errorf("run it from the folder of the app: %w", err)
	}
	if keys, _ := selfupdate.ParsePublicKeys(string(current)); len(keys) > 0 && !force {
		return fmt.Errorf("the app has a key already (%s): a new one leaves the copies built with it telling of new versions only; give -force to make one all the same", keysFile)
	}
	if backup != "" {
		here, _ := filepath.Abs(".")
		if err := checkBackup(backup, here); err != nil {
			return err
		}
	}

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	secret := selfupdate.PrivateKeyText(private, id)
	if err := setSecret(repo, secret); err != nil {
		return fmt.Errorf("the secret could not be set, nothing was changed: %w", err)
	}
	if backup != "" {
		if err := os.WriteFile(backup, []byte(secret+"\n"), 0o600); err != nil {
			return fmt.Errorf("the secret is set, but the backup could not be written: %w", err)
		}
	}
	key := selfupdate.PublicKey{ID: id, Key: public}
	header := "# The public keys the releases of Switch Library Manager Web are signed with (minisign), written\n# by tools/releasekey. With none, the app only tells of a new version.\n"
	if err := os.WriteFile(keysFile, []byte(header+key.Text()), 0o644); err != nil {
		return fmt.Errorf("the secret is set, but %s could not be written: %w", keysFile, err)
	}
	fmt.Printf("The private key is in the secret %s of %s.\n", secretName, repo)
	if backup != "" {
		fmt.Printf("Its backup is %s: keep it somewhere safe, and never in the repository.\n", backup)
	}
	fmt.Printf("The public key is in %s: commit it, and the next release is signed.\n", keysFile)
	return nil
}
