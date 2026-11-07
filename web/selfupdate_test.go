package web

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/selfupdate"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func TestSystemArchive(t *testing.T) {
	for system, want := range map[string]string{
		"windows/amd64": "switch-library-manager-web-windows-x64.zip",
		"darwin/arm64":  "switch-library-manager-web-macos-apple.tar.gz",
		"linux/arm":     "switch-library-manager-web-linux-armv7.tar.gz",
		"freebsd/amd64": "",
	} {
		parts := strings.Split(system, "/")
		if got := systemArchive(parts[0], parts[1]); got != want {
			t.Errorf("%s: %q", system, got)
		}
	}
}

// Only the files of the releases of the app are downloaded.
func TestFilesOfRelease(t *testing.T) {
	archive := "switch-library-manager-web-linux-x64.tar.gz"
	assets := map[string]string{archive: releaseDownloads + "1.70.0/" + archive, "SHA256SUMS.txt": releaseDownloads + "1.70.0/SHA256SUMS.txt"}
	if _, ok := filesOfRelease(assets, archive); !ok {
		t.Fatal("the files of the release")
	}
	assets[archive] = "https://example.com/" + archive
	if _, ok := filesOfRelease(assets, archive); ok {
		t.Fatal("a file from anywhere else")
	}
}

func TestChecksumOf(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	list := []byte(sum + "  switch-library-manager-web-linux-x64.tar.gz\n" + strings.Repeat("cd", 32) + " *other.zip\n")
	if got, ok := checksumOf(list, "switch-library-manager-web-linux-x64.tar.gz"); !ok || got != sum {
		t.Fatalf("%q %v", got, ok)
	}
	if _, ok := checksumOf(list, "missing.zip"); ok {
		t.Fatal("a file not listed")
	}
}

func zipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	buffer := &bytes.Buffer{}
	writer := zip.NewWriter(buffer)
	for name, content := range files {
		file, _ := writer.Create(name)
		file.Write([]byte(content))
	}
	writer.Close()
	return buffer.Bytes()
}

func tarArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	buffer := &bytes.Buffer{}
	zipped := gzip.NewWriter(buffer)
	writer := tar.NewWriter(zipped)
	for name, content := range files {
		writer.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
		writer.Write([]byte(content))
	}
	writer.Close()
	zipped.Close()
	return buffer.Bytes()
}

// The program is the one file of the folder of the app, whatever else the archive holds.
func TestProgramOf(t *testing.T) {
	files := map[string]string{"switch-library-manager-web/README.md": "readme", "../switch-library-manager-web.exe": "bad", "switch-library-manager-web/switch-library-manager-web.exe": "program"}
	if program, err := programOf(zipArchive(t, files), "a.zip", "switch-library-manager-web.exe"); err != nil || string(program) != "program" {
		t.Fatalf("zip: %q %v", program, err)
	}
	files = map[string]string{"switch-library-manager-web/switch-library-manager-web": "program"}
	if program, err := programOf(tarArchive(t, files), "a.tar.gz", "switch-library-manager-web"); err != nil || string(program) != "program" {
		t.Fatalf("tar: %q %v", program, err)
	}
	if _, err := programOf(tarArchive(t, files), "a.tar.gz", "other"); err == nil {
		t.Fatal("no such program")
	}
}

// testSigningKey makes a key the app takes for the signatures of its releases, while the test
// runs, and gives how it signs.
func testSigningKey(t *testing.T) func(data []byte, version string) []byte {
	t.Helper()
	private, key := throwawayKey(t)
	saved := updateSigningKeys
	updateSigningKeys = []selfupdate.PublicKey{key}
	t.Cleanup(func() { updateSigningKeys = saved })
	return func(data []byte, version string) []byte {
		return selfupdate.Sign(private, key.ID, data, selfupdate.Comment(version))
	}
}

func throwawayKey(t *testing.T) (ed25519.PrivateKey, selfupdate.PublicKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := selfupdate.PublicKey{Key: public}
	rand.Read(key.ID[:])
	return private, key
}

// testRelease is what the release of the tests serves: its checksums, and their signature
// (nil: none, 404)
type testRelease struct {
	sums, signature []byte
	sign            func(data []byte, version string) []byte
}

// updateTestWeb serves a release with the archive given and its checksum, signed with a key the
// app takes, and puts a program that runs in a folder of its own.
func updateTestWeb(t *testing.T, archive []byte, sum string) (*Web, string, *atomic.Bool) {
	web, program, started, _ := signedUpdateTestWeb(t, archive, sum)
	return web, program, started
}

// signedUpdateTestWeb is updateTestWeb, with the release it serves (to change) and how the key
// signs.
func signedUpdateTestWeb(t *testing.T, archive []byte, sum string) (*Web, string, *atomic.Bool, *testRelease) {
	t.Helper()
	web := newTestWeb(t)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.CheckForUpdates = true })
	name := systemArchive("linux", "amd64")
	sign := testSigningKey(t)
	release := &testRelease{sums: []byte(sum + "  " + name + "\n"), sign: sign}
	release.signature = sign(release.sums, "1.70.0")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case name:
			w.Write(archive)
		case "SHA256SUMS.txt":
			w.Write(release.sums)
		case sumsSignatureName:
			if release.signature == nil {
				http.NotFound(w, r)
				return
			}
			w.Write(release.signature)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	downloads := releaseDownloads
	releaseDownloads = server.URL + "/"
	program := filepath.Join(t.TempDir(), "switch-library-manager-web")
	os.WriteFile(program, []byte("old"), 0o755)
	started := &atomic.Bool{}
	saved := []any{currentProgram, tryProgram, startProgram, stopProgram, afterAnswer}
	currentProgram = func() (string, error) { return program, nil }
	tryProgram = func(path string) (string, error) {
		data, _ := os.ReadFile(path)
		if string(data) != "new" {
			return "", errors.New("does not run")
		}
		return "1.70.0", nil
	}
	startProgram = func(string) error { started.Store(true); return nil }
	stopProgram = func() {}
	afterAnswer = func(then func()) { then() }
	t.Cleanup(func() {
		releaseDownloads = downloads
		currentProgram = saved[0].(func() (string, error))
		tryProgram = saved[1].(func(string) (string, error))
		startProgram = saved[2].(func(string) error)
		stopProgram = saved[3].(func())
		afterAnswer = saved[4].(func(func()))
	})
	files, ok := filesOfRelease(map[string]string{name: server.URL + "/1.70.0/" + name, "SHA256SUMS.txt": server.URL + "/1.70.0/SHA256SUMS.txt",
		sumsSignatureName: server.URL + "/1.70.0/" + sumsSignatureName}, name)
	if !ok {
		t.Fatal("files of the release")
	}
	web.updates.latest = &AppUpdate{Version: "1.70.0", files: files}
	return web, program, started, release
}

// The new program replaces the one that runs, which stays as .old, and is started.
func TestInstallUpdate(t *testing.T) {
	archive := tarArchive(t, map[string]string{"switch-library-manager-web/switch-library-manager-web": "new"})
	sum := sha256.Sum256(archive)
	web, program, started := updateTestWeb(t, archive, hex.EncodeToString(sum[:]))
	if err := web.installUpdate(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(program); string(data) != "new" {
		t.Fatalf("the program: %q", data)
	}
	if data, _ := os.ReadFile(program + ".old"); string(data) != "old" {
		t.Fatalf("the program of before: %q", data)
	}
	if !started.Load() {
		t.Fatal("the new program was not started")
	}
}

// A download that does not match its checksum, or a program that does not run, changes nothing.
func TestInstallUpdateRefused(t *testing.T) {
	archive := tarArchive(t, map[string]string{"switch-library-manager-web/switch-library-manager-web": "new"})
	web, program, started := updateTestWeb(t, archive, strings.Repeat("00", 32))
	if err := web.installUpdate(); err != errUpdateChecksum {
		t.Fatalf("a wrong checksum: %v", err)
	}
	broken := tarArchive(t, map[string]string{"switch-library-manager-web/switch-library-manager-web": "broken"})
	sum := sha256.Sum256(broken)
	web, program, started = updateTestWeb(t, broken, hex.EncodeToString(sum[:]))
	if err := web.installUpdate(); err != errUpdateProgram {
		t.Fatalf("a program that does not run: %v", err)
	}
	if data, _ := os.ReadFile(program); string(data) != "old" || started.Load() {
		t.Fatalf("the program was changed: %q", data)
	}
	if _, err := os.Stat(program + ".new"); err == nil {
		t.Fatal("the new program was left")
	}
	// nothing is downloaded while a task works on the files
	web.organizing.Store(true)
	if err := web.installUpdate(); err != errUpdateBusy {
		t.Fatalf("while a task runs: %v", err)
	}
}

// Going back puts the program of before in place and keeps the newer one, to come back to it.
func TestRollBack(t *testing.T) {
	archive := tarArchive(t, map[string]string{"switch-library-manager-web/switch-library-manager-web": "new"})
	sum := sha256.Sum256(archive)
	web, program, started := updateTestWeb(t, archive, hex.EncodeToString(sum[:]))
	if previousVersion() != "" {
		t.Fatal("nothing to go back to before an update")
	}
	if err := web.installUpdate(); err != nil {
		t.Fatal(err)
	}
	if previousVersion() != settings.SLM_WEB_VERSION || web.organizing.Load() {
		t.Fatalf("the version of before: %q", previousVersion())
	}
	started.Store(false)
	if err := web.rollBack(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(program); string(data) != "old" || !started.Load() {
		t.Fatalf("the program of before is back: %q", data)
	}
	if data, _ := os.ReadFile(program + ".old"); string(data) != "new" {
		t.Fatalf("the newer one is kept: %q", data)
	}
}

// The notes of a release are read up to its downloads.
func TestReleaseSections(t *testing.T) {
	body := "### New\r\n\r\n- One\r\n- Two\r\n\r\n### Fixes\n\n- Three\n\n### Downloads\n\n- not a change\n"
	sections := releaseSections(body)
	if len(sections) != 2 || sections[0].Title != "New" || len(sections[0].Items) != 2 || sections[1].Items[0] != "Three" {
		t.Fatalf("%+v", sections)
	}
}

// At night the new version is installed by itself, only with the setting on and between 3 and 5.
func TestInstallAtNight(t *testing.T) {
	quickUpdateWait(t)
	archive := tarArchive(t, map[string]string{"switch-library-manager-web/switch-library-manager-web": "new"})
	sum := sha256.Sum256(archive)
	web, _, started := updateTestWeb(t, archive, hex.EncodeToString(sum[:]))
	night := time.Date(2026, 10, 27, 3, 30, 0, 0, time.Local)
	if web.installAtNight(night) || started.Load() {
		t.Fatal("installed with the setting off")
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.AutoInstallUpdates = true })
	if web.installAtNight(night.Add(3 * time.Hour)) {
		t.Fatal("installed in the day")
	}
	if selfUpdatable() && (!web.installAtNight(night) || !started.Load()) {
		t.Fatal("not installed at night")
	}
}

// An update is installed only with a signature of its checksums made by a key of the app for
// that version: a missing signature, another key, checksums changed after they were signed or a
// signature of another version change nothing, and an app built without a key installs none.
func TestInstallUpdateChecksTheSignature(t *testing.T) {
	archive := tarArchive(t, map[string]string{"switch-library-manager-web/switch-library-manager-web": "new"})
	sum := sha256.Sum256(archive)
	good := hex.EncodeToString(sum[:])
	refusedAs := func(want error, what string, change func(web *Web, release *testRelease)) {
		t.Helper()
		web, program, started, release := signedUpdateTestWeb(t, archive, good)
		change(web, release)
		if err := web.installUpdate(); err != want {
			t.Fatalf("%s: %v", what, err)
		}
		if data, _ := os.ReadFile(program); string(data) != "old" || started.Load() {
			t.Fatalf("%s: the program was changed: %q", what, data)
		}
		if web.organizing.Load() || web.updateHold.Load() || web.state.IsSynchronizing() {
			t.Fatalf("%s: the files are still held", what)
		}
	}
	refused := func(what string, change func(web *Web, release *testRelease)) {
		t.Helper()
		refusedAs(errUpdateSigned, what, change)
	}
	refused("a wrong key", func(_ *Web, release *testRelease) {
		private, other := throwawayKey(t)
		release.signature = selfupdate.Sign(private, other.ID, release.sums, selfupdate.Comment("1.70.0"))
	})
	refused("tampered checksums", func(_ *Web, release *testRelease) {
		release.sums = []byte(strings.Repeat("00", 32) + "  " + systemArchive("linux", "amd64") + "\n")
	})
	refused("a signature of another version", func(_ *Web, release *testRelease) {
		release.signature = release.sign(release.sums, "1.69.0")
	})
	// listed in the release, but not there: nothing is installed either
	refusedAs(errUpdateDownload, "a missing signature", func(web *Web, release *testRelease) {
		release.signature = nil
	})
	refused("a release without a signature", func(web *Web, _ *testRelease) {
		web.updates.latest.files.signatureUrl = ""
		if web.updates.latest.CanInstall() || web.installable() != errUpdateSigned {
			t.Fatal("a release without a signature can be installed")
		}
	})
	refused("an app built without a key", func(web *Web, _ *testRelease) {
		updateSigningKeys = nil
		if web.updates.latest.CanInstall() {
			t.Fatal("an app without a key installs")
		}
	})

	// the signature is taken only from the releases of the app
	files, _ := filesOfRelease(map[string]string{"a.tar.gz": releaseDownloads + "a.tar.gz", "SHA256SUMS.txt": releaseDownloads + "SHA256SUMS.txt",
		sumsSignatureName: "https://example.com/" + sumsSignatureName}, "a.tar.gz")
	if files.signatureUrl != "" || files.signed() {
		t.Fatal("a signature from anywhere else")
	}

	// a good one is installed
	web, program, started, _ := signedUpdateTestWeb(t, archive, good)
	if !web.updates.latest.files.signed() {
		t.Fatal("a signed release is not taken")
	}
	if err := web.installUpdate(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(program); string(data) != "new" || !started.Load() {
		t.Fatalf("the program: %q", data)
	}
}
