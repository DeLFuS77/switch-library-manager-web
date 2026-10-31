package db

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrImageTooLarge is an image refused for being larger than asked.
var ErrImageTooLarge = errors.New("the image is too large")

// DownloadImage gets an image at an address the user gave, refusing answers larger than limit.
// As the covers of the titles database, it comes from the internet only (see publicClient).
func DownloadImage(url string, limit int64) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return nil, errors.New("only web addresses are downloaded")
	}
	response, err := publicClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the server answered %s", response.Status)
	}
	if response.ContentLength > limit {
		return nil, ErrImageTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrImageTooLarge
	}
	return data, nil
}
