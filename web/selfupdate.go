package web

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The app updates itself where it runs as a program (Windows, macOS, Linux): the archive of its
// system is downloaded from the release on GitHub, checked against SHA256SUMS.txt of the same
// release, and its program is tried before it replaces the one that runs, which is kept as
// .old. The app then starts the new program and stops. A container is updated by Docker
// instead: the app has no right to replace its own image.

const (
	appName = "switch-library-manager-web"
	// the most an archive and the list of checksums may weigh
	maxArchiveSize = 300 << 20
	maxSumsSize    = 64 << 10
	// how long the new program has to say its version
	tryNewProgramFor = 30 * time.Second
	// the environment of the new program: it waits for the port the old one leaves
	restartedEnv = "SLM_RESTARTED"
)

// releaseDownloads is where the files of the releases of the app are; nothing else is downloaded.
var releaseDownloads = "https://github.com/" + settings.REPOSITORY_OWNER + "/" + appName + "/releases/download/"

var appDownloadClient = &http.Client{Timeout: 10 * time.Minute}

// the program that runs, how a program says its version, how the new one is started and how
// this one stops; variables for the tests
var (
	currentProgram = os.Executable
	tryProgram     = programVersion
	startProgram   = restartProgram
	stopProgram    = func() {
		// the answer reaches the browser, then the port is left to the new program
		time.Sleep(time.Second)
		os.Exit(0)
	}
)

// releaseFiles are the files of a release the update needs: the archive of this system and the
// checksums.
type releaseFiles struct {
	archive, archiveUrl string
	sumsUrl             string
}

// systemArchive is the name of the archive of a release for a system, "" for one without.
func systemArchive(goos string, goarch string) string {
	system := map[string]string{
		"windows/amd64": "windows-x64", "windows/arm64": "windows-arm64",
		"darwin/arm64": "macos-apple", "darwin/amd64": "macos-intel",
		"linux/amd64": "linux-x64", "linux/arm64": "linux-arm64", "linux/arm": "linux-armv7",
	}[goos+"/"+goarch]
	if system == "" {
		return ""
	}
	if goos == "windows" {
		return appName + "-" + system + ".zip"
	}
	return appName + "-" + system + ".tar.gz"
}

// filesOfRelease picks the files of this system among the assets of a release, only from the
// releases of the app.
func filesOfRelease(assets map[string]string, archive string) (releaseFiles, bool) {
	files := releaseFiles{archive: archive, archiveUrl: assets[archive], sumsUrl: assets["SHA256SUMS.txt"]}
	ok := archive != "" && strings.HasPrefix(files.archiveUrl, releaseDownloads) && strings.HasPrefix(files.sumsUrl, releaseDownloads)
	return files, ok
}

// selfUpdatable tells whether this app can update itself: a program of a release, not in a
// container nor the demo, in a folder it can write.
func selfUpdatable() bool {
	if runningInContainer() || isDemoMode() || systemArchive(runtime.GOOS, runtime.GOARCH) == "" {
		return false
	}
	program, err := os.Executable()
	if err != nil {
		return false
	}
	probe, err := os.CreateTemp(filepath.Dir(program), ".update-*")
	if err != nil {
		return false
	}
	probe.Close()
	os.Remove(probe.Name())
	return true
}

var (
	errUpdateNone     = errorString("No newer version is known.")
	errUpdateDownload = errorString("The new version could not be downloaded: try again later.")
	errUpdateChecksum = errorString("The download does not match the checksum of the release: nothing was changed.")
	errUpdateProgram  = errorString("The new version does not start on this computer: nothing was changed.")
	errUpdateReplace  = errorString("The program could not be replaced: nothing was changed. Update it by hand.")
)

// installing is set while an update is downloaded and installed
var installing atomic.Bool

// installUpdate downloads, checks and puts in place the newer version found, and starts it. The
// program that runs stays as .old.
func (web *Web) installUpdate() error {
	update := web.availableUpdate()
	if update == nil || update.files.archiveUrl == "" {
		return errUpdateNone
	}
	if !web.organizing.CompareAndSwap(false, true) {
		return errOrganizeBusy
	}
	release := func() { web.organizing.Store(false) }
	if web.compressionRunning() || web.measuring.Load() || !installing.CompareAndSwap(false, true) {
		release()
		return errOrganizeBusy
	}
	defer installing.Store(false)

	program, err := currentProgram()
	if err == nil {
		program, err = filepath.EvalSymlinks(program)
	}
	if err != nil {
		release()
		return errUpdateReplace
	}
	sums, err := download(update.files.sumsUrl, maxSumsSize)
	if err != nil {
		release()
		return errUpdateDownload
	}
	want, ok := checksumOf(sums, update.files.archive)
	if !ok {
		release()
		return errUpdateChecksum
	}
	archive, err := download(update.files.archiveUrl, maxArchiveSize)
	if err != nil {
		release()
		return errUpdateDownload
	}
	if sum := sha256.Sum256(archive); hex.EncodeToString(sum[:]) != want {
		release()
		return errUpdateChecksum
	}
	newProgram, err := programOf(archive, update.files.archive, filepath.Base(program))
	if err != nil {
		release()
		return errUpdateProgram
	}
	next := program + ".new"
	if err := os.WriteFile(next, newProgram, 0o755); err != nil {
		release()
		return errUpdateReplace
	}
	if version, err := tryProgram(next); err != nil || version != update.Version {
		os.Remove(next)
		web.sugarLogger.Warnf("[Update] the new program says version %q (%v)", version, err)
		release()
		return errUpdateProgram
	}
	old := program + ".old"
	os.Remove(old)
	if err := os.Rename(program, old); err != nil {
		os.Remove(next)
		release()
		return errUpdateReplace
	}
	if err := os.Rename(next, program); err != nil {
		os.Rename(old, program)
		release()
		return errUpdateReplace
	}
	web.sugarLogger.Infof("[Update] version %s installed, the app starts again", update.Version)
	if err := startProgram(program); err != nil {
		// the program of before, which still runs, stays
		os.Remove(program)
		os.Rename(old, program)
		release()
		return errUpdateReplace
	}
	go stopProgram()
	return nil
}

// download reads a file of a release, refusing one heavier than limit.
func download(url string, limit int64) ([]byte, error) {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", appName+"/"+settings.SLM_WEB_VERSION)
	response, err := appDownloadClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("too big")
	}
	return data, nil
}

// checksumOf finds the SHA-256 of a file in a list made by sha256sum ("<hash>  <name>").
func checksumOf(sums []byte, name string) (string, bool) {
	scanner := bufio.NewScanner(bytes.NewReader(sums))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name && len(fields[0]) == 64 {
			if _, err := hex.DecodeString(fields[0]); err == nil {
				return strings.ToLower(fields[0]), true
			}
		}
	}
	return "", false
}

// programOf takes the program out of an archive of a release: only the file
// switch-library-manager-web/<program>, whatever else the archive holds.
func programOf(archive []byte, name string, program string) ([]byte, error) {
	wanted := appName + "/" + program
	if strings.HasSuffix(name, ".zip") {
		reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, file := range reader.File {
			if file.Name == wanted && file.Mode().IsRegular() {
				opened, err := file.Open()
				if err != nil {
					return nil, err
				}
				defer opened.Close()
				return io.ReadAll(io.LimitReader(opened, maxArchiveSize))
			}
		}
		return nil, errors.New("no program in the archive")
	}
	unzipped, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	reader := tar.NewReader(unzipped)
	for {
		header, err := reader.Next()
		if err != nil {
			return nil, errors.New("no program in the archive")
		}
		if strings.TrimPrefix(header.Name, "./") == wanted && header.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(reader, maxArchiveSize))
		}
	}
}

// programVersion runs a program with --version: the new version must start here and be the one
// expected.
func programVersion(program string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tryNewProgramFor)
	defer cancel()
	output, err := exec.CommandContext(ctx, program, "--version").Output()
	return strings.TrimSpace(string(output)), err
}

// restartProgram starts the program again, with the arguments and the environment of this one;
// it waits for the port this one leaves, and opens no browser.
func restartProgram(program string) error {
	command := exec.Command(program, os.Args[1:]...)
	command.Env = append(os.Environ(), restartedEnv+"=1", "SLM_OPEN_BROWSER=false")
	command.Stdout, command.Stderr, command.Stdin = os.Stdout, os.Stderr, os.Stdin
	if dir, err := os.Getwd(); err == nil {
		command.Dir = dir
	}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

// restarted tells a program started by an update, which waits for the port of the one before.
func restarted() bool {
	return os.Getenv(restartedEnv) == "1"
}

func (web *Web) HandleSelfUpdate() {
	web.router.HandleFunc("/update/install", func(w http.ResponseWriter, r *http.Request) {
		// read-only users cannot reach it (see viewerAllowed)
		lang := web.requestLanguage(r)
		if !selfUpdatable() {
			writeGlobalError(w, http.StatusBadRequest, lang, errUpdateReplace.Error())
			return
		}
		if err := web.installUpdate(); err != nil {
			status := http.StatusBadGateway
			if err == errOrganizeBusy || err == errUpdateNone {
				status = http.StatusConflict
			}
			web.sugarLogger.Warnf("[Update] not installed: %v", err)
			writeGlobalError(w, status, lang, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"restarting": true})
	}).Methods("POST")
	// the version that runs, for the page that waits for the new one
	web.router.HandleFunc("/update/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]string{"version": settings.SLM_WEB_VERSION})
	}).Methods("GET")
}
