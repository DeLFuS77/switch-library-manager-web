package db

import (
	bytes2 "bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
)

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
	var file *os.File = nil

	//try to check if there is a new version
	//if so, save the file
	for i, url := range urls {
		if url == "" {
			continue
		}
		// an etag is only meaningful for the URL it came from
		requestEtag := etag
		if i > 0 {
			requestEtag = ""
		}

		bytes, newEtag, err := downloadBytesFromUrl(url, requestEtag)
		if err != nil {
			zap.S().Infof("file [%v] was not downloaded, reason - [%v]", url, err)
			if errors.Is(err, errNotModified) {
				break
			}
			continue
		}

		//validate json structure
		var test map[string]interface{}
		if err = decodeToJsonObject(bytes2.NewReader(bytes), &test); err != nil || len(test) == 0 {
			zap.S().Infof("ignoring new update [%v], reason - [malformed json file]", url)
			continue
		}

		file, err = saveFile(bytes, filePath)
		if err != nil {
			return nil, "", err
		}
		etag = newEtag
		break
	}

	if file == nil {
		//load file
		fileInfo, err := os.Stat(filePath)
		if err != nil || fileInfo.Size() == 0 {
			zap.S().Infof("Local file [%v] is missing, empty or corrupted", filePath)
			return nil, "", errors.New("unable to download " + filepath.Base(filePath))
		}

		file, err = os.Open(filePath)
		if err != nil {
			return nil, "", err
		}
	}

	return file, etag, nil
}

func decodeToJsonObject(reader io.Reader, target interface{}) error {
	err := json.NewDecoder(reader).Decode(target)
	return err
}

var errNotModified = errors.New("no new updates")

func downloadBytesFromUrl(url string, etag string) ([]byte, string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, "", err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := httpClient.Do(req)
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
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

	bytes, _, err := downloadBytesFromUrl(url, "")
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
