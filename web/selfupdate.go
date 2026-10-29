package web

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
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
	"golang.org/x/crypto/blake2b"
)

// The app updates itself where it runs as a program (Windows, macOS, Linux): the archive of its
// system is downloaded from the release on GitHub, checked against SHA256SUMS.txt of the same
// release, and its program is tried before it replaces the one that runs, which is kept as
// .old. The app then starts the new program and stops. A container is updated by Docker
// instead: the app has no right to replace its own image.
//
// With releasePublicKey set, SHA256SUMS.txt must also carry a minisign signature of that key
// (SHA256SUMS.txt.minisig in the release): the checksums then prove the archive was made by
// whoever holds the secret key, not only that it is the one of the release on GitHub.

// releasePublicKey is the minisign public key the releases are signed with: the second line of
// minisign.pub. Empty, the checksums of the release are trusted as GitHub serves them.
const releasePublicKey = ""

// updateSigningKey is the key the update checks the signature with; a variable for the tests.
var updateSigningKey = releasePublicKey

const (
	appName = "switch-library-manager-web"
	// the most an archive and the list of checksums may weigh
	maxArchiveSize = 300 << 20
	maxSumsSize    = 64 << 10
	// the name of the signature of the checksums in a release, and the most it may weigh
	sumsSignatureName = "SHA256SUMS.txt.minisig"
	maxSignatureSize  = 4 << 10
	// how long the new program has to say its version
	tryNewProgramFor = 30 * time.Second
	// the environment of the new program: it waits for the port the old one leaves
	restartedEnv = "SLM_RESTARTED"
)

// releaseDownloads is where the files of the releases of the app are; nothing else is downloaded.
var releaseDownloads = "https://github.com/" + settings.REPOSITORY_OWNER + "/" + appName + "/releases/download/"

var appDownloadClient = &http.Client{Timeout: 10 * time.Minute}

// the program that runs, how a program says its version, how the next one is started, how this
// one stops and when, once the answer has left; variables for the tests
var (
	currentProgram = os.Executable
	tryProgram     = programVersion
	startProgram   = restartProgram
	stopProgram    = func() { os.Exit(0) }
	afterAnswer    = func(then func()) {
		go func() {
			time.Sleep(500 * time.Millisecond)
			then()
		}()
	}
)

// releaseFiles are the files of a release the update needs: the archive of this system, the
// checksums and their signature ("" when the release has none).
type releaseFiles struct {
	archive, archiveUrl string
	sumsUrl             string
	signatureUrl        string
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
	if signature := assets[sumsSignatureName]; strings.HasPrefix(signature, releaseDownloads) {
		files.signatureUrl = signature
	}
	return files, ok
}

// signed tells whether the checksums of a release can be trusted: there is no key to check them
// with, or the release has a signature to check.
func (f releaseFiles) signed() bool {
	return updateSigningKey == "" || f.signatureUrl != ""
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
	errUpdateSigned   = errorString("The signature of the release is missing or wrong: nothing was changed.")
	errUpdateProgram  = errorString("The new version does not start on this computer: nothing was changed.")
	errUpdateReplace  = errorString("The program could not be replaced: nothing was changed. Update it by hand.")
	errUpdateNoOld    = errorString("There is no version of before to go back to.")
)

// installing is set while an update is downloaded and installed
var installing atomic.Bool

// updateProgress is what the update does now, for the page that waits: the step, and the bytes
// of the archive downloaded of all of them (0 when not known)
var updateProgress struct {
	step        atomic.Value
	done, total atomic.Int64
}

const (
	UPDATE_STEP_DOWNLOAD = "download"
	UPDATE_STEP_CHECK    = "check"
	UPDATE_STEP_RESTART  = "restart"
)

// countingReader counts the bytes downloaded in updateProgress.
type countingReader struct {
	reader io.Reader
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	updateProgress.done.Add(int64(n))
	return n, err
}

// oldVersionFile keeps the version of the program kept as .old, written by the update: only a
// program kept so can be gone back to.
func oldVersionFile(program string) string {
	return program + ".old.version"
}

// previousVersion is the version the app can go back to, "" when there is none.
func previousVersion() string {
	program, err := runningProgram()
	if err != nil {
		return ""
	}
	if _, err := os.Stat(program + ".old"); err != nil {
		return ""
	}
	data, err := os.ReadFile(oldVersionFile(program))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func runningProgram() (string, error) {
	program, err := currentProgram()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(program)
}

// holdFiles keeps the files of the library still while the program is replaced: no task may
// run, nor start, meanwhile. The function given ends it.
func (web *Web) holdFiles() (func(), error) {
	if !web.organizing.CompareAndSwap(false, true) {
		return nil, errOrganizeBusy
	}
	if web.compressionRunning() || web.measuring.Load() || !installing.CompareAndSwap(false, true) {
		web.organizing.Store(false)
		return nil, errOrganizeBusy
	}
	return func() {
		installing.Store(false)
		web.organizing.Store(false)
	}, nil
}

// installUpdate downloads, checks and puts in place the newer version found, and starts it once
// the answer has left. The program that runs stays as .old.
func (web *Web) installUpdate() error {
	update := web.availableUpdate()
	if update == nil || update.files.archiveUrl == "" {
		return errUpdateNone
	}
	end, err := web.holdFiles()
	if err != nil {
		return err
	}
	updateProgress.step.Store(UPDATE_STEP_DOWNLOAD)
	updateProgress.done.Store(0)
	updateProgress.total.Store(0)
	program, err := runningProgram()
	if err != nil {
		end()
		return errUpdateReplace
	}
	sums, err := download(update.files.sumsUrl, maxSumsSize, false)
	if err != nil {
		end()
		return errUpdateDownload
	}
	if updateSigningKey != "" {
		if !update.files.signed() {
			end()
			return errUpdateSigned
		}
		signature, err := download(update.files.signatureUrl, maxSignatureSize, false)
		if err != nil {
			end()
			return errUpdateDownload
		}
		if err := verifyMinisign(updateSigningKey, sums, signature); err != nil {
			web.sugarLogger.Warnf("[Update] the checksums of version %s are not signed by the key of the app: %v", update.Version, err)
			end()
			return errUpdateSigned
		}
	}
	want, ok := checksumOf(sums, update.files.archive)
	if !ok {
		end()
		return errUpdateChecksum
	}
	archive, err := download(update.files.archiveUrl, maxArchiveSize, true)
	if err != nil {
		end()
		return errUpdateDownload
	}
	updateProgress.step.Store(UPDATE_STEP_CHECK)
	if sum := sha256.Sum256(archive); hex.EncodeToString(sum[:]) != want {
		end()
		return errUpdateChecksum
	}
	newProgram, err := programOf(archive, update.files.archive, filepath.Base(program))
	if err != nil {
		end()
		return errUpdateProgram
	}
	next := program + ".new"
	if err := os.WriteFile(next, newProgram, 0o755); err != nil {
		end()
		return errUpdateReplace
	}
	if version, err := tryProgram(next); err != nil || version != update.Version {
		os.Remove(next)
		web.sugarLogger.Warnf("[Update] the new program says version %q (%v)", version, err)
		end()
		return errUpdateProgram
	}
	if err := swapPrograms(program, next, settings.SLM_WEB_VERSION); err != nil {
		os.Remove(next)
		end()
		return errUpdateReplace
	}
	web.sugarLogger.Infof("[Update] version %s installed, the app starts again", update.Version)
	web.restartWith(program, end)
	return nil
}

// rollBack puts back the program of before, kept as .old, and starts it once the answer has
// left; the one that runs is kept as .old in turn, to come back to it.
func (web *Web) rollBack() error {
	previous := previousVersion()
	if previous == "" {
		return errUpdateNoOld
	}
	end, err := web.holdFiles()
	if err != nil {
		return err
	}
	program, err := runningProgram()
	if err != nil {
		end()
		return errUpdateReplace
	}
	back := program + ".back"
	os.Remove(back)
	if err := os.Rename(program+".old", back); err != nil {
		end()
		return errUpdateReplace
	}
	if err := swapPrograms(program, back, settings.SLM_WEB_VERSION); err != nil {
		os.Rename(back, program+".old")
		end()
		return errUpdateReplace
	}
	web.sugarLogger.Infof("[Update] back to version %s, the app starts again", previous)
	web.restartWith(program, end)
	return nil
}

// swapPrograms puts next in the place of program, which is kept as .old with its version.
func swapPrograms(program string, next string, version string) error {
	old := program + ".old"
	os.Remove(old)
	if err := os.Rename(program, old); err != nil {
		return err
	}
	if err := os.Rename(next, program); err != nil {
		os.Rename(old, program)
		return err
	}
	os.WriteFile(oldVersionFile(program), []byte(version+"\n"), 0o644)
	return nil
}

// restartWith starts the program put in place once the answer has left: this app leaves its
// port first, so a version that does not wait for it starts too. If the program cannot be
// started, the one of before is put back and started instead.
func (web *Web) restartWith(program string, end func()) {
	updateProgress.step.Store(UPDATE_STEP_RESTART)
	afterAnswer(func() {
		if web.server != nil {
			web.server.Close()
		}
		if err := startProgram(program); err != nil {
			web.sugarLogger.Errorf("[Update] the new program could not be started: %v", err)
			old := program + ".old"
			os.Remove(program)
			os.Rename(old, program)
			os.Remove(oldVersionFile(program))
			if err := startProgram(program); err != nil {
				web.sugarLogger.Errorf("[Update] the program of before could not be started either: %v", err)
			}
		}
		end()
		stopProgram()
	})
}

// download reads a file of a release, refusing one heavier than limit; the bytes read are
// counted in updateProgress when count is set.
func download(url string, limit int64, count bool) ([]byte, error) {
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
	var body io.Reader = response.Body
	if count {
		updateProgress.total.Store(response.ContentLength)
		body = &countingReader{reader: body}
	}
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
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

// verifyMinisign checks a minisign signature of data with a public key, as minisign -V does:
// the key is the base64 line of minisign.pub (the algorithm "Ed", its id, the Ed25519 key), the
// signature has a line of the same id with the signature of the file ("Ed" of the file itself,
// "ED" of its BLAKE2b-512 hash, as minisign makes by default) and a trusted comment signed with it.
func verifyMinisign(publicKey string, data []byte, signature []byte) error {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKey))
	if err != nil || len(key) != 2+8+ed25519.PublicKeySize || string(key[:2]) != "Ed" {
		return errors.New("the public key cannot be read")
	}
	lines := strings.Split(strings.ReplaceAll(string(signature), "\r\n", "\n"), "\n")
	if len(lines) < 4 || !strings.HasPrefix(lines[2], "trusted comment: ") {
		return errors.New("the signature cannot be read")
	}
	signed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil || len(signed) != 2+8+ed25519.SignatureSize {
		return errors.New("the signature cannot be read")
	}
	if !bytes.Equal(signed[2:10], key[2:10]) {
		return errors.New("signed with another key")
	}
	publicEd := ed25519.PublicKey(key[10:])
	message := data
	switch string(signed[:2]) {
	case "Ed":
	case "ED":
		hash := blake2b.Sum512(data)
		message = hash[:]
	default:
		return errors.New("an unknown kind of signature")
	}
	if !ed25519.Verify(publicEd, message, signed[10:]) {
		return errors.New("the signature does not match")
	}
	global, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[3]))
	comment := strings.TrimPrefix(lines[2], "trusted comment: ")
	if err != nil || !ed25519.Verify(publicEd, append(append([]byte{}, signed[10:]...), comment...), global) {
		return errors.New("the trusted comment does not match")
	}
	return nil
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
	// read-only users cannot reach the requests that change something (see viewerAllowed)
	install := func(path string, action func() error) {
		web.router.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			lang := web.requestLanguage(r)
			if !selfUpdatable() {
				writeGlobalError(w, http.StatusBadRequest, lang, errUpdateReplace.Error())
				return
			}
			if err := action(); err != nil {
				status := http.StatusBadGateway
				if err == errOrganizeBusy || err == errUpdateNone || err == errUpdateNoOld {
					status = http.StatusConflict
				}
				web.sugarLogger.Warnf("[Update] %s: %v", path, err)
				writeGlobalError(w, status, lang, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"restarting": true})
		}).Methods("POST")
	}
	install("/update/install", web.installUpdate)
	install("/update/rollback", web.rollBack)
	// Check now: GitHub is asked at once instead of once a day
	web.router.HandleFunc("/update/check", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		if err := web.checkForUpdate(); err != nil {
			writeGlobalError(w, http.StatusBadGateway, lang, "GitHub could not be reached: try again later.")
			return
		}
		version := ""
		if update := web.availableUpdate(); update != nil {
			version = update.Version
		}
		writeJSON(w, http.StatusOK, map[string]string{"version": version})
	}).Methods("POST")
	// what the update does now, for the bar of the page
	web.router.HandleFunc("/update/progress", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		step, _ := updateProgress.step.Load().(string)
		writeJSON(w, http.StatusOK, map[string]any{"step": step, "done": updateProgress.done.Load(), "total": updateProgress.total.Load()})
	}).Methods("GET")
	// the version that runs, for the page that waits for the new one
	web.router.HandleFunc("/update/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]string{"version": settings.SLM_WEB_VERSION})
	}).Methods("GET")
}
