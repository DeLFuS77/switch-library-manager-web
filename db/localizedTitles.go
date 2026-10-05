package db

import (
	"encoding/json"
	"io"
	"strings"
)

// LocalizedTitle is the name and description of a title in another language.
type LocalizedTitle struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// LoadLocalizedTitles reads a titles.<lang>.json file: title ID -> name and description.
func LoadLocalizedTitles(reader io.Reader) (map[string]LocalizedTitle, error) {
	titles := map[string]LocalizedTitle{}
	if err := json.NewDecoder(reader).Decode(&titles); err != nil {
		return nil, err
	}
	result := make(map[string]LocalizedTitle, len(titles))
	for id, title := range titles {
		if title.Name != "" {
			result[strings.ToUpper(id)] = title
		}
	}
	return result, nil
}
