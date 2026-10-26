package web

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// updateTestWeb serves a release with the archive given and its checksum, and puts a program
// that runs in a folder of its own.
func updateTestWeb(t *testing.T, archive []byte, sum string) (*Web, string, *bool) {
	t.Helper()
	web := newTestWeb(t)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.CheckForUpdates = true })
	name := systemArchive("linux", "amd64")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case name:
			w.Write(archive)
		case "SHA256SUMS.txt":
			w.Write([]byte(sum + "  " + name + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	downloads := releaseDownloads
	releaseDownloads = server.URL + "/"
	program := filepath.Join(t.TempDir(), "switch-library-manager-web")
	os.WriteFile(program, []byte("old"), 0o755)
	started := false
	saved := []any{currentProgram, tryProgram, startProgram, stopProgram}
	currentProgram = func() (string, error) { return program, nil }
	tryProgram = func(path string) (string, error) {
		data, _ := os.ReadFile(path)
		if string(data) != "new" {
			return "", errors.New("does not run")
		}
		return "1.70.0", nil
	}
	startProgram = func(string) error { started = true; return nil }
	stopProgram = func() {}
	t.Cleanup(func() {
		releaseDownloads = downloads
		currentProgram = saved[0].(func() (string, error))
		tryProgram = saved[1].(func(string) (string, error))
		startProgram = saved[2].(func(string) error)
		stopProgram = saved[3].(func())
	})
	files, ok := filesOfRelease(map[string]string{name: server.URL + "/1.70.0/" + name, "SHA256SUMS.txt": server.URL + "/1.70.0/SHA256SUMS.txt"}, name)
	if !ok {
		t.Fatal("files of the release")
	}
	web.updates.latest = &AppUpdate{Version: "1.70.0", files: files}
	return web, program, &started
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
	if !*started {
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
	if data, _ := os.ReadFile(program); string(data) != "old" || *started {
		t.Fatalf("the program was changed: %q", data)
	}
	if _, err := os.Stat(program + ".new"); err == nil {
		t.Fatal("the new program was left")
	}
	// nothing is downloaded while a task works on the files
	web.organizing.Store(true)
	if err := web.installUpdate(); err != errOrganizeBusy {
		t.Fatalf("while a task runs: %v", err)
	}
}
