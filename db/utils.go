package db

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"
)

const (
	// the titles database is more than 100 MB; nothing it downloads is larger than this
	maxDownloadSize = 1 << 30
	// a cover or screenshot
	maxImageSize = 20 << 20
)

// publicClient downloads the covers named by the titles database: only from addresses on
// the internet, so a changed database cannot make the app call services of the local network.
var publicClient = &http.Client{
	Timeout: 2 * time.Minute,
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second,
			Control: publicAddressOnly,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		return nil
	},
}

// AllowLocalDownloads lets the tests download covers from servers on this computer.
var AllowLocalDownloads = false

// publicAddressOnly refuses connections to this computer and to private networks, unless
// the downloads go through a proxy (which is then on the local network).
func publicAddressOnly(network string, address string, _ syscall.RawConn) error {
	if AllowLocalDownloads {
		return nil
	}
	if os.Getenv("HTTPS_PROXY") != "" || os.Getenv("https_proxy") != "" || os.Getenv("HTTP_PROXY") != "" || os.Getenv("http_proxy") != "" {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Errorf("address %s is not on the internet", host)
	}
	return nil
}

var httpClient = &http.Client{
	// titles.json is >100MB, so allow slow connections to finish the download
	Timeout: 10 * time.Minute,
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
}

type ProgressUpdater interface {
	UpdateProgress(curr int, total int, message string)
}

// LoadAndUpdateFile downloads a JSON file from the first URL that answers with a valid
// document and stores it at filePath. If no URL provides a newer version, the local copy
// is used. The returned file must be closed by the caller.
func LoadAndUpdateFile(urls []string, filePath string, etag string) (*os.File, string, error) {
	result, err := LoadAndUpdate(urls, filePath, etag)
	return result.File, result.Etag, err
}

// UpdateResult is the outcome of LoadAndUpdate.
type UpdateResult struct {
	// the up to date file, to be closed by the caller
	File *os.File
	Etag string
	// a new version was downloaded; false when the local copy is used
	Updated bool
}

// LoadAndUpdate is LoadAndUpdateFile, also telling whether a new version was downloaded.
// Downloads are streamed to a temporary file, checked for truncation and for a valid JSON
// document, and only then replace the local copy, so an interrupted download never
// damages it. Temporary failures are retried before the next URL is tried.
func LoadAndUpdate(urls []string, filePath string, etag string) (UpdateResult, error) {
	tmpName := filePath + ".tmp"
	defer os.Remove(tmpName)

	for i, url := range urls {
		if url == "" {
			continue
		}
		// an etag is only meaningful for the URL it came from
		requestEtag := etag
		if i > 0 {
			requestEtag = ""
		}

		newEtag, err := downloadWithRetries(url, requestEtag, tmpName)
		if err != nil {
			zap.S().Infof("file [%v] was not downloaded, reason - [%v]", url, err)
			if errors.Is(err, errNotModified) {
				break
			}
			continue
		}

		if err := validateJsonObjectFile(tmpName); err != nil {
			zap.S().Infof("ignoring new update [%v], reason - [malformed json file: %v]", url, err)
			continue
		}

		if err := os.Rename(tmpName, filePath); err != nil {
			return UpdateResult{}, err
		}
		file, err := os.Open(filePath)
		if err != nil {
			return UpdateResult{}, err
		}
		return UpdateResult{File: file, Etag: newEtag, Updated: true}, nil
	}

	fileInfo, err := os.Stat(filePath)
	if err != nil || fileInfo.Size() == 0 {
		zap.S().Infof("Local file [%v] is missing, empty or corrupted", filePath)
		return UpdateResult{}, errors.New("unable to download " + filepath.Base(filePath))
	}

	file, err := os.Open(filePath)
	if err != nil {
		return UpdateResult{}, err
	}
	return UpdateResult{File: file, Etag: etag}, nil
}

// downloadAttempts and retryDelay control how often a temporary failure is retried; the
// delay doubles after every attempt.
var (
	downloadAttempts = 3
	retryDelay       = 2 * time.Second
)

// errPermanent marks failures that a retry cannot fix, such as a 404.
type errPermanent struct{ error }

func downloadWithRetries(url string, etag string, target string) (string, error) {
	delay := retryDelay
	var err error
	for attempt := 1; attempt <= downloadAttempts; attempt++ {
		var newEtag string
		newEtag, err = downloadToFile(url, etag, target)
		var permanent errPermanent
		if err == nil || errors.Is(err, errNotModified) || errors.As(err, &permanent) {
			return newEtag, err
		}
		if attempt < downloadAttempts {
			zap.S().Infof("downloading [%v] failed (attempt %v of %v), retrying in %v - %v", url, attempt, downloadAttempts, delay, err)
			time.Sleep(delay)
			delay *= 2
		}
	}
	return "", err
}

// downloadToFile streams url into target and returns the etag of the response.
func downloadToFile(url string, etag string, target string) (string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", errPermanent{err}
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return "", errNotModified
	}
	if resp.StatusCode != http.StatusOK {
		err := errors.New("got a non 200 response - " + resp.Status)
		// server errors and rate limits are usually temporary
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return "", err
		}
		return "", errPermanent{err}
	}

	file, err := os.Create(target)
	if err != nil {
		return "", errPermanent{err}
	}
	written, copyErr := io.Copy(file, resp.Body)
	closeErr := file.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", errPermanent{closeErr}
	}
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		return "", fmt.Errorf("download incomplete, got %v of %v bytes", written, resp.ContentLength)
	}

	return resp.Header.Get("Etag"), nil
}

// validateJsonObjectFile checks that the file holds one non-empty JSON object. The
// document is read token by token, so a file of hundreds of megabytes is not loaded.
func validateJsonObjectFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := json.NewDecoder(bufio.NewReader(file))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return errors.New("not a JSON object")
	}
	if !decoder.More() {
		return errors.New("empty JSON object")
	}
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("unexpected data after the JSON object")
	}
	return nil
}

func decodeToJsonObject(reader io.Reader, target interface{}) error {
	err := json.NewDecoder(reader).Decode(target)
	return err
}

var errNotModified = errors.New("no new updates")

func downloadBytesFromUrl(url string, etag string) ([]byte, string, error) {
	return download(httpClient, url, etag, maxDownloadSize)
}

// download gets a URL, refusing answers larger than limit.
func download(client *http.Client, url string, etag string, limit int64) ([]byte, string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, "", err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return nil, "", errNotModified
	}

	if resp.StatusCode != http.StatusOK {
		return nil, "", errors.New("got a non 200 response - " + resp.Status)
	}

	if resp.ContentLength > limit {
		return nil, "", errors.New("the file is too large")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > limit {
		return nil, "", errors.New("the file is too large")
	}
	//getting the new etag
	return body, resp.Header.Get("Etag"), nil
}

// saveFile writes the content to a temporary file first, so an interrupted write never
// replaces a valid file with a truncated one.
func saveFile(bytes []byte, fileName string) (*os.File, error) {
	tmpName := fileName + ".tmp"
	if err := os.WriteFile(tmpName, bytes, 0644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpName, fileName); err != nil {
		os.Remove(tmpName)
		return nil, err
	}

	return os.Open(fileName)
}

// DownloadFile downloads url to filePath unless a non-empty copy already exists.
func DownloadFile(url string, filePath string) error {
	if fileInfo, err := os.Stat(filePath); err == nil && fileInfo.Size() > 0 {
		return nil
	}

	if !strings.HasPrefix(url, "https://") && !AllowLocalDownloads {
		return errors.New("only https downloads are allowed")
	}
	bytes, _, err := download(publicClient, url, "", maxImageSize)
	if err != nil {
		zap.S().Infof("file [%v] was not downloaded, reason - [%v]", url, err)
		return err
	}

	file, err := saveFile(bytes, filePath)
	if err != nil {
		return err
	}
	return file.Close()
}
